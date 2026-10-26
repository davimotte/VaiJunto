package testes

import (
	"fmt"
	"sync"
	"testing"

	"vaijunto/internal/protocolo"
)

// Cenários do coordenador do 2PC (PROJETO_P2.md, seção 11.3: TR1, TR2 e TR5).
//
// Todos passam pela porta TCP, como um passageiro de verdade: o que se afirma
// aqui é o caminho inteiro — cliente, RESERVAR, coordenador, PREPARAR pela
// rede, votos, decisão com fsync, fase 2 — e não uma peça dele.
//
// A diferença destes para os cenários T1 e T2 do P1 é de onde vêm os assentos:
// lá, de um mutex só; aqui, de três empresas que só se falam por REST. A
// asserção, porém, é a mesma, e é o que torna a comparação possível no
// relatório.

// caronasEncadeadas monta uma carona por empresa, encadeadas no espaço e no
// tempo, para um itinerário A+B+C com uma perna em cada uma.
//
// As folgas são de 1 h entre as pernas: acima de MARGEM_BALDEACAO (30 min) e
// bem abaixo de ESPERA_MAXIMA_BALDEACAO (12 h), de modo que o itinerário passe
// na validação do coordenador (D34) e o teste meça o que quer medir — a
// disputa — e não a regra de baldeação.
//
// O caronasDe de transacoes_test.go não serve porque gera uma carona só, de um
// trecho: a atomicidade entre empresas precisa de pernas que encadeiem.
func caronasEncadeadas(assentosPorEmpresa map[string]int) map[string][]byte {
	// Rota em cadeia: cada empresa cobre um salto, começando onde a anterior
	// terminou.
	saltos := []struct {
		empresa       string
		origem        string
		destino       string
		partida       string
		chegada       string
		precoCentavos int
	}{
		{"vaijunto", "Salvador", "Feira de Santana", "2026-11-05T06:00:00-03:00", "2026-11-05T08:00:00-03:00", 2000},
		{"rotacapixaba", "Feira de Santana", "Jequié", "2026-11-05T09:00:00-03:00", "2026-11-05T11:00:00-03:00", 3000},
		{"rotafluminense", "Jequié", "Vitória da Conquista", "2026-11-05T12:00:00-03:00", "2026-11-05T14:00:00-03:00", 4000},
	}

	caronas := make(map[string][]byte, len(saltos))
	for _, s := range saltos {
		assentos, pedida := assentosPorEmpresa[s.empresa]
		if !pedida {
			continue
		}
		caronas[s.empresa] = fmt.Appendf(nil, `[{"id":%q,"motorista":"joao",
			"rota":[%q,%q],"horarios":[%q,%q],
			"assentos":%d,"precos_centavos":[%d]}]`,
			s.empresa+":car-1", s.origem, s.destino, s.partida, s.chegada, assentos, s.precoCentavos)
	}
	return caronas
}

// reservarEmParalelo dispara um RESERVAR por participante, todos no mesmo
// instante, e devolve as respostas na ordem dos participantes.
//
// A barreira é a mesma de disputa (concorrencia_test.go): conectar e autenticar
// acontecem **antes** da largada, para que no instante em que o canal fecha
// cada requisição esteja a um write de distância. Sem isso, o custo do LOGIN
// escalonaria as tentativas e a disputa deixaria de ser disputa.
//
// A dupla WaitGroup garante que todas as goroutines já foram escalonadas antes
// da largada, que é o que dá ao teste chance real de pegar a janela entre
// validar e escrever, se ela existir.
func reservarEmParalelo(t *testing.T, pedidos []pedidoDeReserva) []protocolo.Resposta {
	t.Helper()

	clientes := make([]*cliente, len(pedidos))
	for i, p := range pedidos {
		clientes[i] = conectar(t, p.endereco)
		clientes[i].entrar(p.usuario, p.senha)
	}

	respostas := make([]protocolo.Resposta, len(pedidos))
	largada := make(chan struct{})
	var prontas, terminadas sync.WaitGroup
	prontas.Add(len(pedidos))
	terminadas.Add(len(pedidos))

	for i := range pedidos {
		go func(n int) {
			defer terminadas.Done()
			prontas.Done()
			<-largada
			respostas[n] = clientes[n].enviar(protocolo.TipoReservar, pedidos[n].pedido)
		}(i)
	}

	prontas.Wait()
	close(largada)
	terminadas.Wait()
	return respostas
}

// pedidoDeReserva é um RESERVAR a disparar: quem, em qual empresa, e o quê.
type pedidoDeReserva struct {
	endereco string
	usuario  string
	senha    string
	pedido   protocolo.ReservarRequisicao
}

// TestTR1UmAssentoCinquentaPassageirosDasTresEmpresas é o TR1 da seção 11.3: 1
// carona, 1 assento, 50 passageiros das 3 empresas reservando ao mesmo tempo.
//
// Exatamente um OK. É o RD02 — nenhum assento vendido duas vezes — observado no
// caso mais hostil que o enunciado descreve, e a garantia não vem da rede nem
// de relógio: vem de a carona ter um **único** dono, cujo mutex serializa os
// cinquenta PREPARAR (seção 7.5). Os coordenadores são três empresas
// diferentes, e isso não muda nada.
//
// É o T1 do P1 atravessando empresas: lá os cinquenta disputavam no mesmo
// processo, aqui chegam de três servidores que só se falam por REST.
func TestTR1UmAssentoCinquentaPassageirosDasTresEmpresas(t *testing.T) {
	consorcio := subirConsorcioP2(t, opcoesConsorcio{
		caronas: map[string][]byte{
			// Só a vaijunto tem carona, e com um assento: é por ele que os
			// cinquenta disputam.
			"vaijunto":       caronasDe("vaijunto", 1),
			"rotacapixaba":   {'[', ']'},
			"rotafluminense": {'[', ']'},
		},
		tcp:       true,
		auditoria: true,
	})

	const passageiros = 50
	empresas := []string{"vaijunto", "rotacapixaba", "rotafluminense"}
	carga := passageirosDaCarga()

	pedidos := make([]pedidoDeReserva, 0, passageiros)
	for i := 0; i < passageiros; i++ {
		// Os passageiros se distribuem pelas três empresas, então as reservas
		// são coordenadas por três servidores diferentes — é o que distingue
		// este cenário do T1.
		nome := empresas[i%len(empresas)]
		pedidos = append(pedidos, pedidoDeReserva{
			endereco: consorcio[nome].endereco,
			usuario:  carga[i].usuario,
			senha:    carga[i].senha,
			pedido:   reserva(trecho("vaijunto:car-1", 0, 1)),
		})
	}

	respostas := reservarEmParalelo(t, pedidos)
	oks, porCodigo := contar(respostas)
	if oks != 1 {
		t.Errorf("confirmações = %d, want exatamente 1 (códigos: %v)", oks, porCodigo)
	}
	if recusas := recusasPorFaltaDeAssento(porCodigo); recusas != passageiros-1 {
		t.Errorf("recusas por falta de assento = %d, want %d (códigos: %v)",
			recusas, passageiros-1, porCodigo)
	}

	esperarQuiescencia(t, consorcio)
	todas := auditoriasDe(t, consorcio)
	verificarInvariantesP2(t, todas)

	// O assento foi vendido uma vez, e está confirmado — não em espera.
	carona := todas["vaijunto"].Caronas[0]
	if carona.Livres[0] != 0 || carona.Confirmados[0] != 1 || carona.EmEspera[0] != 0 {
		t.Errorf("car-1 = %d livres, %d confirmados, %d em espera, want 0, 1, 0",
			carona.Livres[0], carona.Confirmados[0], carona.EmEspera[0])
	}
}

// TestTR2ItinerarioEntreTresEmpresasEhAtomico é o TR2 da seção 11.3, e o teste
// central da fase: itinerário A+B+C, C com 1 assento, A e B com 10, 50
// passageiros simultâneos.
//
// O que ele afirma é o RD01 — ou todos os trechos, ou nenhum — entre empresas:
// **nunca** dois sucessos, e, após a quiescência, cada assento de A e de B ou
// pertence ao vencedor ou está de volta ao mercado. Nenhuma tentativa perdedora
// pode deixar algo reservado em A ou em B, ainda que as duas tenham votado SIM
// e descontado o assento antes de a decisão sair. Quem devolve é o ABORTAR.
//
// **Por que "exatamente um", e não "no máximo um".** Até a fase 6 este teste
// afirmava apenas que ninguém vendia o assento duas vezes, porque com o
// participante no-wait a rodada podia terminar sem vencedor: a tentativa que
// ganhava o assento único de C era muitas vezes uma das 40 que perdiam A, e
// desistia na hora, devolvendo C depois de as outras já terem desistido.
//
// O wait-die acaba com isso, pelo motivo exato da seção 8.2: A esgotada por
// **portadores em PREPARADO** não responde SEM_ASSENTO — responde AGUARDE ou
// PRECEDENCIA —, e aí a D36 manda tentar de novo com o mesmo carimbo. A mais
// velha espera em vez de morrer, e alguém sempre chega ao fim. É a asserção
// que a seção 11.3 pede.
//
// É o T2 do P1 agora distribuído. A diferença é o que pode dar errado: no P1,
// uma ordem de validação ruim; aqui, um abort que não chega.
func TestTR2ItinerarioEntreTresEmpresasEhAtomico(t *testing.T) {
	consorcio := subirConsorcioP2(t, opcoesConsorcio{
		caronas: caronasEncadeadas(map[string]int{
			"vaijunto":       10,
			"rotacapixaba":   10,
			"rotafluminense": 1,
		}),
		tcp:       true,
		auditoria: true,
	})

	const passageiros = 50
	empresas := []string{"vaijunto", "rotacapixaba", "rotafluminense"}
	carga := passageirosDaCarga()

	itinerario := reserva(
		trecho("vaijunto:car-1", 0, 1),
		trecho("rotacapixaba:car-1", 0, 1),
		trecho("rotafluminense:car-1", 0, 1),
	)

	pedidos := make([]pedidoDeReserva, 0, passageiros)
	for i := 0; i < passageiros; i++ {
		nome := empresas[i%len(empresas)]
		pedidos = append(pedidos, pedidoDeReserva{
			endereco: consorcio[nome].endereco,
			usuario:  carga[i].usuario,
			senha:    carga[i].senha,
			pedido:   itinerario,
		})
	}

	respostas := reservarEmParalelo(t, pedidos)
	oks, porCodigo := contar(respostas)
	// Dois sucessos significariam o assento único de C vendido duas vezes;
	// nenhum significaria que a disputa não converge. Sob wait-die, é um.
	if oks != 1 {
		t.Fatalf("confirmações = %d, want exatamente 1 (códigos: %v)", oks, porCodigo)
	}
	// Toda recusa tem de ser por falta de assento. Qualquer outro código aqui
	// — ITINERARIO_INVALIDO, EMPRESA_INDISPONIVEL, ERRO_INTERNO — seria defeito,
	// e não disputa.
	if recusas := recusasPorFaltaDeAssento(porCodigo); recusas != len(pedidos)-oks {
		t.Errorf("recusas por falta de assento = %d, want %d (códigos: %v)",
			recusas, len(pedidos)-oks, porCodigo)
	}

	// A fase 2 precisa terminar antes da contagem: enquanto os ABORTAR estão no
	// ar, os assentos de A e B legitimamente ainda estão em espera.
	esperarQuiescencia(t, consorcio)
	todas := auditoriasDe(t, consorcio)
	verificarInvariantesP2(t, todas)

	// Cada assento consumido em A e em B é o do vencedor, e nada mais: as
	// perdedoras devolveram tudo. Com um vencedor, 9 livres e 1 confirmado; sem
	// vencedor, os 10 de volta e nenhum confirmado. O que o teste não pode ver,
	// em nenhum dos dois casos, é assento em espera sobrando ou confirmado que
	// não corresponda ao vencedor.
	for _, nome := range []string{"vaijunto", "rotacapixaba"} {
		carona := todas[nome].Caronas[0]
		if carona.Livres[0] != 10-oks {
			t.Errorf("%s: livres = %d, want %d — as tentativas perdedoras não devolveram o assento",
				nome, carona.Livres[0], 10-oks)
		}
		if carona.Confirmados[0] != oks || carona.EmEspera[0] != 0 {
			t.Errorf("%s: %d confirmados e %d em espera, want %d e 0",
				nome, carona.Confirmados[0], carona.EmEspera[0], oks)
		}
	}
	// E o trecho disputado vendeu o seu único assento, no máximo, uma vez.
	disputada := todas["rotafluminense"].Caronas[0]
	if disputada.Livres[0] != 1-oks || disputada.Confirmados[0] != oks {
		t.Errorf("rotafluminense: %d livres e %d confirmados, want %d e %d",
			disputada.Livres[0], disputada.Confirmados[0], 1-oks, oks)
	}
}

// TestTR5MesmoPassageiroDoisReservarSimultaneos é o TR5 da seção 11.3: duas
// conexões do mesmo passageiro reservando ao mesmo tempo.
//
// Um recebe RESERVA_EM_ANDAMENTO (D35). A marca existe porque, no P2, a
// checagem de sobreposição e a gravação não estão mais na mesma seção crítica:
// entre a votação e a decisão passa-se tempo de rede, e sem a marca as duas
// tentativas poderiam passar na checagem e confirmar reservas sobrepostas.
//
// O itinerário é o mesmo nas duas, então a sobreposição é total: se a marca
// falhar, o passageiro acaba com duas reservas no mesmo horário, que é o T8 do
// P1 furado.
func TestTR5MesmoPassageiroDoisReservarSimultaneos(t *testing.T) {
	consorcio := subirConsorcioP2(t, opcoesConsorcio{
		caronas: caronasEncadeadas(map[string]int{
			// Assentos de sobra nas duas: o único motivo possível de recusa é a
			// exclusividade, nunca a falta de assento.
			"vaijunto":     10,
			"rotacapixaba": 10,
		}),
		tcp:       true,
		auditoria: true,
	})

	endereco := consorcio["vaijunto"].endereco
	itinerario := reserva(
		trecho("vaijunto:car-1", 0, 1),
		trecho("rotacapixaba:car-1", 0, 1),
	)

	respostas := reservarEmParalelo(t, []pedidoDeReserva{
		{endereco: endereco, usuario: "maria", senha: "abcd", pedido: itinerario},
		{endereco: endereco, usuario: "maria", senha: "abcd", pedido: itinerario},
	})

	oks, porCodigo := contar(respostas)
	if oks != 1 {
		t.Errorf("confirmações = %d, want exatamente 1 (códigos: %v)", oks, porCodigo)
	}
	// A recusa pode vir pelos dois caminhos, como no T8: a marca da D35 quando
	// a segunda chega durante a votação da primeira, ou a sobreposição da D14
	// quando ela chega depois da decisão. Nesta disputa simultânea é quase
	// sempre a primeira.
	recusas := porCodigo[protocolo.CodigoReservaEmAndamento] + porCodigo[protocolo.CodigoConflitoHorario]
	if recusas != 1 {
		t.Errorf("recusas = %d, want 1 (códigos: %v)", recusas, porCodigo)
	}
	if porCodigo[protocolo.CodigoReservaEmAndamento] == 0 && porCodigo[protocolo.CodigoConflitoHorario] == 0 {
		t.Errorf("nenhuma recusa por exclusividade ou sobreposição: %v", porCodigo)
	}

	esperarQuiescencia(t, consorcio)
	todas := auditoriasDe(t, consorcio)
	verificarInvariantesP2(t, todas)

	// Uma reserva ativa, nunca duas, e a marca não ficou presa: um RESERVAR
	// posterior tem de ser atendido, e recusado pela sobreposição — não pela
	// exclusividade.
	c := conectar(t, endereco)
	c.entrar("maria", "abcd")
	var listadas protocolo.ListarMinhasReservasResposta
	c.exigirOK(protocolo.TipoListarMinhasReservas, protocolo.ListarMinhasReservasRequisicao{}, &listadas)
	if len(listadas.Reservas) != 1 {
		t.Fatalf("maria tem %d reservas ativas, want 1", len(listadas.Reservas))
	}

	depois := c.enviar(protocolo.TipoReservar, itinerario)
	if depois.Codigo != protocolo.CodigoConflitoHorario {
		t.Errorf("RESERVAR depois da disputa: codigo = %q, want %s — a marca da D35 ficou presa? (mensagem: %q)",
			depois.Codigo, protocolo.CodigoConflitoHorario, depois.Mensagem)
	}
}

// TestD34CoordenadorValidaSobreAsPernasAutoritativas é a D34 vista pelo lado
// que importa: o coordenador **não** confia no que a busca mostrou, e valida o
// encadeamento sobre os dados que os donos devolveram no voto.
//
// O cenário monta o caso que distingue as duas coisas: as caronas encadeiam no
// espaço, cada dona valida a sua e vota SIM, mas os horários **autoritativos**
// deixam só 10 minutos de baldeação — abaixo de MARGEM_BALDEACAO. Nenhuma
// empresa sozinha poderia descobrir isso, porque cada uma só conhece a sua
// perna; é exatamente a divisão de validação da D34.
//
// O que se afirma: a reserva é recusada com ITINERARIO_INVALIDO e, depois do
// abort, nenhum assento fica em espera em empresa nenhuma. A decisão não é
// gravada (D23), e por isso não há o que desfazer.
func TestD34CoordenadorValidaSobreAsPernasAutoritativas(t *testing.T) {
	consorcio := subirConsorcioP2(t, opcoesConsorcio{
		caronas: map[string][]byte{
			"vaijunto": []byte(`[{"id":"vaijunto:car-1","motorista":"joao",
				"rota":["Salvador","Feira de Santana"],
				"horarios":["2026-11-05T06:00:00-03:00","2026-11-05T08:00:00-03:00"],
				"assentos":3,"precos_centavos":[2000]}]`),
			// Parte 10 min depois de a primeira chegar: encadeia no espaço, não
			// no tempo. A dona desta carona não tem como saber disso.
			"rotacapixaba": []byte(`[{"id":"rotacapixaba:car-1","motorista":"joao",
				"rota":["Feira de Santana","Jequié"],
				"horarios":["2026-11-05T08:10:00-03:00","2026-11-05T10:00:00-03:00"],
				"assentos":3,"precos_centavos":[3000]}]`),
		},
		tcp:       true,
		auditoria: true,
	})

	c := conectar(t, consorcio["vaijunto"].endereco)
	c.entrar("maria", "abcd")

	resp := c.enviar(protocolo.TipoReservar, reserva(
		trecho("vaijunto:car-1", 0, 1),
		trecho("rotacapixaba:car-1", 0, 1),
	))
	if resp.Codigo != protocolo.CodigoItinerarioInvalido {
		t.Fatalf("codigo = %q, want %s (mensagem: %q)",
			resp.Codigo, protocolo.CodigoItinerarioInvalido, resp.Mensagem)
	}

	// Os dois votaram SIM e descontaram o assento antes de o coordenador
	// validar; o ABORTAR é que os devolve. Depois dele, nada em espera.
	esperarQuiescencia(t, consorcio)
	todas := auditoriasDe(t, consorcio)
	verificarInvariantesP2(t, todas)

	for nome, a := range todas {
		carona := a.Caronas[0]
		if carona.Livres[0] != 3 || carona.EmEspera[0] != 0 || carona.Confirmados[0] != 0 {
			t.Errorf("%s: %d livres, %d confirmados, %d em espera, want 3, 0, 0 — o abort não devolveu o assento",
				nome, carona.Livres[0], carona.Confirmados[0], carona.EmEspera[0])
		}
		// Abort presumido (D23): nenhuma reserva foi gravada em lugar nenhum.
		if len(a.Reservas) != 0 {
			t.Errorf("%s gravou reserva para uma transação abortada: %+v", nome, a.Reservas)
		}
	}
}

// TestD42ListarReservasFuncionaComParForaDoAr é a razão de ser da D42: a reserva
// guarda a cópia das pernas que os donos devolveram no voto SIM, e listar não
// depende de eles estarem no ar (RD05).
//
// No P1 a reserva guardava só os índices, e os dados legíveis eram resolvidos
// contra as caronas a cada listagem, para não guardar o mesmo dado duas vezes.
// No P2 as caronas das pernas remotas estão em outros servidores: resolver na
// hora faria a listagem falhar — ou mentir — sempre que um par caísse.
//
// A cópia não diverge porque rota, horários e preços são imutáveis depois da
// publicação (D09).
func TestD42ListarReservasFuncionaComParForaDoAr(t *testing.T) {
	consorcio := subirConsorcioP2(t, opcoesConsorcio{
		caronas: caronasEncadeadas(map[string]int{
			"vaijunto":     3,
			"rotacapixaba": 3,
		}),
		tcp:       true,
		auditoria: true,
	})

	c := conectar(t, consorcio["vaijunto"].endereco)
	c.entrar("maria", "abcd")

	var confirmada protocolo.ReservarResposta
	c.exigirOK(protocolo.TipoReservar, reserva(
		trecho("vaijunto:car-1", 0, 1),
		trecho("rotacapixaba:car-1", 0, 1),
	), &confirmada)

	esperarQuiescencia(t, consorcio)

	// Agora a rotacapixaba sai do ar. A perna dela continua na reserva.
	if err := consorcio["rotacapixaba"].rest.Fechar(); err != nil {
		t.Fatalf("fechar a porta da rotacapixaba: %v", err)
	}

	var listadas protocolo.ListarMinhasReservasResposta
	c.exigirOK(protocolo.TipoListarMinhasReservas, protocolo.ListarMinhasReservasRequisicao{}, &listadas)
	if len(listadas.Reservas) != 1 {
		t.Fatalf("maria tem %d reservas, want 1 — a listagem dependeu do par", len(listadas.Reservas))
	}

	guardada := listadas.Reservas[0]
	if guardada.ReservaID != confirmada.ReservaID {
		t.Errorf("reserva listada = %q, want %q", guardada.ReservaID, confirmada.ReservaID)
	}
	if len(guardada.Trechos) != 2 {
		t.Fatalf("reserva com %d trechos, want 2 — a perna remota sumiu com o par", len(guardada.Trechos))
	}

	// A perna da empresa fora do ar vem completa: empresa, motorista, cidades,
	// horários e preço, tudo da cópia.
	remota := guardada.Trechos[1]
	if remota.Empresa != "rotacapixaba" {
		t.Errorf("empresa da perna remota = %q, want rotacapixaba", remota.Empresa)
	}
	if remota.Origem != "Feira de Santana" || remota.Destino != "Jequié" {
		t.Errorf("perna remota = %s → %s, want Feira de Santana → Jequié", remota.Origem, remota.Destino)
	}
	if remota.Motorista == "" {
		t.Errorf("perna remota sem nome de motorista: %+v", remota)
	}
	if remota.PrecoCentavos != 3000 {
		t.Errorf("preço da perna remota = %d, want 3000", remota.PrecoCentavos)
	}
	if guardada.PrecoTotalCentavos != 5000 {
		t.Errorf("preço total = %d, want 5000", guardada.PrecoTotalCentavos)
	}
}

// TestAuditoriaRefleteOEstadoDasTresEmpresas confere o formato e o conteúdo de
// GET /auditoria (seção 4.9 da API), que é o instrumento de que o verificador
// depende.
//
// Vale um teste próprio porque, se a auditoria mentir, todos os outros testes
// desta fase passam a afirmar nada: eles leem o mundo por ela. Aqui o estado é
// conhecido — uma reserva, três empresas, uma perna em cada —, e a conferência
// é contra esse estado, não contra a própria auditoria.
func TestAuditoriaRefleteOEstadoDasTresEmpresas(t *testing.T) {
	consorcio := subirConsorcioP2(t, opcoesConsorcio{
		caronas: caronasEncadeadas(map[string]int{
			"vaijunto":       3,
			"rotacapixaba":   3,
			"rotafluminense": 3,
		}),
		tcp:       true,
		auditoria: true,
	})

	c := conectar(t, consorcio["vaijunto"].endereco)
	c.entrar("maria", "abcd")
	var confirmada protocolo.ReservarResposta
	c.exigirOK(protocolo.TipoReservar, reserva(
		trecho("vaijunto:car-1", 0, 1),
		trecho("rotacapixaba:car-1", 0, 1),
		trecho("rotafluminense:car-1", 0, 1),
	), &confirmada)

	esperarQuiescencia(t, consorcio)
	todas := auditoriasDe(t, consorcio)
	verificarInvariantesP2(t, todas)

	// Visão de dono: as três empresas descontaram um assento e estão
	// CONFIRMADO para a mesma transação.
	for nome, a := range todas {
		if a.Empresa != nome {
			t.Errorf("auditoria de %s diz empresa = %q", nome, a.Empresa)
		}
		if len(a.Participacoes) != 1 {
			t.Fatalf("%s tem %d participações, want 1", nome, len(a.Participacoes))
		}
		pt := a.Participacoes[0]
		if pt.Tx != confirmada.ReservaID {
			t.Errorf("%s: participação de %q, want %q", nome, pt.Tx, confirmada.ReservaID)
		}
		if pt.Coordenador != "vaijunto" {
			t.Errorf("%s: coordenador = %q, want vaijunto", nome, pt.Coordenador)
		}
		if pt.Situacao != "CONFIRMADO" {
			t.Errorf("%s: situação = %q, want CONFIRMADO", nome, pt.Situacao)
		}
		// O carimbo é o mesmo nas três: é uma transação só (D24).
		if pt.Carimbo.Empresa != "vaijunto" || pt.Carimbo.Lamport == 0 {
			t.Errorf("%s: carimbo = %+v, want empresa vaijunto e lamport > 0", nome, pt.Carimbo)
		}
		if carona := a.Caronas[0]; carona.Livres[0] != 2 || carona.Confirmados[0] != 1 {
			t.Errorf("%s: %d livres e %d confirmados, want 2 e 1",
				nome, carona.Livres[0], carona.Confirmados[0])
		}
		// Este cenário não cancela nada, então o outbox fica vazio (I-P2-3).
		if len(a.Outbox) != 0 {
			t.Errorf("%s: outbox com %d avisos, want 0", nome, len(a.Outbox))
		}
	}

	// Visão de coordenador: só a vaijunto tem a reserva, com os três
	// participantes e a fase 2 concluída.
	if n := len(todas["vaijunto"].Reservas); n != 1 {
		t.Fatalf("vaijunto tem %d reservas, want 1", n)
	}
	r := todas["vaijunto"].Reservas[0]
	if r.ID != confirmada.ReservaID || !r.Ativa || !r.Concluida {
		t.Errorf("reserva = %+v, want %q ativa e concluída", r, confirmada.ReservaID)
	}
	if len(r.Participantes) != 3 {
		t.Errorf("participantes = %v, want as três empresas", r.Participantes)
	}
	// Coordenar não é guardar reserva: as outras duas são só participantes.
	for _, nome := range []string{"rotacapixaba", "rotafluminense"} {
		if n := len(todas[nome].Reservas); n != 0 {
			t.Errorf("%s guarda %d reserva(s), want 0 — a reserva é do coordenador", nome, n)
		}
	}
}
