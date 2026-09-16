package testes

// Cenários de concorrência da seção 8.2 do PROJETO.md, exercitados pelo
// protocolo real por socket, exatamente como um cliente faria.
//
// Os oito testes deste arquivo atacam propriedades diferentes:
//
//	T1 — nunca vender o mesmo assento duas vezes (RNF05);
//	T2 — a confirmação de itinerário é tudo-ou-nada (RNF06);
//	T3 — a disponibilidade é controlada por trecho (RF11);
//	T4 — nenhum assento fica permanentemente bloqueado (RNF07);
//	T5 — o cancelamento em cascata não deixa assento órfão (I1 e I4);
//	T6 — conexão que cai no meio de uma linha não executa nada (RNF04);
//	T7 — lixo no protocolo é recusado sem derrubar ninguém (RNF02);
//	T8 — reservas sobrepostas do mesmo passageiro se excluem (D14).
//
// Todos usam a mesma estrutura: N conexões já autenticadas esperando em uma
// barreira, liberadas de uma vez pelo fechamento de um canal. Sem a barreira,
// o custo do LOGIN espalharia as requisições no tempo e a disputa que o teste
// quer provocar simplesmente não aconteceria.
//
// Rodar sem -race não conta como verificação: uma corrida pode não se
// manifestar em uma execução isolada.

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// --- Apoio comum ---

// motoristasDaCarga e passageirosDaCarga reproduzem dados/usuarios.json
// (PROJETO.md, seção 9.1). Os 50 usuários genéricos existem justamente para
// que T1 e T8 possam autenticar 50 conexões distintas.
func motoristasDaCarga() []credencial {
	return []credencial{{"joao", "1234"}, {"carlos", "1234"}, {"ana", "1234"}}
}

func passageirosDaCarga() []credencial {
	lista := []credencial{{"maria", "abcd"}, {"pedro", "abcd"}, {"lucia", "abcd"}}
	for i := 1; i <= 50; i++ {
		lista = append(lista, credencial{fmt.Sprintf("teste%02d", i), "teste"})
	}
	return lista
}

type credencial struct{ usuario, senha string }

// caronaObservada é o que o verificador de invariantes consegue enxergar de
// uma carona usando só a interface pública do protocolo.
type caronaObservada struct {
	rota        []string
	horarios    []time.Time
	assentos    int
	cancelada   bool
	livres      []int
	passageiros []int // quantidade de passageiros confirmados por trecho
}

// observarCaronas reconstrói o estado de todas as caronas pela mesma porta que
// os usuários reais usam (PROJETO.md, seção 8.1): autentica-se como cada
// motorista, lista as caronas dele e detalha uma a uma.
//
// Não existe operação administrativa no protocolo para espiar o estado, e isso
// é deliberado: o verificador valida o sistema pela interface que o enunciado
// especifica, e não por uma porta dos fundos que só o teste conhece.
//
// Uma única conexão atende a todos os motoristas, alternando com LOGOUT e
// LOGIN. É de graça e ainda confere, de passagem, que a identidade é mesmo da
// conexão e trocável em voo (D08).
func observarCaronas(t *testing.T, endereco string) map[string]caronaObservada {
	t.Helper()

	observado := make(map[string]caronaObservada)
	c := conectar(t, endereco)

	for _, m := range motoristasDaCarga() {
		c.entrar(m.usuario, m.senha)

		var lista protocolo.ListarMinhasCaronasResposta
		c.exigirOK(protocolo.TipoListarMinhasCaronas,
			protocolo.ListarMinhasCaronasRequisicao{IncluirCanceladas: true}, &lista)
		// Uma listagem no limite pode ter sido cortada (D16), e verificar só
		// parte das caronas passaria sem ter verificado tudo.
		if len(lista.Caronas) >= dominio.MAXIMO_ITENS_LISTAGEM {
			t.Fatalf("%s tem %d caronas listadas, o limite da listagem: o verificador não enxergaria as demais",
				m.usuario, len(lista.Caronas))
		}

		for _, resumo := range lista.Caronas {
			var detalhe protocolo.DetalharCaronaResposta
			c.exigirOK(protocolo.TipoDetalharCarona,
				protocolo.DetalharCaronaRequisicao{CaronaID: resumo.CaronaID}, &detalhe)

			carona := caronaObservada{
				rota:      resumo.Rota,
				horarios:  resumo.Horarios,
				assentos:  resumo.Assentos,
				cancelada: resumo.Cancelada,
			}
			for _, trecho := range detalhe.Trechos {
				carona.livres = append(carona.livres, trecho.Livres)
				carona.passageiros = append(carona.passageiros, len(trecho.Passageiros))
			}
			observado[resumo.CaronaID] = carona
		}

		c.exigirOK(protocolo.TipoLogout, vazio, nil)
	}
	return observado
}

// verificarInvariantes confere as seis invariantes da seção 8.1 do PROJETO.md
// sobre o estado final do servidor.
//
// I1 é a invariante mestra e cobre sozinha dois requisitos não funcionais: o
// lado esquerdo nunca excede Assentos (nenhum assento vendido duas vezes,
// RNF05) e nunca fica abaixo (nenhum assento permanentemente bloqueado,
// RNF07). Um cancelamento em cascata que esquecesse de devolver o assento de
// uma carona vizinha apareceria aqui como soma menor que a capacidade.
func verificarInvariantes(t *testing.T, endereco string) {
	t.Helper()

	caronas := observarCaronas(t, endereco)

	for id, c := range caronas {
		verificarRotaDaCarona(t, id, c)

		for trecho := range c.livres {
			if soma := c.livres[trecho] + c.passageiros[trecho]; soma != c.assentos {
				t.Errorf("I1 violada em %s trecho %d: livres=%d + confirmados=%d = %d, want %d",
					id, trecho, c.livres[trecho], c.passageiros[trecho], soma, c.assentos)
			}
			if c.livres[trecho] < 0 || c.livres[trecho] > c.assentos {
				t.Errorf("I2 violada em %s trecho %d: livres=%d fora de [0,%d]",
					id, trecho, c.livres[trecho], c.assentos)
			}
			// I4 pelo lado do motorista: carona cancelada não pode ter
			// passageiro confirmado em trecho nenhum.
			if c.cancelada && c.passageiros[trecho] > 0 {
				t.Errorf("I4 violada: carona cancelada %s ainda tem %d passageiro(s) no trecho %d",
					id, c.passageiros[trecho], trecho)
			}
		}
	}

	// I4 pelo lado do passageiro: nenhuma reserva ativa pode referenciar uma
	// carona cancelada. As duas metades se complementam — a de cima pega o
	// assento que ficou pendurado na carona, esta pega a reserva que sobreviveu
	// ao cancelamento sem ninguém perceber.
	c := conectar(t, endereco)
	for _, p := range passageirosDaCarga() {
		c.entrar(p.usuario, p.senha)

		// Só as ativas: são as únicas de que I3, I4 e I5 falam, e um passageiro
		// com longo histórico de canceladas (o T4 produz centenas) não pode
		// empurrar as ativas para fora do limite da listagem.
		var reservas protocolo.ListarMinhasReservasResposta
		c.exigirOK(protocolo.TipoListarMinhasReservas,
			protocolo.ListarMinhasReservasRequisicao{IncluirCanceladas: false}, &reservas)
		if len(reservas.Reservas) >= dominio.MAXIMO_ITENS_LISTAGEM {
			t.Fatalf("%s tem %d reservas ativas listadas, o limite da listagem: o verificador não enxergaria as demais",
				p.usuario, len(reservas.Reservas))
		}

		var ativas []protocolo.ReservaResumo
		for _, r := range reservas.Reservas {
			if !r.Ativa {
				continue
			}
			ativas = append(ativas, r)
			for _, trecho := range r.Trechos {
				if caronas[trecho.CaronaID].cancelada {
					t.Errorf("I4 violada: reserva ativa %s de %s usa a carona cancelada %s",
						r.ReservaID, p.usuario, trecho.CaronaID)
				}
			}
			verificarCaminhoDaReserva(t, r, caronas)
		}

		// I5: intervalos fechados, como na D14 — encostar no mesmo instante já
		// é sobreposição.
		for i := range ativas {
			for j := i + 1; j < len(ativas); j++ {
				a, b := ativas[i], ativas[j]
				if !a.Chegada.Before(b.Partida) && !b.Chegada.Before(a.Partida) {
					t.Errorf("I5 violada: as reservas ativas %s e %s de %s se sobrepõem no tempo",
						a.ReservaID, b.ReservaID, p.usuario)
				}
			}
		}

		c.exigirOK(protocolo.TipoLogout, vazio, nil)
	}
}

// verificarRotaDaCarona confere I6: ao menos duas paradas, nenhuma cidade
// repetida e horários estritamente crescentes. Busca e reserva contam com isso
// para que a chegada de um trecho venha sempre depois da partida (D09).
func verificarRotaDaCarona(t *testing.T, id string, c caronaObservada) {
	t.Helper()

	if len(c.rota) < 2 || len(c.horarios) != len(c.rota) {
		t.Errorf("I6 violada em %s: %d parada(s) e %d horário(s)", id, len(c.rota), len(c.horarios))
		return
	}
	vistas := make(map[string]bool, len(c.rota))
	for i, cidade := range c.rota {
		if vistas[cidade] {
			t.Errorf("I6 violada em %s: %q aparece mais de uma vez na rota %v", id, cidade, c.rota)
		}
		vistas[cidade] = true
		if i > 0 && !c.horarios[i].After(c.horarios[i-1]) {
			t.Errorf("I6 violada em %s: horário de %q (%s) não é posterior ao de %q (%s)",
				id, cidade, c.horarios[i], c.rota[i-1], c.horarios[i-1])
		}
	}
}

// verificarCaminhoDaReserva confere I3 sobre uma reserva ativa: cada trecho é
// um segmento da rota da sua carona, com os horários dela; trechos
// consecutivos se encontram na mesma cidade; o tempo não volta; e nenhuma
// cidade se repete, contando as intermediárias por onde o passageiro passa
// dentro do veículo.
//
// A listagem não traz os índices de e ate, então eles são reconstruídos pela
// posição da origem e do destino na rota da carona. Isso só é possível porque
// a rota não repete cidade (I6).
func verificarCaminhoDaReserva(t *testing.T, r protocolo.ReservaResumo, caronas map[string]caronaObservada) {
	t.Helper()

	if len(r.Trechos) == 0 {
		t.Errorf("I3 violada: a reserva ativa %s não tem trechos", r.ReservaID)
		return
	}

	visitadas := map[string]bool{r.Trechos[0].Origem: true}
	for i, trecho := range r.Trechos {
		carona, conhecida := caronas[trecho.CaronaID]
		if !conhecida {
			t.Errorf("I3 violada: a reserva %s usa a carona desconhecida %s", r.ReservaID, trecho.CaronaID)
			return
		}
		de, ate := indiceNaRota(carona.rota, trecho.Origem), indiceNaRota(carona.rota, trecho.Destino)
		if de < 0 || ate <= de {
			t.Errorf("I3 violada: o trecho %d da reserva %s (%s → %s) não é segmento da rota %v de %s",
				i, r.ReservaID, trecho.Origem, trecho.Destino, carona.rota, trecho.CaronaID)
			return
		}
		if !trecho.Partida.Equal(carona.horarios[de]) || !trecho.Chegada.Equal(carona.horarios[ate]) {
			t.Errorf("I3 violada: o trecho %d da reserva %s tem horários diferentes dos da carona %s",
				i, r.ReservaID, trecho.CaronaID)
		}

		if i > 0 {
			anterior := r.Trechos[i-1]
			if anterior.Destino != trecho.Origem {
				t.Errorf("I3 violada: na reserva %s, o trecho %d desembarca em %q e o seguinte embarca em %q",
					r.ReservaID, i-1, anterior.Destino, trecho.Origem)
			}
			if trecho.Partida.Before(anterior.Chegada) {
				t.Errorf("I3 violada: na reserva %s, o trecho %d parte antes de o anterior chegar",
					r.ReservaID, i)
			}
		}

		for _, cidade := range carona.rota[de+1 : ate+1] {
			if visitadas[cidade] {
				t.Errorf("I3 violada: a reserva %s passa duas vezes por %q", r.ReservaID, cidade)
			}
			visitadas[cidade] = true
		}
	}
}

// indiceNaRota devolve a posição da cidade na rota, ou -1.
func indiceNaRota(rota []string, cidade string) int {
	for i, c := range rota {
		if c == cidade {
			return i
		}
	}
	return -1
}

// disputa autentica um passageiro por conexão, prende todos em uma barreira e
// libera de uma vez, devolvendo as respostas na ordem dos participantes.
//
// A barreira é o que dá sentido ao teste: as conexões e os LOGIN acontecem
// antes, então no instante da largada as N requisições de reserva estão a um
// write de distância do servidor e disputam de fato a mesma seção crítica.
func disputa(t *testing.T, endereco string, participantes []credencial, pedido func(indice int) protocolo.ReservarRequisicao) []protocolo.Resposta {
	t.Helper()

	clientes := make([]*cliente, len(participantes))
	for i, p := range participantes {
		clientes[i] = conectar(t, endereco)
		clientes[i].entrar(p.usuario, p.senha)
	}

	respostas := make([]protocolo.Resposta, len(participantes))
	largada := make(chan struct{})

	var espera sync.WaitGroup
	for i := range clientes {
		espera.Add(1)
		go func(n int) {
			defer espera.Done()
			<-largada
			respostas[n] = clientes[n].enviar(protocolo.TipoReservar, pedido(n))
		}(i)
	}

	close(largada)
	espera.Wait()
	return respostas
}

// contar tabula as respostas por status e código, que é a forma em que os
// cenários da seção 8.2 são enunciados ("1 OK, 49 SEM_ASSENTO").
func contar(respostas []protocolo.Resposta) (oks int, porCodigo map[string]int) {
	porCodigo = make(map[string]int)
	for _, r := range respostas {
		if r.Status == protocolo.StatusOK {
			oks++
			continue
		}
		porCodigo[r.Codigo]++
	}
	return oks, porCodigo
}

// trecho monta um item de RESERVAR (PROTOCOL.md, seção 5.9).
func trecho(caronaID string, de, ate int) protocolo.ItemReserva {
	return protocolo.ItemReserva{CaronaID: caronaID, De: de, Ate: ate}
}

// reserva monta o payload de RESERVAR a partir dos itens, na ordem dada.
func reserva(itens ...protocolo.ItemReserva) protocolo.ReservarRequisicao {
	return protocolo.ReservarRequisicao{Trechos: itens}
}

// publicarComo publica uma carona autenticado como o motorista informado e
// devolve o identificador gerado pelo servidor.
func publicarComo(t *testing.T, endereco string, m credencial, assentos int, precos []int, paradas ...map[string]any) string {
	t.Helper()

	c := conectar(t, endereco)
	c.entrar(m.usuario, m.senha)

	var publicada protocolo.PublicarCaronaResposta
	c.exigirOK(protocolo.TipoPublicarCarona, publicacao(assentos, precos, paradas...), &publicada)
	return publicada.CaronaID
}

// --- T1 — 50 clientes disputam o assento único de car-4 ---

// TestT1AssentoUnicoDisputadoPor50Clientes é o cenário T1 da seção 8.2.
//
// car-4 existe na carga de demonstração exatamente para isto: Feira de Santana
// → Jequié, um trecho, um assento (PROJETO.md, seção 9.2). Cinquenta conexões
// pedem o mesmo assento no mesmo instante; o resultado correto é uma única
// confirmação.
//
// O que o teste prova é RNF05: o mutex único (D04) serializa as cinquenta
// tentativas, e a verificação de disponibilidade acontece na mesma seção
// crítica da escrita (D07). Uma implementação que verificasse fora do lock, ou
// que verificasse e escrevesse em seções críticas diferentes, confirmaria dois
// passageiros e o teste apontaria mais de um OK.
func TestT1AssentoUnicoDisputadoPor50Clientes(t *testing.T) {
	endereco := subirServidor(t)

	participantes := passageirosDaCarga()[3:] // os 50 teste01..teste50
	respostas := disputa(t, endereco, participantes, func(int) protocolo.ReservarRequisicao {
		return reserva(trecho("car-4", 0, 1))
	})

	oks, porCodigo := contar(respostas)
	if oks != 1 {
		t.Errorf("confirmações = %d, want 1 — o assento único foi vendido %d vezes", oks, oks)
	}
	if porCodigo[protocolo.CodigoSemAssento] != len(participantes)-1 {
		t.Errorf("SEM_ASSENTO = %d, want %d (códigos observados: %v)",
			porCodigo[protocolo.CodigoSemAssento], len(participantes)-1, porCodigo)
	}

	caronas := observarCaronas(t, endereco)
	if livres := caronas["car-4"].livres[0]; livres != 0 {
		t.Errorf("car-4 trecho 0: livres = %d, want 0", livres)
	}
	verificarInvariantes(t, endereco)
}

// --- T2 — atomicidade do itinerário de duas caronas ---

// TestT2ItinerarioAtomicoNaoDeixaReservaParcial é o cenário T2 da seção 8.2, o
// teste central de RNF06.
//
// O itinerário tem duas pernas: a primeira com dez assentos, a segunda com um.
// Cinquenta clientes tentam reservar o itinerário inteiro ao mesmo tempo, e só
// um pode passar, porque a segunda perna é o gargalo.
//
// A afirmação decisiva é a última: a primeira perna precisa terminar com nove
// assentos livres, e não com menos. Qualquer valor abaixo de nove significa
// que uma confirmação recusada chegou a decrementar a perna disponível antes
// de descobrir que a outra estava esgotada — exatamente a reserva parcial que
// a separação entre validação e escrita (PROJETO.md, seção 7) existe para
// tornar impossível.
func TestT2ItinerarioAtomicoNaoDeixaReservaParcial(t *testing.T) {
	endereco := subirServidor(t)

	// Partida deslocada da carga de demonstração para não disputar assento com
	// as caronas do cenário fixo. A folga entre as pernas é de uma hora: o
	// veículo A chega a Feira de Santana duas horas após partir de Salvador, e
	// o B parte de lá três horas após a partida de A.
	partidaA := futuro(24)
	partidaB := partidaA.Add(3 * time.Hour)

	folgado := publicarComo(t, endereco, credencial{"joao", "1234"}, 10, []int{3000},
		parada("Salvador", partidaA), parada("Feira de Santana", partidaA.Add(2*time.Hour)))
	gargalo := publicarComo(t, endereco, credencial{"carlos", "1234"}, 1, []int{4500},
		parada("Feira de Santana", partidaB), parada("Jequié", partidaB.Add(3*time.Hour)))

	participantes := passageirosDaCarga()[3:]
	respostas := disputa(t, endereco, participantes, func(int) protocolo.ReservarRequisicao {
		return reserva(trecho(folgado, 0, 1), trecho(gargalo, 0, 1))
	})

	oks, porCodigo := contar(respostas)
	if oks != 1 {
		t.Errorf("confirmações = %d, want 1", oks)
	}
	if porCodigo[protocolo.CodigoSemAssento] != len(participantes)-1 {
		t.Errorf("SEM_ASSENTO = %d, want %d (códigos observados: %v)",
			porCodigo[protocolo.CodigoSemAssento], len(participantes)-1, porCodigo)
	}

	caronas := observarCaronas(t, endereco)
	if livres := caronas[folgado].livres[0]; livres != 9 {
		t.Errorf("perna folgada: livres = %d, want 9 — %d confirmação(ões) recusada(s) escreveu(ram) antes de validar tudo",
			livres, 10-livres-1)
	}
	if livres := caronas[gargalo].livres[0]; livres != 0 {
		t.Errorf("perna gargalo: livres = %d, want 0", livres)
	}
	verificarInvariantes(t, endereco)
}

// --- T3 — reservas simultâneas em trechos disjuntos da mesma carona ---

// TestT3ReservasEmTrechosDisjuntosDaMesmaCarona é o cenário T3 da seção 8.2, e
// o teste de RF11: a disponibilidade é controlada por trecho, e não pela
// carona inteira.
//
// A carona tem três trechos e quinze assentos. Antes da largada, o primeiro
// trecho é esgotado com quinze reservas feitas em sequência; depois, trinta
// passageiros disputam ao mesmo tempo os outros dois trechos, quinze em cada.
//
// Com controle por trecho, as trinta cabem, mas só cabem se cada confirmação
// conferir e decrementar o seu próprio trecho e nenhum outro. O trecho
// esgotado antes da largada é o que torna a detecção independente da ordem de
// chegada: uma implementação que controlasse a carona inteira enxergaria a
// carona lotada e recusaria todas as trinta, e não só as que chegassem por
// último.
func TestT3ReservasEmTrechosDisjuntosDaMesmaCarona(t *testing.T) {
	endereco := subirServidor(t)

	const porTrecho = 15

	partida := futuro(24)
	carona := publicarComo(t, endereco, credencial{"joao", "1234"}, porTrecho, []int{3000, 4500, 4000},
		parada("Salvador", partida),
		parada("Feira de Santana", partida.Add(2*time.Hour)),
		parada("Jequié", partida.Add(5*time.Hour)),
		parada("Vitória da Conquista", partida.Add(7*time.Hour)))

	passageiros := passageirosDaCarga()[3 : 3+3*porTrecho]

	for _, p := range passageiros[:porTrecho] {
		c := conectar(t, endereco)
		c.entrar(p.usuario, p.senha)
		c.exigirOK(protocolo.TipoReservar, reserva(trecho(carona, 0, 1)), nil)
	}

	participantes := passageiros[porTrecho:]
	respostas := disputa(t, endereco, participantes, func(n int) protocolo.ReservarRequisicao {
		indice := 1 + n%2
		return reserva(trecho(carona, indice, indice+1))
	})

	oks, porCodigo := contar(respostas)
	if oks != len(participantes) {
		t.Errorf("confirmações = %d, want %d — trechos disjuntos não deveriam disputar assento (códigos: %v)",
			oks, len(participantes), porCodigo)
	}

	observada := observarCaronas(t, endereco)[carona]
	for indice, livres := range observada.livres {
		if livres != 0 {
			t.Errorf("trecho %d: livres = %d, want 0", indice, livres)
		}
		if confirmados := observada.passageiros[indice]; confirmados != porTrecho {
			t.Errorf("trecho %d: %d passageiros confirmados, want %d", indice, confirmados, porTrecho)
		}
	}
	verificarInvariantes(t, endereco)
}

// --- T4 — reservar e cancelar em laço ---

// duracaoT4 é a duração do laço do T4.
//
// A seção 8.2 fala em 30 s, mas a suíte roda com -count=20, e vinte vezes 30 s
// passaria do timeout padrão de 10 min do go test. Por isso o padrão é curto, e
// o cenário completo é pedido explicitamente com VAIJUNTO_T4_DURACAO=30s.
func duracaoT4(t *testing.T) time.Duration {
	t.Helper()

	valor := os.Getenv("VAIJUNTO_T4_DURACAO")
	if valor == "" {
		return 2 * time.Second
	}
	duracao, err := time.ParseDuration(valor)
	if err != nil || duracao <= 0 {
		t.Fatalf("VAIJUNTO_T4_DURACAO = %q: informe uma duração positiva, como 30s", valor)
	}
	return duracao
}

// TestT4ReservarECancelarEmLacoDevolveTudo é o cenário T4 da seção 8.2, o teste
// de RNF07: nenhum assento fica permanentemente bloqueado.
//
// Dez passageiros disputam três assentos de um itinerário de duas caronas e,
// a cada confirmação, cancelam em seguida. Terminado o laço, sem nenhuma
// reserva ativa, as duas caronas precisam ter voltado à capacidade cheia. Um
// assento que o cancelamento esquecesse de devolver — em especial o da segunda
// carona — apareceria como Livres abaixo de três, e um devolvido duas vezes,
// como Livres acima.
//
// Haver mais passageiros que assentos é o que dá valor ao teste: as recusas
// por SEM_ASSENTO se intercalam com os cancelamentos, e é nessa intercalação
// que um contador mal sincronizado se perderia.
func TestT4ReservarECancelarEmLacoDevolveTudo(t *testing.T) {
	endereco := subirServidor(t)
	duracao := duracaoT4(t)

	const clientes, assentos = 10, 3

	// Partida com um dia de folga: o prazo do passageiro vence 1 h antes dela
	// (D13), e não pode vencer no meio do laço.
	partidaA := futuro(24)
	partidaB := partidaA.Add(3 * time.Hour)
	primeira := publicarComo(t, endereco, credencial{"joao", "1234"}, assentos, []int{3000},
		parada("Salvador", partidaA), parada("Feira de Santana", partidaA.Add(2*time.Hour)))
	segunda := publicarComo(t, endereco, credencial{"carlos", "1234"}, assentos, []int{4500},
		parada("Feira de Santana", partidaB), parada("Jequié", partidaB.Add(3*time.Hour)))
	pedido := reserva(trecho(primeira, 0, 1), trecho(segunda, 0, 1))

	conexoes := make([]*cliente, clientes)
	for i, p := range passageirosDaCarga()[3 : 3+clientes] {
		conexoes[i] = conectar(t, endereco)
		conexoes[i].entrar(p.usuario, p.senha)
	}

	var ciclos, recusas atomic.Int64
	largada := make(chan struct{})
	var espera sync.WaitGroup

	fim := time.Now().Add(duracao)
	for _, c := range conexoes {
		espera.Add(1)
		go func(c *cliente) {
			defer espera.Done()
			<-largada
			for time.Now().Before(fim) {
				resp := c.enviar(protocolo.TipoReservar, pedido)
				if resp.Status != protocolo.StatusOK {
					if resp.Codigo != protocolo.CodigoSemAssento {
						t.Errorf("RESERVAR: codigo = %q, want OK ou SEM_ASSENTO (mensagem: %q)", resp.Codigo, resp.Mensagem)
						return
					}
					recusas.Add(1)
					continue
				}

				var confirmada protocolo.ReservarResposta
				if err := json.Unmarshal(resp.Dados, &confirmada); err != nil {
					t.Errorf("decodificar confirmação %s: %v", resp.Dados, err)
					return
				}
				cancelada := c.enviar(protocolo.TipoCancelarReserva,
					protocolo.CancelarReservaRequisicao{ReservaID: confirmada.ReservaID})
				if cancelada.Status != protocolo.StatusOK {
					t.Errorf("CANCELAR_RESERVA %s: codigo = %q (mensagem: %q)",
						confirmada.ReservaID, cancelada.Codigo, cancelada.Mensagem)
					return
				}
				ciclos.Add(1)
			}
		}(c)
	}
	close(largada)
	espera.Wait()

	t.Logf("%d ciclos de reservar e cancelar e %d SEM_ASSENTO em %s", ciclos.Load(), recusas.Load(), duracao)
	if ciclos.Load() == 0 {
		t.Fatalf("nenhum ciclo completo: o laço não exercitou nada")
	}

	caronas := observarCaronas(t, endereco)
	for _, id := range []string{primeira, segunda} {
		if livres := caronas[id].livres[0]; livres != assentos {
			t.Errorf("%s: livres = %d, want %d — o laço deixou %d assento(s) bloqueado(s)",
				id, livres, assentos, assentos-livres)
		}
	}
	verificarInvariantes(t, endereco)
}

// --- T5 — cancelamento de carona em cascata sob concorrência ---

// TestT5CancelamentoEmCascataSobConcorrencia é o cenário T5 da seção 8.2.
//
// Enquanto vinte passageiros reservam um itinerário de duas caronas, o
// motorista cancela a primeira. Cada reserva confirmada consumiu um assento
// **nas duas** caronas, então a cascata precisa devolver os assentos de todos
// os trechos da reserva, inclusive os da segunda carona, que não foi cancelada
// (PROTOCOL.md, seção 5.7). Devolver só os da carona cancelada deixaria
// assentos órfãos na vizinha: reservados por ninguém e invendáveis para
// sempre.
//
// O teste tem duas fases de propósito. Um punhado de passageiros reserva
// **antes** da largada, em sequência, e só depois o restante disputa com o
// cancelamento. A primeira fase é o que garante que exista cascata para
// verificar: se todas as reservas corressem contra o cancelamento, uma execução
// em que o motorista vencesse a corrida inteira acabaria com zero confirmações,
// e o teste passaria sem ter exercitado a cascata uma única vez — verde por
// vacuidade, que é o pior tipo de teste de concorrência.
//
// Três afirmações, todas consequência do mutex único serializar cancelamento e
// reservas:
//
//   - reservas_canceladas é igual ao número de confirmações, porque toda
//     confirmação usa a carona cancelada e, se foi confirmada, foi antes do
//     cancelamento — quem chegou depois recebeu CARONA_CANCELADA;
//   - as duas caronas voltam à capacidade cheia, o que é I1 no caso extremo;
//   - nenhuma reserva ativa sobra apontando para a carona cancelada (I4).
//
// O prazo de uma hora do passageiro não entra aqui: ele não se aplica à
// cascata (D13). Se aplicasse, cancelar uma carona a menos de uma hora da
// partida travaria as reservas dos passageiros de um veículo que não vai sair.
func TestT5CancelamentoEmCascataSobConcorrencia(t *testing.T) {
	endereco := subirServidor(t)

	const assentos, antes, durante = 20, 8, 12

	partidaA := futuro(24)
	partidaB := partidaA.Add(3 * time.Hour)

	dono := credencial{"joao", "1234"}
	primeira := publicarComo(t, endereco, dono, assentos, []int{3000},
		parada("Salvador", partidaA), parada("Feira de Santana", partidaA.Add(2*time.Hour)))
	segunda := publicarComo(t, endereco, credencial{"carlos", "1234"}, assentos, []int{4500},
		parada("Feira de Santana", partidaB), parada("Jequié", partidaB.Add(3*time.Hour)))

	passageiros := passageirosDaCarga()[3 : 3+antes+durante]

	// Fase 1 — reservas garantidas, antes de qualquer cancelamento.
	for _, p := range passageiros[:antes] {
		c := conectar(t, endereco)
		c.entrar(p.usuario, p.senha)
		c.exigirOK(protocolo.TipoReservar, reserva(trecho(primeira, 0, 1), trecho(segunda, 0, 1)), nil)
	}
	inicial := observarCaronas(t, endereco)
	if livres := inicial[segunda].livres[0]; livres != assentos-antes {
		t.Fatalf("preparação: carona vizinha com livres = %d, want %d", livres, assentos-antes)
	}

	// Fase 2 — o restante disputa com o cancelamento. A conexão do motorista é
	// aberta e autenticada antes da largada, pelo mesmo motivo da barreira: o
	// cancelamento tem que competir com as reservas, e não chegar depois delas.
	motorista := conectar(t, endereco)
	motorista.entrar(dono.usuario, dono.senha)

	clientes := make([]*cliente, durante)
	for i, p := range passageiros[antes:] {
		clientes[i] = conectar(t, endereco)
		clientes[i].entrar(p.usuario, p.senha)
	}

	respostas := make([]protocolo.Resposta, durante)
	var cancelamento protocolo.CancelarCaronaResposta

	largada := make(chan struct{})
	var espera sync.WaitGroup

	for i := range clientes {
		espera.Add(1)
		go func(n int) {
			defer espera.Done()
			<-largada
			respostas[n] = clientes[n].enviar(protocolo.TipoReservar,
				reserva(trecho(primeira, 0, 1), trecho(segunda, 0, 1)))
		}(i)
	}
	espera.Add(1)
	go func() {
		defer espera.Done()
		<-largada
		motorista.exigirOK(protocolo.TipoCancelarCarona,
			protocolo.CancelarCaronaRequisicao{CaronaID: primeira}, &cancelamento)
	}()

	close(largada)
	espera.Wait()

	oks, porCodigo := contar(respostas)
	if oks+porCodigo[protocolo.CodigoCaronaCancelada] != durante {
		t.Errorf("respostas inesperadas: %d OK, %v — só OK e CARONA_CANCELADA são possíveis aqui", oks, porCodigo)
	}
	if cancelamento.ReservasCanceladas != antes+oks {
		t.Errorf("reservas_canceladas = %d, want %d (toda confirmação usava a carona cancelada)",
			cancelamento.ReservasCanceladas, antes+oks)
	}

	caronas := observarCaronas(t, endereco)
	if !caronas[primeira].cancelada {
		t.Errorf("a carona %s deveria estar marcada como cancelada", primeira)
	}
	if livres := caronas[primeira].livres[0]; livres != assentos {
		t.Errorf("carona cancelada: livres = %d, want %d", livres, assentos)
	}
	if livres := caronas[segunda].livres[0]; livres != assentos {
		t.Errorf("carona vizinha: livres = %d, want %d — a cascata deixou %d assento(s) órfão(s)",
			livres, assentos, assentos-livres)
	}
	verificarInvariantes(t, endereco)
}

// --- T6 — conexão derrubada no meio de uma linha ---

// TestT6ConexaoDerrubadaNoMeioDeUmaLinha é o cenário T6 da seção 8.2.
//
// Dez passageiros reservam de verdade enquanto outros dez, já autenticados,
// começam a enviar o mesmo RESERVAR e caem antes do '\n'. Metade dos que caem
// manda o JSON inteiro sem o terminador, e metade corta a linha no meio; parte
// fecha a conexão normalmente, e parte com RST. O JSON inteiro sem '\n' é o
// caso que importa: ele é sintaticamente válido, e processá-lo seria executar
// uma reserva que o cliente não terminou de pedir (PROTOCOL.md, seção 1).
//
// A carona tem assento para todos, dos que ficam e dos que caem. Assim a
// detecção não depende de quem chega primeiro: um fragmento executado
// indevidamente viraria uma reserva de quem caiu, e não uma recusa
// silenciosa por falta de lugar.
func TestT6ConexaoDerrubadaNoMeioDeUmaLinha(t *testing.T) {
	endereco := subirServidor(t)

	const saudaveis, derrubados = 10, 10

	partida := futuro(24)
	carona := publicarComo(t, endereco, credencial{"joao", "1234"}, saudaveis+derrubados, []int{3000},
		parada("Salvador", partida), parada("Feira de Santana", partida.Add(2*time.Hour)))
	pedido := reserva(trecho(carona, 0, 1))

	dados, err := json.Marshal(pedido)
	if err != nil {
		t.Fatalf("montar dados: %v", err)
	}
	linhaSemTerminador, err := json.Marshal(protocolo.Requisicao{ID: "fragmento", Tipo: protocolo.TipoReservar, Dados: dados})
	if err != nil {
		t.Fatalf("montar envelope: %v", err)
	}

	passageiros := passageirosDaCarga()[3 : 3+saudaveis+derrubados]
	conexoes := make([]*cliente, len(passageiros))
	for i, p := range passageiros {
		conexoes[i] = conectar(t, endereco)
		conexoes[i].entrar(p.usuario, p.senha)
	}

	respostas := make([]protocolo.Resposta, saudaveis)
	largada := make(chan struct{})
	var espera sync.WaitGroup

	for i, c := range conexoes {
		espera.Add(1)
		go func(n int, c *cliente) {
			defer espera.Done()
			<-largada

			if n < saudaveis {
				respostas[n] = c.enviar(protocolo.TipoReservar, pedido)
				return
			}

			fragmento := linhaSemTerminador
			if n%2 == 0 {
				fragmento = linhaSemTerminador[:len(linhaSemTerminador)/2]
			}
			// Linger zero faz o Close mandar RST em vez do fechamento
			// ordenado: é a queda abrupta, e não a despedida educada.
			if n%4 < 2 {
				if err := c.conn.(*net.TCPConn).SetLinger(0); err != nil {
					t.Errorf("linger: %v", err)
				}
			}
			if _, err := c.conn.Write(fragmento); err != nil {
				t.Errorf("escrever fragmento: %v", err)
			}
			_ = c.conn.Close()
		}(i, c)
	}
	close(largada)
	espera.Wait()

	oks, porCodigo := contar(respostas)
	if oks != saudaveis {
		t.Errorf("confirmações dos saudáveis = %d, want %d (códigos: %v)", oks, saudaveis, porCodigo)
	}

	// O servidor continua atendendo, e nenhum dos que caíram ficou com reserva.
	conferente := conectar(t, endereco)
	conferente.exigirOK(protocolo.TipoPing, vazio, nil)
	for _, p := range passageiros[saudaveis:] {
		conferente.entrar(p.usuario, p.senha)
		var reservas protocolo.ListarMinhasReservasResposta
		conferente.exigirOK(protocolo.TipoListarMinhasReservas,
			protocolo.ListarMinhasReservasRequisicao{IncluirCanceladas: true}, &reservas)
		if len(reservas.Reservas) != 0 {
			t.Errorf("%s caiu no meio da linha e ainda assim tem %d reserva(s)", p.usuario, len(reservas.Reservas))
		}
		conferente.exigirOK(protocolo.TipoLogout, vazio, nil)
	}

	if livres := observarCaronas(t, endereco)[carona].livres[0]; livres != derrubados {
		t.Errorf("livres = %d, want %d — só os saudáveis podiam ter reservado", livres, derrubados)
	}
	verificarInvariantes(t, endereco)
}

// --- T7 — rajada de mensagens malformadas ---

// respostaEsperada é o id e o código que uma linha da rajada do T7 precisa
// receber de volta.
type respostaEsperada struct{ id, codigo string }

// rajadaMalformada monta, para uma conexão, as linhas inválidas do T7 e a
// resposta que cada uma precisa receber, na ordem.
//
// Cobre cada recusa que a seção 6 do PROTOCOL.md distingue antes de qualquer
// regra de negócio: linha que não é JSON, JSON cortado, JSON que não é
// envelope, envelope sem tipo, dados nulo, id que não é string, tipo
// inexistente, tipo em minúsculas e operação sem autenticação. A linha vazia
// não tem resposta nenhuma (seção 1).
func rajadaMalformada(conexao, rodadas int) ([]byte, []respostaEsperada) {
	var rajada []byte
	var esperadas []respostaEsperada

	linha := func(texto string, resposta respostaEsperada) {
		rajada = append(rajada, texto...)
		rajada = append(rajada, '\n')
		esperadas = append(esperadas, resposta)
	}

	for r := 0; r < rodadas; r++ {
		id := fmt.Sprintf("c%d-r%d", conexao, r)
		linha(`isso não é json`, respostaEsperada{"", protocolo.CodigoJSONInvalido})
		linha(`{"id":"`+id+`","tipo":"PING"`, respostaEsperada{"", protocolo.CodigoJSONInvalido})
		linha(`[1,2,3]`, respostaEsperada{"", protocolo.CodigoEnvelopeInvalido})
		linha(`{"id":7,"tipo":"PING","dados":{}}`, respostaEsperada{"", protocolo.CodigoEnvelopeInvalido})
		linha(`{"id":"`+id+`","dados":{}}`, respostaEsperada{id, protocolo.CodigoEnvelopeInvalido})
		linha(`{"id":"`+id+`","tipo":"PING","dados":null}`, respostaEsperada{id, protocolo.CodigoEnvelopeInvalido})
		linha(`{"id":"`+id+`","tipo":"INVENTADO","dados":{}}`, respostaEsperada{id, protocolo.CodigoTipoDesconhecido})
		linha(`{"id":"`+id+`","tipo":"ping","dados":{}}`, respostaEsperada{id, protocolo.CodigoTipoDesconhecido})
		linha(`{"id":"`+id+`","tipo":"RESERVAR","dados":{"trechos":[{"carona_id":"car-4","de":0,"ate":1}]}}`,
			respostaEsperada{id, protocolo.CodigoNaoAutenticado})
		rajada = append(rajada, '\n')
	}
	return rajada, esperadas
}

// TestT7RajadaDeMensagensMalformadas é o cenário T7 da seção 8.2.
//
// Dez conexões despejam ao mesmo tempo, cada uma num único write, centenas de
// linhas inválidas. Cada linha precisa receber o erro com o código certo e,
// quando legível, o id ecoado, na ordem, porque o modelo é estritamente
// requisição/resposta. Terminada a rajada, a mesma conexão ainda responde PING:
// "permanece disponível" vale para quem mandou o lixo, e não só para os outros.
//
// Uma das linhas é um RESERVAR sem autenticação sobre o assento único de car-4.
// No fim, o assento continua livre: nenhuma linha recusada alterou o estado.
func TestT7RajadaDeMensagensMalformadas(t *testing.T) {
	endereco := subirServidor(t)

	const conexoes, rodadas = 10, 20

	largada := make(chan struct{})
	var espera sync.WaitGroup

	for n := 0; n < conexoes; n++ {
		c := conectar(t, endereco)
		rajada, esperadas := rajadaMalformada(n, rodadas)

		espera.Add(1)
		go func() {
			defer espera.Done()
			<-largada

			if err := c.conn.SetDeadline(time.Now().Add(prazoLeitura)); err != nil {
				t.Errorf("conexão %d: prazo: %v", n, err)
				return
			}
			if _, err := c.conn.Write(rajada); err != nil {
				t.Errorf("conexão %d: escrever rajada: %v", n, err)
				return
			}

			for i, quero := range esperadas {
				bruta, err := c.leitor.LerLinha()
				if err != nil {
					t.Errorf("conexão %d, resposta %d: %v", n, i, err)
					return
				}
				resp, err := protocolo.DecodificarResposta(bruta)
				if err != nil {
					t.Errorf("conexão %d, resposta %d: decodificar %q: %v", n, i, bruta, err)
					return
				}
				if resp.Status != protocolo.StatusErro || resp.Codigo != quero.codigo || resp.ID != quero.id {
					t.Errorf("conexão %d, resposta %d: got id=%q %s/%s, want id=%q ERRO/%s",
						n, i, resp.ID, resp.Status, resp.Codigo, quero.id, quero.codigo)
					return
				}
			}

			if resp := c.enviar(protocolo.TipoPing, vazio); resp.Status != protocolo.StatusOK {
				t.Errorf("conexão %d: PING depois da rajada = %s/%s", n, resp.Status, resp.Codigo)
			}
		}()
	}
	close(largada)
	espera.Wait()

	if livres := observarCaronas(t, endereco)["car-4"].livres[0]; livres != 1 {
		t.Errorf("car-4: livres = %d, want 1 — uma linha recusada alterou o estado", livres)
	}
	verificarInvariantes(t, endereco)
}

// --- T8 — reservas sobrepostas do mesmo passageiro ---

// TestT8ReservasSobrepostasDoMesmoPassageiro é o cenário T8 da seção 8.2, que
// existe por causa de D14: duas reservas ativas do mesmo passageiro não podem
// se sobrepor no tempo, porque ninguém viaja em dois veículos ao mesmo tempo.
//
// Cada passageiro abre duas conexões e dispara, no mesmo instante, duas
// reservas de itinerários que partem à mesma hora em veículos diferentes.
// Exatamente uma pode passar; a outra tem que receber CONFLITO_HORARIO — e não
// SEM_ASSENTO, que seria a resposta errada pelo motivo certo.
//
// Os dois veículos são publicados com folga de assentos justamente para isso:
// com sessenta lugares para vinte e cinco passageiros, o único motivo possível
// de recusa é a sobreposição. Vinte e cinco duelos simultâneos por execução,
// multiplicados pelo -count, é o que dá ao teste chance real de pegar a janela
// entre verificar e escrever, caso ela exista.
func TestT8ReservasSobrepostasDoMesmoPassageiro(t *testing.T) {
	endereco := subirServidor(t)

	const duelos = 25

	// Mesma partida nas duas caronas: os intervalos coincidem, então a
	// sobreposição é total e independe de qual das duas vence.
	partida := futuro(24)
	chegada := partida.Add(2 * time.Hour)
	umaDelas := publicarComo(t, endereco, credencial{"joao", "1234"}, 60, []int{3000},
		parada("Salvador", partida), parada("Feira de Santana", chegada))
	aOutra := publicarComo(t, endereco, credencial{"carlos", "1234"}, 60, []int{3200},
		parada("Salvador", partida), parada("Feira de Santana", chegada))

	// Duas conexões por passageiro. A identidade é da conexão (D08), mas o
	// estado que decide o conflito é o compartilhado, e é ele que precisa
	// serializar as duas tentativas.
	participantes := passageirosDaCarga()[3 : 3+duelos]
	dobrados := make([]credencial, 0, 2*duelos)
	for _, p := range participantes {
		dobrados = append(dobrados, p, p)
	}

	respostas := disputa(t, endereco, dobrados, func(n int) protocolo.ReservarRequisicao {
		if n%2 == 0 {
			return reserva(trecho(umaDelas, 0, 1))
		}
		return reserva(trecho(aOutra, 0, 1))
	})

	for i, p := range participantes {
		primeira, segunda := respostas[2*i], respostas[2*i+1]

		oks, porCodigo := contar([]protocolo.Resposta{primeira, segunda})
		if oks != 1 {
			t.Errorf("%s: %d confirmações, want 1 (códigos: %v)", p.usuario, oks, porCodigo)
		}
		if porCodigo[protocolo.CodigoConflitoHorario] != 1 {
			t.Errorf("%s: CONFLITO_HORARIO = %d, want 1 (códigos: %v)",
				p.usuario, porCodigo[protocolo.CodigoConflitoHorario], porCodigo)
		}
	}

	// A confirmação final vem pela listagem do próprio passageiro: uma reserva
	// ativa, nunca duas.
	conferente := conectar(t, endereco)
	for _, p := range participantes {
		conferente.entrar(p.usuario, p.senha)

		var reservas protocolo.ListarMinhasReservasResposta
		conferente.exigirOK(protocolo.TipoListarMinhasReservas,
			protocolo.ListarMinhasReservasRequisicao{}, &reservas)
		if len(reservas.Reservas) != 1 {
			t.Errorf("%s tem %d reservas ativas, want 1", p.usuario, len(reservas.Reservas))
		}

		conferente.exigirOK(protocolo.TipoLogout, vazio, nil)
	}
	verificarInvariantes(t, endereco)
}
