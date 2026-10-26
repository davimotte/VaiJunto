package simulador

import (
	"testing"
	"time"
)

// TestRepartirDistribuiOTotalNoConsorcio fixa a leitura da curva da seção
// 11.6: o número do ponto é o total de passageiros no consórcio, e não o de
// cada empresa, para que o eixo seja o mesmo das curvas do P1.
func TestRepartirDistribuiOTotalNoConsorcio(t *testing.T) {
	empresas := configDeTeste(CenarioFederado).Empresas

	casos := []struct {
		total    int
		esperado map[string]int
	}{
		{1, map[string]int{"vaijunto": 1, "rotacapixaba": 0, "rotafluminense": 0}},
		{10, map[string]int{"vaijunto": 4, "rotacapixaba": 3, "rotafluminense": 3}},
		{50, map[string]int{"vaijunto": 17, "rotacapixaba": 17, "rotafluminense": 16}},
		{100, map[string]int{"vaijunto": 34, "rotacapixaba": 33, "rotafluminense": 33}},
	}
	for _, caso := range casos {
		obtido := repartir(caso.total, empresas)
		soma := 0
		for nome, quantidade := range caso.esperado {
			if obtido[nome] != quantidade {
				t.Errorf("total %d: %s ficou com %d, queria %d", caso.total, nome, obtido[nome], quantidade)
			}
			soma += obtido[nome]
		}
		if soma != caso.total {
			t.Errorf("total %d: a repartição soma %d", caso.total, soma)
		}
	}
}

// TestDiasDobramACapacidadeDaOcupacao é a regra que impede o cenário de
// esgotar: cada passageiro carrega no máximo uma reserva, então a ocupação
// máxima é o número de passageiros, e a preparação cria o dobro de assentos.
func TestDiasDobramACapacidadeDaOcupacao(t *testing.T) {
	cfg := configDeTeste(CenarioFederado)
	cfg.Assentos = 2

	for _, total := range []int{1, 10, 60, 100} {
		dias := cfg.diasDe(total)
		if assentos := dias * cfg.Assentos; assentos < capacidadePorPassageiro*total {
			t.Errorf("%d passageiros: %d dia(s) × %d assentos = %d, menos que os %d pedidos",
				total, dias, cfg.Assentos, assentos, capacidadePorPassageiro*total)
		}
	}

	// --dias explícito manda, inclusive abaixo da regra: é como se força o
	// esgotamento de propósito.
	cfg.Dias = 1
	if dias := cfg.diasDe(100); dias != 1 {
		t.Errorf("com --dias=1, diasDe devolveu %d", dias)
	}
}

func TestTotaisUsaACurvaOuOsPassageirosPorEmpresa(t *testing.T) {
	cfg := configDeTeste(CenarioFederado)
	cfg.Passageiros = 20
	if totais := cfg.totais(); len(totais) != 1 || totais[0] != 60 {
		t.Errorf("sem curva, totais = %v, queria [60] (20 × 3 empresas)", totais)
	}

	cfg.Curva = []int{1, 10}
	if totais := cfg.totais(); len(totais) != 2 || totais[0] != 1 || totais[1] != 10 {
		t.Errorf("com curva, totais = %v, queria [1 10]", totais)
	}
}

// TestValidarRecusaPontoAcimaDasContasDaSemente: a identidade é da conexão
// (D08), e dois passageiros virtuais na mesma conta disputariam a sobreposição
// de horário um com o outro (D14) em vez de um assento.
func TestValidarRecusaPontoAcimaDasContasDaSemente(t *testing.T) {
	cfg := configDeTeste(CenarioFederado)
	cfg.Curva = []int{3 * MaximoPassageirosPorEmpresa}
	if err := cfg.validar(); err != nil {
		t.Errorf("validar recusou %d passageiros, que cabem nas contas da semente: %v", cfg.Curva[0], err)
	}

	cfg.Curva = []int{3*MaximoPassageirosPorEmpresa + 3}
	if err := cfg.validar(); err == nil {
		t.Errorf("validar aceitou %d passageiros, acima das %d contas por empresa",
			cfg.Curva[0], MaximoPassageirosPorEmpresa)
	}
}

func TestValidarRecusaConfiguracaoSemSentido(t *testing.T) {
	casos := map[string]func(*Config){
		"sem empresa":              func(c *Config) { c.Empresas = nil },
		"empresa sem endereço":     func(c *Config) { c.Empresas[0].Endereco = "" },
		"empresa sem motorista":    func(c *Config) { c.Empresas[0].Motorista = "" },
		"cenário desconhecido":     func(c *Config) { c.Cenario = "misto" },
		"duração zero":             func(c *Config) { c.Duracao = 0 },
		"prazo zero":               func(c *Config) { c.Prazo = 0 },
		"sem assento":              func(c *Config) { c.Assentos = 0 },
		"dias negativo":            func(c *Config) { c.Dias = -1 },
		"probabilidade acima de 1": func(c *Config) { c.ProbabilidadeCancelamento = 1.5 },
		"fração negativa":          func(c *Config) { c.FracaoBuscaVazia = -0.1 },
		"ponto sem passageiro":     func(c *Config) { c.Curva = []int{0} },
	}
	for nome, estragar := range casos {
		cfg := configDeTeste(CenarioFederado)
		estragar(&cfg)
		if err := cfg.validar(); err == nil {
			t.Errorf("validar aceitou configuração com %s", nome)
		}
	}
}

func TestValidarAceitaAConfiguracaoPadrao(t *testing.T) {
	cfg := configDeTeste(CenarioFederado)
	if err := cfg.validar(); err != nil {
		t.Fatalf("validar recusou a configuração de teste: %v", err)
	}
}

// TestDiaBaseDerivadoEspalhaAsRodadas: as caronas da rodada anterior ficam no
// log (D22), e duas rodadas nos mesmos dias fariam a busca achar caronas já
// cheias.
func TestDiaBaseDerivadoEspalhaAsRodadas(t *testing.T) {
	const bloco = 60
	agora := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	primeiro := diaBaseDerivado(agora, bloco)
	seguinte := diaBaseDerivado(agora.Add(time.Minute), bloco)

	if primeiro < diaBasePadrao {
		t.Errorf("dia base %d antes do mínimo de %d dias", primeiro, diaBasePadrao)
	}
	if mesmo := diaBaseDerivado(agora.Add(30*time.Second), bloco); mesmo != primeiro {
		t.Errorf("o mesmo minuto deu dias base %d e %d", primeiro, mesmo)
	}

	// O essencial: dois minutos vizinhos não podem ter dias em comum. Avançar
	// de um em um deixaria 59 dos 60 dias em cima da rodada anterior.
	if distancia := seguinte - primeiro; distancia < bloco {
		t.Errorf("minutos vizinhos distam %d dia(s), e a rodada ocupa %d", distancia, bloco)
	}
}

// TestMaiorBlocoEOPontoMaisLargo: o bloco que separa duas rodadas precisa
// cobrir o ponto que consome mais dias, e não o primeiro da curva.
func TestMaiorBlocoEOPontoMaisLargo(t *testing.T) {
	cfg := configDeTeste(CenarioFederado)
	cfg.Assentos = 2
	cfg.Curva = []int{1, 10, 50, 100}

	if bloco, esperado := cfg.maiorBloco(), cfg.diasDe(100); bloco != esperado {
		t.Errorf("maiorBloco = %d, queria %d (os dias do ponto de 100 passageiros)", bloco, esperado)
	}

	cfg.Curva = nil
	cfg.Passageiros = 20
	if bloco, esperado := cfg.maiorBloco(), cfg.diasDe(60); bloco != esperado {
		t.Errorf("maiorBloco = %d, queria %d", bloco, esperado)
	}
}
