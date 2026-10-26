package simulador

import (
	"testing"
	"time"

	"vaijunto/internal/dominio"
)

// configDeTeste é uma rodada de três empresas, com os motoristas do fixture do
// P1 (o mesmo usuarios.json nas três, em processo).
func configDeTeste(cenario string) Config {
	return Config{
		Empresas: []Empresa{
			{Nome: "vaijunto", Endereco: "127.0.0.1:1", Motorista: "joao", SenhaMotorista: "1234", SenhaPassageiro: "teste"},
			{Nome: "rotacapixaba", Endereco: "127.0.0.1:2", Motorista: "carlos", SenhaMotorista: "1234", SenhaPassageiro: "teste"},
			{Nome: "rotafluminense", Endereco: "127.0.0.1:3", Motorista: "ana", SenhaMotorista: "1234", SenhaPassageiro: "teste"},
		},
		Cenario:     cenario,
		Passageiros: 2,
		Duracao:     time.Second,
		Assentos:    2,
		Semente:     1,
		Prazo:       time.Second,
	}
}

// TestPlanoFederadoEncadeiaUmaPernaPorEmpresa é a invariante do cenário: a
// corrente que a busca precisa achar só existe se cada empresa publicar a sua
// perna no lugar certo da fila de cidades.
func TestPlanoFederadoEncadeiaUmaPernaPorEmpresa(t *testing.T) {
	cfg := configDeTeste(CenarioFederado)
	plano, publicacoes, err := montarPlano(cfg, 2, 60)
	if err != nil {
		t.Fatalf("montarPlano: %v", err)
	}

	if len(publicacoes) != 2*len(cfg.Empresas) {
		t.Fatalf("%d publicações, queria %d (2 dias × 3 empresas)", len(publicacoes), 2*len(cfg.Empresas))
	}

	cidades := dominio.CidadesAtendidas()
	// Todos os passageiros buscam da primeira à última cidade da corrente, seja
	// qual for a empresa deles: é o itinerário com uma perna em cada empresa.
	for _, e := range cfg.Empresas {
		if p := plano.busca[e.Nome]; p.origem != cidades[0] || p.destino != cidades[len(cfg.Empresas)] {
			t.Errorf("%s busca %s → %s, queria %s → %s", e.Nome, p.origem, p.destino,
				cidades[0], cidades[len(cfg.Empresas)])
		}
	}

	// Perna k da empresa k, no primeiro dia.
	primeiroDia := plano.dias[0]
	var anterior time.Time
	for k, e := range cfg.Empresas {
		var achada *publicacao
		for i := range publicacoes {
			p := publicacoes[i]
			if p.empresa == e.Nome && p.paradas[0].Horario.Truncate(24*time.Hour).Equal(primeiroDia.Truncate(24*time.Hour)) {
				achada = &publicacoes[i]
				break
			}
		}
		if achada == nil {
			t.Fatalf("nenhuma perna da %s no primeiro dia", e.Nome)
		}
		if achada.paradas[0].Cidade != cidades[k] || achada.paradas[1].Cidade != cidades[k+1] {
			t.Errorf("%s publica %s → %s, queria %s → %s", e.Nome,
				achada.paradas[0].Cidade, achada.paradas[1].Cidade, cidades[k], cidades[k+1])
		}
		if k > 0 {
			folga := achada.paradas[0].Horario.Sub(anterior)
			if folga < dominio.MARGEM_BALDEACAO || folga > dominio.ESPERA_MAXIMA_BALDEACAO {
				t.Errorf("folga de %v antes da perna da %s, fora de [%v, %v] (D13)",
					folga, e.Nome, dominio.MARGEM_BALDEACAO, dominio.ESPERA_MAXIMA_BALDEACAO)
			}
		}
		anterior = achada.paradas[1].Horario
	}
}

// TestPlanoLocalDaUmParSoSeuACadaEmpresa é o que mantém o cenário local
// local: se duas empresas dividissem uma cidade, a busca poderia encadeá-las e
// o RESERVAR medido deixaria de ser o de um participante só.
func TestPlanoLocalDaUmParSoSeuACadaEmpresa(t *testing.T) {
	cfg := configDeTeste(CenarioLocal)
	plano, publicacoes, err := montarPlano(cfg, 3, 60)
	if err != nil {
		t.Fatalf("montarPlano: %v", err)
	}
	if len(publicacoes) != 3*len(cfg.Empresas) {
		t.Fatalf("%d publicações, queria %d", len(publicacoes), 3*len(cfg.Empresas))
	}

	vistas := make(map[string]string)
	for _, e := range cfg.Empresas {
		p := plano.busca[e.Nome]
		for _, cidade := range []string{p.origem, p.destino} {
			if dona, repetida := vistas[cidade]; repetida {
				t.Errorf("%s aparece no par da %s e no da %s", cidade, dona, e.Nome)
			}
			vistas[cidade] = e.Nome
		}
	}

	// Cada publicação é uma direta, e é da empresa cujo par ela serve.
	for _, p := range publicacoes {
		if len(p.paradas) != 2 {
			t.Errorf("carona da %s com %d paradas, queria 2 (direta)", p.empresa, len(p.paradas))
		}
		esperado := plano.busca[p.empresa]
		if p.paradas[0].Cidade != esperado.origem || p.paradas[1].Cidade != esperado.destino {
			t.Errorf("%s publica %s → %s, queria %s → %s", p.empresa,
				p.paradas[0].Cidade, p.paradas[1].Cidade, esperado.origem, esperado.destino)
		}
	}
}

// TestPlanoRecusaCatalogoInsuficiente: o cenário local precisa de duas cidades
// por empresa, e recusar antes de publicar é a regra de validar tudo antes de
// escrever qualquer coisa.
func TestPlanoRecusaCatalogoInsuficiente(t *testing.T) {
	cfg := configDeTeste(CenarioLocal)
	for i := 0; i < 3; i++ {
		cfg.Empresas = append(cfg.Empresas, Empresa{
			Nome: "extra", Endereco: "127.0.0.1:9", Motorista: "joao",
		})
	}
	if _, _, err := montarPlano(cfg, 1, 60); err == nil {
		t.Fatalf("montarPlano aceitou 6 empresas no cenário local, e o catálogo tem %d cidades",
			len(dominio.CidadesAtendidas()))
	}
}

// TestPlanoUsaOFusoDasCidades é a lição da seção 10.1 do PROJETO.md: dentro do
// contêiner, time.Local é UTC, e uma carona montada nele sairia três horas
// deslocada. Os testes rodam com time.Local forçado para UTC, então um horário
// montado em time.Local falharia aqui.
func TestPlanoUsaOFusoDasCidades(t *testing.T) {
	anterior := time.Local
	time.Local = time.UTC
	defer func() { time.Local = anterior }()

	cfg := configDeTeste(CenarioLocal)
	_, publicacoes, err := montarPlano(cfg, 1, 60)
	if err != nil {
		t.Fatalf("montarPlano: %v", err)
	}

	fuso := dominio.FusoDasCidades()
	for _, p := range publicacoes {
		partida := p.paradas[0].Horario.In(fuso)
		if partida.Hour() != horaDaPrimeiraPerna {
			t.Errorf("carona da %s parte às %dh no fuso das cidades, queria %dh",
				p.empresa, partida.Hour(), horaDaPrimeiraPerna)
		}
	}
}

// TestPlanoPublicaNoFuturoComFolgaParaCancelar: com a partida a menos de uma
// hora, todo cancelamento do ciclo responderia PRAZO_CANCELAMENTO_EXPIRADO, e
// a carga mediria um erro em vez de um cancelamento.
func TestPlanoPublicaNoFuturoComFolgaParaCancelar(t *testing.T) {
	cfg := configDeTeste(CenarioFederado)
	_, publicacoes, err := montarPlano(cfg, 1, diaBasePadrao)
	if err != nil {
		t.Fatalf("montarPlano: %v", err)
	}
	for _, p := range publicacoes {
		folga := time.Until(p.paradas[0].Horario)
		if folga <= dominio.ANTECEDENCIA_CANCELAMENTO {
			t.Errorf("carona da %s parte em %v, dentro da ANTECEDENCIA_CANCELAMENTO de %v",
				p.empresa, folga, dominio.ANTECEDENCIA_CANCELAMENTO)
		}
	}
}
