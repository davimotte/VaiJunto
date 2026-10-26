package federacao

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vaijunto/internal/dominio"
)

// fuso é o fuso das cidades, usado nos instantes dos testes deste arquivo.
// Nunca time.Local: o pacote roda com TZ indefinido, como no contêiner Alpine.
func fuso() *time.Location { return dominio.FusoDasCidades() }

// instante monta um instante do cenário da semente (05 ou 06/11/2026).
func instante(dia, hora, minuto int) time.Time {
	return time.Date(2026, 11, dia, hora, minuto, 0, 0, fuso())
}

// parComUmaCarona devolve o estado de um par com uma única carona publicada,
// pelos mesmos caminhos do boot: a carona entra pela carga da semente, com ID
// já prefixado (D27).
func parComUmaCarona(t *testing.T, empresa string) *dominio.Estado {
	t.Helper()

	usuarios := `[{"usuario":"rafael","senha":"1234","nome":"Rafael Mendonça Pires","perfil":"MOTORISTA"}]`
	caronas := `[{"id":"` + empresa + `:car-1","motorista":"rafael",
		"rota":["Vitória da Conquista","Vitória","Cachoeiro de Itapemirim"],
		"horarios":["2026-11-05T14:00:00-03:00","2026-11-06T04:00:00-03:00","2026-11-06T06:30:00-03:00"],
		"assentos":3,"precos_centavos":[18000,3500]}]`

	estado, err := dominio.CarregarEstado(empresa, []byte(usuarios), []byte(caronas))
	if err != nil {
		t.Fatalf("carga da semente do par: %v", err)
	}
	return estado
}

// pedirCaronas roda GET /federacao/caronas pelo handler completo, com os
// cabeçalhos da seção 1.1 da API.
func pedirCaronas(cfg Config, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/federacao/caronas?"+query, nil)
	return pedir(cfg, comCabecalhos(req, "rotacapixaba", 1))
}

// O recurso devolve a carona da empresa na forma da seção 3.4 da API, com o
// nome do motorista já resolvido e livres iguais aos assentos (D40).
func TestCaronas_FormaDaResposta(t *testing.T) {
	cfg := configDeTeste()
	cfg.Estado = parComUmaCarona(t, "vaijunto")

	rec := pedirCaronas(cfg, "desde=2026-11-05T00:00:00-03:00&ate=2026-11-07T00:00:00-03:00")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var resposta RespostaCaronas
	if err := json.Unmarshal(rec.Body.Bytes(), &resposta); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if resposta.Empresa != "vaijunto" {
		t.Errorf("empresa = %q, want vaijunto", resposta.Empresa)
	}
	if len(resposta.Caronas) != 1 {
		t.Fatalf("caronas = %d, want 1", len(resposta.Caronas))
	}

	c := resposta.Caronas[0]
	if c.ID != "vaijunto:car-1" || c.Empresa != "vaijunto" {
		t.Errorf("id/empresa = %q/%q, want vaijunto:car-1/vaijunto", c.ID, c.Empresa)
	}
	// O nome, e não o login: quem resolve o motorista é o dono (D32).
	if c.MotoristaNome != "Rafael Mendonça Pires" {
		t.Errorf("motorista_nome = %q, want Rafael Mendonça Pires", c.MotoristaNome)
	}
	if c.Assentos != 3 {
		t.Errorf("assentos = %d, want 3", c.Assentos)
	}
	if len(c.Livres) != 2 || c.Livres[0] != 3 || c.Livres[1] != 3 {
		t.Errorf("livres = %v, want [3 3]", c.Livres)
	}
	if len(c.Rota) != 3 || len(c.Horarios) != 3 || len(c.PrecosCentavos) != 2 {
		t.Errorf("rota/horarios/precos = %d/%d/%d, want 3/3/2", len(c.Rota), len(c.Horarios), len(c.PrecosCentavos))
	}

	// O que sai daqui tem de passar pela validação de quem recebe (passo 5):
	// se não passasse, a empresa descartaria a própria carona ao buscar.
	recebida := dominio.CaronaBusca{
		ID: c.ID, Empresa: c.Empresa, MotoristaNome: c.MotoristaNome,
		Rota: c.Rota, Horarios: c.Horarios, Assentos: c.Assentos,
		PrecoTrecho: c.PrecosCentavos, Livres: c.Livres,
	}
	if err := recebida.Validar(); err != nil {
		t.Errorf("a carona que este servidor devolve não passa na validação do passo 5: %v", err)
	}
}

// O critério é "alguma parada em [desde, ate)" (D40), e não a partida: é o que
// mantém na busca a carona em que o passageiro embarca no meio da rota.
func TestCaronas_AlgumaParadaNaJanela(t *testing.T) {
	cfg := configDeTeste()
	cfg.Estado = parComUmaCarona(t, "vaijunto")

	casos := []struct {
		nome   string
		query  string
		quer   int
		motivo string
	}{
		{
			"janela só do segundo dia", "desde=2026-11-06T00:00:00-03:00&ate=2026-11-06T12:00:00-03:00", 1,
			"a carona parte em 05/11, mas tem duas paradas em 06/11",
		},
		{
			"janela antes da primeira parada", "desde=2026-11-04T00:00:00-03:00&ate=2026-11-05T00:00:00-03:00", 0,
			"nenhuma parada cai antes de 05/11",
		},
		{
			"ate é exclusivo", "desde=2026-11-04T00:00:00-03:00&ate=2026-11-05T14:00:00-03:00", 0,
			"a partida às 14:00 não pertence a [.., 14:00)",
		},
		{
			"desde é inclusivo", "desde=2026-11-05T14:00:00-03:00&ate=2026-11-05T15:00:00-03:00", 1,
			"a partida às 14:00 pertence a [14:00, ..)",
		},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			rec := pedirCaronas(cfg, caso.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			var resposta RespostaCaronas
			if err := json.Unmarshal(rec.Body.Bytes(), &resposta); err != nil {
				t.Fatalf("decodificar: %v", err)
			}
			if len(resposta.Caronas) != caso.quer {
				t.Errorf("caronas = %d, want %d: %s", len(resposta.Caronas), caso.quer, caso.motivo)
			}
		})
	}
}

// Carona cancelada não sai do dono (D29): quem sabe o que está cancelado é ele.
func TestCaronas_CanceladaNaoSai(t *testing.T) {
	cfg := configDeTeste()
	estado := parComUmaCarona(t, "vaijunto")
	cfg.Estado = estado

	// O cancelamento usa o relógio real, e a carona é de 2026: o prazo da D14
	// está cumprido enquanto a data do cenário não passar.
	if _, _, err := estado.CancelarCarona("vaijunto:car-1", "rafael", time.Now().In(fuso())); err != nil {
		t.Fatalf("CancelarCarona: %v", err)
	}

	rec := pedirCaronas(cfg, "desde=2026-11-05T00:00:00-03:00&ate=2026-11-07T00:00:00-03:00")
	var resposta RespostaCaronas
	if err := json.Unmarshal(rec.Body.Bytes(), &resposta); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if len(resposta.Caronas) != 0 {
		t.Errorf("caronas = %d, want 0: carona cancelada não pode trafegar", len(resposta.Caronas))
	}
}

// Toda recusa de parâmetro é 400 PARAMETRO_INVALIDO (seção 4.2 da API): é erro
// de quem chama, e nenhuma repetição o conserta.
func TestCaronas_ParametroInvalido(t *testing.T) {
	cfg := configDeTeste()
	cfg.Estado = parComUmaCarona(t, "vaijunto")

	casos := map[string]string{
		"sem desde":            "ate=2026-11-07T00:00:00-03:00",
		"sem ate":              "desde=2026-11-05T00:00:00-03:00",
		"sem nenhum":           "",
		"desde malformado":     "desde=2026-11-05&ate=2026-11-07T00:00:00-03:00",
		"ate malformado":       "desde=2026-11-05T00:00:00-03:00&ate=ontem",
		"desde igual a ate":    "desde=2026-11-05T00:00:00-03:00&ate=2026-11-05T00:00:00-03:00",
		"desde depois de ate":  "desde=2026-11-07T00:00:00-03:00&ate=2026-11-05T00:00:00-03:00",
		"janela acima do teto": "desde=2026-11-05T00:00:00-03:00&ate=2026-11-08T00:00:01-03:00",
	}
	for nome, query := range casos {
		t.Run(nome, func(t *testing.T) {
			rec := pedirCaronas(cfg, query)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if got := codigoDoErro(t, rec); got != codigoParametroInvalido {
				t.Errorf("codigo = %q, want %q", got, codigoParametroInvalido)
			}
		})
	}
}

// A janela de 48 h da busca (D40) cabe no teto de 72 h do validador (seção
// 4.2): se não cabesse, todo par responderia 400 a toda busca.
func TestCaronas_JanelaDaBuscaCabeNoTeto(t *testing.T) {
	if dominio.JANELA_BUSCA > JANELA_MAXIMA_ACEITA {
		t.Fatalf("JANELA_BUSCA (%s) passa do teto aceito (%s)", dominio.JANELA_BUSCA, JANELA_MAXIMA_ACEITA)
	}
}

// Ida e volta pelo cliente: a query é montada e codificada aqui, e o corpo
// volta decodificado. É o caminho que o passo 4 da busca percorre.
func TestCliente_CaronasIdaEVolta(t *testing.T) {
	cfgPar := configDoParCom("rotacapixaba", parComUmaCarona(t, "rotacapixaba"))
	cliente, _ := clienteApontadoPara(t, "rotacapixaba", NovoHandler(cfgPar), time.Second)

	desde, ate := dominio.JanelaDaBusca(instante(5, 0, 0))
	resposta, err := cliente.Caronas(context.Background(), "rotacapixaba", desde, ate)
	if err != nil {
		t.Fatalf("Caronas: %v", err)
	}
	if resposta.Empresa != "rotacapixaba" {
		t.Errorf("empresa = %q, want rotacapixaba", resposta.Empresa)
	}
	if len(resposta.Caronas) != 1 || resposta.Caronas[0].ID != "rotacapixaba:car-1" {
		t.Fatalf("caronas = %+v, want uma rotacapixaba:car-1", resposta.Caronas)
	}
	// Os instantes atravessaram a query e voltaram como o mesmo ponto no tempo.
	if got := resposta.Caronas[0].Horarios[0]; !got.Equal(instante(5, 14, 0)) {
		t.Errorf("primeira partida = %s, want %s", got, instante(5, 14, 0))
	}
}

// Pares sai ordenado, porque é dele que a busca monta empresas_indisponiveis.
func TestCliente_ParesSaiOrdenado(t *testing.T) {
	cfg := configDeTeste()
	cfg.Pares = map[string]string{
		"rotafluminense": "http://rotafluminense.invalido:8080",
		"rotacapixaba":   "http://rotacapixaba.invalido:8080",
	}

	got := NovoCliente(cfg).Pares()
	want := []string{"rotacapixaba", "rotafluminense"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Pares() = %v, want %v", got, want)
	}
}
