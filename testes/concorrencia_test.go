package testes

// Cenários de concorrência da seção 8.2 do PROJETO.md, exercitados pelo
// protocolo real por socket, exatamente como um cliente faria.
//
// Os quatro testes deste arquivo atacam propriedades diferentes:
//
//	T1 — nunca vender o mesmo assento duas vezes (RNF05);
//	T2 — a confirmação de itinerário é tudo-ou-nada (RNF06);
//	T5 — o cancelamento em cascata não deixa assento órfão (I1 e I4);
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
	"fmt"
	"sync"
	"testing"
	"time"

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

		for _, resumo := range lista.Caronas {
			var detalhe protocolo.DetalharCaronaResposta
			c.exigirOK(protocolo.TipoDetalharCarona,
				protocolo.DetalharCaronaRequisicao{CaronaID: resumo.CaronaID}, &detalhe)

			carona := caronaObservada{assentos: resumo.Assentos, cancelada: resumo.Cancelada}
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

// verificarInvariantes confere I1, I2 e I4 da seção 8.1 do PROJETO.md sobre o
// estado final do servidor.
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

		var reservas protocolo.ListarMinhasReservasResposta
		c.exigirOK(protocolo.TipoListarMinhasReservas,
			protocolo.ListarMinhasReservasRequisicao{IncluirCanceladas: true}, &reservas)

		for _, r := range reservas.Reservas {
			if !r.Ativa {
				continue
			}
			for _, trecho := range r.Trechos {
				if caronas[trecho.CaronaID].cancelada {
					t.Errorf("I4 violada: reserva ativa %s de %s usa a carona cancelada %s",
						r.ReservaID, p.usuario, trecho.CaronaID)
				}
			}
		}

		c.exigirOK(protocolo.TipoLogout, vazio, nil)
	}
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

// --- T1 — 50 clientes disputam o assento único de car-7 ---

// TestT1AssentoUnicoDisputadoPor50Clientes é o cenário T1 da seção 8.2.
//
// car-7 existe na carga de demonstração exatamente para isto: Feira de Santana
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
		return reserva(trecho("car-7", 0, 1))
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
	if livres := caronas["car-7"].livres[0]; livres != 0 {
		t.Errorf("car-7 trecho 0: livres = %d, want 0", livres)
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
