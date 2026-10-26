package testes

import (
	"strings"
	"testing"
	"time"

	"vaijunto/internal/protocolo"
	"vaijunto/internal/simulador"
)

// Simulador de passageiros (E4; PROJETO_P2.md, seção 11.4).
//
// O que este teste afirma não é desempenho — a medição roda sem -race, e aqui
// o detector está ligado —, e sim que a carga **só produz respostas que o
// PROTOCOL.md prevê** e que ela deixa o consórcio consistente. Um simulador
// malformado é fácil de não notar: ele continua imprimindo uma tabela bonita,
// com CAMPO_INVALIDO ou NAO_AUTENTICADO em todas as linhas.
//
// É também o esqueleto do TS1 da seção 11.5, que roda o mesmo Rodar contra as
// três empresas em contêineres e derruba uma no meio.

// codigosEsperadosNaCarga são as respostas que a carga pode legitimamente
// receber.
//
// A lista é curta de propósito. SEM_ASSENTO e DISPUTA_PERDIDA são a disputa
// pelo assento escasso; EMPRESA_INDISPONIVEL é a queda de um dono detectada
// por timeout (D30); RESERVA_EM_ANDAMENTO é a D35 e CONFLITO_HORARIO é a D14,
// os dois possíveis quando um cancelamento é recusado e o passageiro segue com
// a reserva de pé no servidor.
//
// Qualquer outro código é defeito do simulador, e não resultado da medição:
// CAMPO_INVALIDO ou ROTA_INVALIDA seriam requisição malformada,
// NAO_AUTENTICADO seria sessão perdida, ITINERARIO_INVALIDO seria reservar
// algo que a busca não devolveu, PRAZO_CANCELAMENTO_EXPIRADO seria cenário
// montado perto demais do relógio e ERRO_INTERNO seria defeito do servidor.
var codigosEsperadosNaCarga = map[string]bool{
	protocolo.StatusOK:                  true,
	protocolo.CodigoSemAssento:          true,
	protocolo.CodigoDisputaPerdida:      true,
	protocolo.CodigoEmpresaIndisponivel: true,
	protocolo.CodigoReservaEmAndamento:  true,
	protocolo.CodigoConflitoHorario:     true,
}

// consorcioVazio sobe as três empresas sem carona nenhuma, com a porta TCP, a
// busca federada e a auditoria ligadas. As caronas deste teste são as que o
// próprio simulador publica na preparação.
func consorcioVazio(t *testing.T) map[string]*empresa {
	t.Helper()
	return subirConsorcioP2(t, opcoesConsorcio{
		caronas: map[string][]byte{
			"vaijunto":       {'[', ']'},
			"rotacapixaba":   {'[', ']'},
			"rotafluminense": {'[', ']'},
		},
		tcp:       true,
		busca:     true,
		auditoria: true,
	})
}

// empresasDoSimulador aponta o simulador para o consórcio em processo.
//
// O motorista é joao nas três porque os testes carregam o fixture do P1 (seção
// 12.4) nas três empresas, e ele é o único usuarios.json que existe ali. No
// compose, cada empresa tem o seu motorista (joao, rafael, marcos), e é
// cmd/simulador que sabe disso.
func empresasDoSimulador(consorcio map[string]*empresa) []simulador.Empresa {
	nomes := []string{"vaijunto", "rotacapixaba", "rotafluminense"}
	empresas := make([]simulador.Empresa, 0, len(nomes))
	for _, nome := range nomes {
		empresas = append(empresas, simulador.Empresa{
			Nome:            nome,
			Endereco:        consorcio[nome].endereco,
			Motorista:       "joao",
			SenhaMotorista:  "1234",
			SenhaPassageiro: "teste",
		})
	}
	return empresas
}

// configDaCarga é uma rodada curta, com assento escasso para que a disputa
// aconteça de verdade dentro dos milissegundos do teste.
func configDaCarga(t *testing.T, consorcio map[string]*empresa, cenario string) simulador.Config {
	t.Helper()
	return simulador.Config{
		Empresas:                  empresasDoSimulador(consorcio),
		Cenario:                   cenario,
		Passageiros:               2,
		Duracao:                   200 * time.Millisecond,
		ProbabilidadeCancelamento: 0.5,
		FracaoBuscaVazia:          0.2,
		Assentos:                  1,
		// Dia base fixo: o teste em processo começa com o log vazio, então não
		// há rodada anterior de quem fugir, e um dia fixo mantém o cenário
		// igual entre execuções.
		DiaBase: 90,
		Semente: 1,
		Prazo:   prazoLeitura,
		Saida:   t.TempDir(),
	}
}

// TestSimuladorFederadoSoProduzRespostasDoProtocolo roda a carga com uma perna
// em cada empresa — o caminho que exercita o 2PC com dois donos remotos — e
// confere os códigos, as métricas e as invariantes.
func TestSimuladorFederadoSoProduzRespostasDoProtocolo(t *testing.T) {
	consorcio := consorcioVazio(t)

	resultado, err := simulador.Rodar(configDaCarga(t, consorcio, simulador.CenarioFederado))
	if err != nil {
		t.Fatalf("Rodar: %v", err)
	}

	conferirCodigosDaCarga(t, resultado)
	conferirOperacoesDaCarga(t, resultado)

	// Uma participação em cada empresa é a prova de que o itinerário reservado
	// tinha mesmo uma perna por empresa: a participação só existe em quem é
	// dono de um trecho da transação (D21).
	esperarQuiescencia(t, consorcio)
	todas := auditoriasDe(t, consorcio)
	for nome, visao := range todas {
		if len(visao.Participacoes) == 0 {
			t.Errorf("a %s não participou de nenhuma transação: o itinerário não atravessou as três empresas", nome)
		}
	}
	verificarInvariantesP2(t, todas)

	if len(resultado.Arquivos) != 2 {
		t.Errorf("a rodada gravou %d arquivo(s), queria 2 (latências e códigos)", len(resultado.Arquivos))
	}
}

// TestSimuladorLocalSoProduzRespostasDoProtocolo roda a carga com itinerários
// de uma perna, da empresa do próprio passageiro: é o 2PC degenerado, sem
// chamada de rede, e o ponto de comparação do custo da coordenação (seção
// 11.6).
func TestSimuladorLocalSoProduzRespostasDoProtocolo(t *testing.T) {
	consorcio := consorcioVazio(t)

	resultado, err := simulador.Rodar(configDaCarga(t, consorcio, simulador.CenarioLocal))
	if err != nil {
		t.Fatalf("Rodar: %v", err)
	}

	conferirCodigosDaCarga(t, resultado)
	conferirOperacoesDaCarga(t, resultado)

	// Aqui cada empresa só participa das transações dos seus próprios
	// passageiros, e nenhuma guarda participação de carona de outra (I-P2-4).
	esperarQuiescencia(t, consorcio)
	verificarInvariantesP2(t, auditoriasDe(t, consorcio))
}

// TestSimuladorCurvaMedeUmPontoPorValor confere o que a seção 11.6 pede: cada
// ponto da curva é um total de passageiros no consórcio, medido em sequência
// sobre o mesmo consórcio.
func TestSimuladorCurvaMedeUmPontoPorValor(t *testing.T) {
	consorcio := consorcioVazio(t)

	cfg := configDaCarga(t, consorcio, simulador.CenarioFederado)
	cfg.Curva = []int{1, 6}
	cfg.Duracao = 100 * time.Millisecond

	resultado, err := simulador.Rodar(cfg)
	if err != nil {
		t.Fatalf("Rodar: %v", err)
	}
	if len(resultado.Pontos) != len(cfg.Curva) {
		t.Fatalf("a curva mediu %d ponto(s), queria %d", len(resultado.Pontos), len(cfg.Curva))
	}
	for i, ponto := range resultado.Pontos {
		if ponto.Passageiros != cfg.Curva[i] {
			t.Errorf("ponto %d com %d passageiros, queria %d", i, ponto.Passageiros, cfg.Curva[i])
		}
	}

	conferirCodigosDaCarga(t, resultado)
	esperarQuiescencia(t, consorcio)
	verificarInvariantesP2(t, auditoriasDe(t, consorcio))
}

// conferirCodigosDaCarga é o critério de pronto da fase 10: nenhuma resposta
// fora dos códigos que a carga pode produzir.
func conferirCodigosDaCarga(t *testing.T, resultado simulador.Resultado) {
	t.Helper()
	observados := resultado.CodigosObservados()
	if len(observados) == 0 {
		t.Fatalf("a carga não registrou resposta nenhuma")
	}
	for codigo, quantidade := range observados {
		if !codigosEsperadosNaCarga[codigo] {
			t.Errorf("a carga recebeu %d resposta(s) %s, que não é resultado esperado (códigos: %v)",
				quantidade, codigo, observados)
		}
	}
}

// conferirOperacoesDaCarga afirma que o ciclo da seção 11.4 aconteceu inteiro.
// Sem isto, uma carga que só buscasse — porque a reserva falha sempre, por
// exemplo — passaria no teste dos códigos.
func conferirOperacoesDaCarga(t *testing.T, resultado simulador.Resultado) {
	t.Helper()
	oks := make(map[string]int)
	requisicoes := make(map[string]int)
	for _, ponto := range resultado.Pontos {
		for _, resumo := range ponto.Operacoes {
			requisicoes[resumo.Operacao] += resumo.Requisicoes
			oks[resumo.Operacao] += resumo.PorCodigo[protocolo.StatusOK]
		}
	}

	for _, operacao := range []string{simulador.OpBuscar, simulador.OpReservar, simulador.OpCancelar} {
		if oks[operacao] == 0 {
			t.Errorf("nenhuma %s respondeu OK em %d requisição(ões): o ciclo não chegou ao fim",
				operacao, requisicoes[operacao])
		}
	}
}

// TestSimuladorRecusaCenarioSujo é a guarda que faltava e que apareceu na
// primeira medição de verdade: as caronas da rodada anterior ficam no log
// (D22), e uma rodada nova sobre os mesmos dias mede SEM_ASSENTO nas caronas
// velhas e CONFLITO_HORARIO com as reservas que elas ainda sustentam.
//
// O dia base derivado do relógio separa as rodadas em blocos da largura da
// rodada; este teste força a colisão com --dia-base fixo e exige a recusa, para
// que o caso nunca volte a sair como medição plausível.
func TestSimuladorRecusaCenarioSujo(t *testing.T) {
	consorcio := consorcioVazio(t)

	cfg := configDaCarga(t, consorcio, simulador.CenarioFederado)
	cfg.Duracao = 50 * time.Millisecond
	if _, err := simulador.Rodar(cfg); err != nil {
		t.Fatalf("primeira rodada: %v", err)
	}

	// Mesmo dia base: a preparação publica em cima do cenário anterior.
	_, err := simulador.Rodar(cfg)
	if err == nil {
		t.Fatalf("a segunda rodada no mesmo --dia-base foi aceita")
	}
	if !strings.Contains(err.Error(), "--dia-base") {
		t.Errorf("a recusa não diz como sair dela: %v", err)
	}
}
