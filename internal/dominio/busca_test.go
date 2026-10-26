package dominio

import (
	"errors"
	"testing"
	"time"
)

// Testes das peças que a fase 3B acrescentou ao domínio: a janela da busca
// federada (D40), o recorte que o dono devolve e a validação de uma carona
// recebida de um par (PROJETO_P2.md, seção 6, passos 2, 3 e 5).
//
// O algoritmo em si continua coberto pelos testes de dominio_test.go, que agora
// o exercitam pela composição de CaronasNaJanela com BuscarItinerariosEm.

// A janela vai da meia-noite do dia pedido até 48 h depois, lida no fuso de
// data e nunca em time.Local (PROJETO.md, seção 10.1).
func TestJanelaDaBusca(t *testing.T) {
	// Uma hora qualquer do dia, para provar que a janela começa na meia-noite
	// e não no instante pedido.
	data := time.Date(2026, 11, 5, 22, 30, 0, 0, FusoDasCidades())

	desde, ate := JanelaDaBusca(data)

	querDesde := time.Date(2026, 11, 5, 0, 0, 0, 0, FusoDasCidades())
	if !desde.Equal(querDesde) {
		t.Errorf("desde = %s, want %s", desde, querDesde)
	}
	if !ate.Equal(querDesde.Add(48 * time.Hour)) {
		t.Errorf("ate = %s, want %s", ate, querDesde.Add(48*time.Hour))
	}
	if got := ate.Sub(desde); got != JANELA_BUSCA {
		t.Errorf("largura = %s, want %s", got, JANELA_BUSCA)
	}
	if desde.Location() != data.Location() {
		t.Errorf("fuso de desde = %s, want %s", desde.Location(), data.Location())
	}
}

// A janela de uma carona que parte às 22:00 cobre a conexão do dia seguinte: é
// o encadeamento que a ESPERA_MAXIMA_BALDEACAO permite, e é por isso que 48 h
// não é um número solto.
func TestJanelaDaBuscaCobreAEsperaMaxima(t *testing.T) {
	data := time.Date(2026, 11, 5, 0, 0, 0, 0, FusoDasCidades())
	_, ate := JanelaDaBusca(data)

	// Pior caso de uma primeira perna: parte às 23:59 do dia pedido, e cada
	// baldeação seguinte pode esperar até o teto.
	ultimaPartida := time.Date(2026, 11, 5, 23, 59, 0, 0, FusoDasCidades())
	if primeiraConexao := ultimaPartida.Add(ESPERA_MAXIMA_BALDEACAO); !primeiraConexao.Before(ate) {
		t.Errorf("a janela (%s) não cobre a primeira conexão do pior caso (%s)", ate, primeiraConexao)
	}
}

// CaronasNaJanela aplica o critério "alguma parada em [desde, ate)" (D40) e
// deixa fora a cancelada.
func TestCaronasNaJanela(t *testing.T) {
	e := NovoEstado("vaijunto")

	// Atravessa a meia-noite: parte em 01/10 e chega em 02/10. Serve para
	// provar que o critério é por parada, e não por partida.
	atravessa := publicarNoDia(t, e, []string{"Salvador", "Vitória da Conquista"},
		as(23, 0), as(23, 0).Add(6*time.Hour))
	// Toda no dia 01/10.
	noDia := publicarNoDia(t, e, []string{"Feira de Santana", "Jequié"}, as(8, 0), as(11, 0))

	meiaNoite := as(0, 0)

	// Janela só do dia seguinte: pega a chegada de atravessa, não noDia.
	desde := meiaNoite.Add(24 * time.Hour)
	if ids := idsDe(e.CaronasNaJanela(desde, desde.Add(24*time.Hour))); len(ids) != 1 || ids[0] != atravessa {
		t.Errorf("janela do dia seguinte = %v, want [%s]: o critério é alguma parada, não a partida", ids, atravessa)
	}

	// Janela do dia pedido: pega as duas.
	ids := idsDe(e.CaronasNaJanela(meiaNoite, meiaNoite.Add(JANELA_BUSCA)))
	if len(ids) != 2 {
		t.Fatalf("janela de 48 h = %v, want as duas caronas", ids)
	}
	// E sai ordenada por ID.
	if ids[0] > ids[1] {
		t.Errorf("CaronasNaJanela = %v, want ordenada por ID", ids)
	}

	// Cancelada não sai.
	if _, _, err := e.CancelarCarona(noDia, "joao", as(0, 0)); err != nil {
		t.Fatalf("CancelarCarona: %v", err)
	}
	if ids := idsDe(e.CaronasNaJanela(meiaNoite, meiaNoite.Add(JANELA_BUSCA))); len(ids) != 1 || ids[0] != atravessa {
		t.Errorf("depois do cancelamento = %v, want [%s]", ids, atravessa)
	}
}

// O nome de exibição é resolvido pelo dono, porque só ele tem o mapa de
// usuários (D32): quem recebe a carona pela rede não teria como traduzir um
// identificador de login.
func TestCaronasNaJanelaResolveONomeDoMotorista(t *testing.T) {
	e := NovoEstado("vaijunto")
	e.usuarios["joao"] = &Usuario{Usuario: "joao", Nome: "João Silva", Perfil: "MOTORISTA"}
	publicarNoDia(t, e, []string{"Salvador", "Jequié"}, as(6, 0), as(10, 0))

	caronas := e.CaronasNaJanela(JanelaDaBusca(as(0, 0)))
	if len(caronas) != 1 {
		t.Fatalf("caronas = %d, want 1", len(caronas))
	}
	if caronas[0].MotoristaNome != "João Silva" {
		t.Errorf("MotoristaNome = %q, want João Silva", caronas[0].MotoristaNome)
	}
	if caronas[0].Empresa != "vaijunto" {
		t.Errorf("Empresa = %q, want vaijunto", caronas[0].Empresa)
	}
}

// O que sai de CaronasNaJanela é cópia: alterá-lo não pode tocar o estado, que
// é o que torna seguro rodar a busca fora do lock.
func TestCaronasNaJanelaDevolveCopia(t *testing.T) {
	e := NovoEstado("vaijunto")
	publicarNoDia(t, e, []string{"Salvador", "Jequié"}, as(6, 0), as(10, 0))

	caronas := e.CaronasNaJanela(JanelaDaBusca(as(0, 0)))
	caronas[0].Livres[0] = 0
	caronas[0].Rota[0] = "Petrópolis"

	depois := e.CaronasNaJanela(JanelaDaBusca(as(0, 0)))
	if depois[0].Livres[0] != 2 {
		t.Errorf("Livres do estado = %d, want 2: a fatia devolvida aponta para dentro do estado", depois[0].Livres[0])
	}
	if depois[0].Rota[0] != "Salvador" {
		t.Errorf("Rota do estado = %q, want Salvador: a fatia devolvida aponta para dentro do estado", depois[0].Rota[0])
	}
}

// Validar é o passo 5: uma carona recebida de um par com forma inválida é
// recusada, em vez de derrubar a busca das outras empresas.
func TestCaronaBuscaValidar(t *testing.T) {
	valida := func() CaronaBusca {
		return CaronaBusca{
			ID:            "rotacapixaba:car-1",
			Empresa:       "rotacapixaba",
			MotoristaNome: "Rafael Moreira",
			Rota:          []string{"Vitória", "Cachoeiro de Itapemirim"},
			Horarios:      []time.Time{as(9, 0), as(11, 0)},
			Assentos:      3,
			PrecoTrecho:   []int{3500},
			Livres:        []int{2},
		}
	}

	if err := valida().Validar(); err != nil {
		t.Fatalf("a carona de referência deveria passar: %v", err)
	}

	casos := []struct {
		nome      string
		estragar  func(*CaronaBusca)
		sentinela error
	}{
		{"cidade fora do catálogo", func(c *CaronaBusca) { c.Rota[1] = "Macapá" }, ErrCidadeDesconhecida},
		{"uma parada só", func(c *CaronaBusca) { c.Rota = c.Rota[:1] }, ErrRotaInvalida},
		{"horários a menos", func(c *CaronaBusca) { c.Horarios = c.Horarios[:1] }, ErrRotaInvalida},
		{"horários fora de ordem", func(c *CaronaBusca) { c.Horarios[1] = as(8, 0) }, ErrRotaInvalida},
		{"preços a mais", func(c *CaronaBusca) { c.PrecoTrecho = []int{1, 2} }, ErrRotaInvalida},
		{"livres a mais", func(c *CaronaBusca) { c.Livres = []int{1, 2} }, ErrRotaInvalida},
		{"livres acima dos assentos", func(c *CaronaBusca) { c.Livres[0] = 4 }, ErrAssentosInvalidos},
		{"livres negativo", func(c *CaronaBusca) { c.Livres[0] = -1 }, ErrAssentosInvalidos},
		{"assentos zero", func(c *CaronaBusca) { c.Assentos = 0 }, ErrAssentosInvalidos},
		{"ID sem prefixo", func(c *CaronaBusca) { c.ID = "car-1" }, ErrIDSemEmpresa},
		{"prefixo de outra empresa", func(c *CaronaBusca) { c.ID = "vaijunto:car-1" }, ErrIDSemEmpresa},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			c := valida()
			caso.estragar(&c)

			err := c.Validar()
			if err == nil {
				t.Fatalf("Validar() = nil, want %v", caso.sentinela)
			}
			if !errors.Is(err, caso.sentinela) {
				t.Errorf("Validar() = %v, want %v", err, caso.sentinela)
			}
		})
	}
}

// BuscarItinerariosEm não depende de qual empresa hospeda cada carona: é a
// equivalência federada da seção 6, no menor caso que a exibe.
//
// As duas caronas são a baldeação do enunciado. Hospedadas na mesma empresa ou
// em empresas diferentes, o itinerário é o mesmo, com os mesmos índices e o
// mesmo preço — só o prefixo dos IDs muda.
func TestBuscarItinerariosEm_NaoDependeDeQuemHospeda(t *testing.T) {
	monta := func(empresaA, empresaB string) []CaronaBusca {
		return []CaronaBusca{
			{
				ID: empresaA + ":car-1", Empresa: empresaA, MotoristaNome: "João Silva",
				Rota:     []string{"Salvador", "Jequié"},
				Horarios: []time.Time{as(6, 0), as(10, 45)},
				Assentos: 3, PrecoTrecho: []int{6000}, Livres: []int{3},
			},
			{
				ID: empresaB + ":car-2", Empresa: empresaB, MotoristaNome: "Carlos Lima",
				Rota:     []string{"Jequié", "Vitória da Conquista"},
				Horarios: []time.Time{as(12, 15), as(14, 30)},
				Assentos: 2, PrecoTrecho: []int{4000}, Livres: []int{2},
			},
		}
	}

	juntas, err := BuscarItinerariosEm(monta("vaijunto", "vaijunto"), "Salvador", "Vitória da Conquista", as(0, 0))
	if err != nil {
		t.Fatalf("mesma empresa: %v", err)
	}
	// As empresas são escolhidas para que a ordem alfabética dos IDs globais se
	// invertesse em relação à dos sufixos, se ela importasse.
	separadas, err := BuscarItinerariosEm(monta("vaijunto", "rotacapixaba"), "Salvador", "Vitória da Conquista", as(0, 0))
	if err != nil {
		t.Fatalf("empresas diferentes: %v", err)
	}

	if len(juntas) != 1 || len(separadas) != 1 {
		t.Fatalf("itinerários = %d e %d, want 1 e 1", len(juntas), len(separadas))
	}
	a, b := juntas[0], separadas[0]
	if a.PrecoTotalCentavos != b.PrecoTotalCentavos || !a.Chegada.Equal(b.Chegada) || len(a.Pernas) != len(b.Pernas) {
		t.Fatalf("o itinerário mudou com a distribuição:\n mesma empresa: %+v\n separadas:     %+v", a, b)
	}
	for i := range a.Pernas {
		if a.Pernas[i].De != b.Pernas[i].De || a.Pernas[i].Ate != b.Pernas[i].Ate {
			t.Errorf("perna %d: índices %d-%d contra %d-%d", i, a.Pernas[i].De, a.Pernas[i].Ate, b.Pernas[i].De, b.Pernas[i].Ate)
		}
	}
	// E a empresa de cada perna acompanha quem hospeda.
	if separadas[0].Pernas[0].Empresa != "vaijunto" || separadas[0].Pernas[1].Empresa != "rotacapixaba" {
		t.Errorf("empresas das pernas = %q e %q, want vaijunto e rotacapixaba",
			separadas[0].Pernas[0].Empresa, separadas[0].Pernas[1].Empresa)
	}
}

// idsDe reduz as caronas aos identificadores, na ordem em que vieram.
func idsDe(caronas []CaronaBusca) []string {
	ids := make([]string, len(caronas))
	for i, c := range caronas {
		ids[i] = c.ID
	}
	return ids
}
