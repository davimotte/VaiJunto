package federacao

import (
	"net/http"
	"time"

	"vaijunto/internal/dominio"
)

// JANELA_MAXIMA_ACEITA é o teto de janela que GET /federacao/caronas aceita
// (API_CONSORCIO.md, seção 4.2).
//
// A busca pede dominio.JANELA_BUSCA, 48 h, e o teto aqui é maior de propósito.
// São dois valores com donos diferentes: a janela é a decisão de quem pergunta
// (D40), e o teto é a defesa de quem responde. Igualá-los faria um par que
// rodasse uma versão com janela um minuto mais larga receber 400 em vez de
// responder, e a busca o veria como indisponível (D30) — uma divergência de
// versão virando empresa fora do ar.
//
// O teto existe para que uma janela absurda não faça este servidor percorrer e
// serializar o estado inteiro a pedido de um par com defeito.
const JANELA_MAXIMA_ACEITA = 72 * time.Hour

// CaronaFederada é uma carona como ela trafega entre servidores
// (API_CONSORCIO.md, seção 3.4).
//
// É o tipo de fio do dominio.CaronaBusca, e existe separado dele porque as tags
// JSON são do contrato da API: o domínio não conhece a rede (regra 8). Esta é a
// única forma em que uma empresa vê dado de outra, e ela não traz MotoristaID
// nem passageiro nenhum — de outras empresas só chegam os dados necessários à
// busca (D32).
type CaronaFederada struct {
	ID             string      `json:"id"`
	Empresa        string      `json:"empresa"`
	MotoristaNome  string      `json:"motorista_nome"`
	Rota           []string    `json:"rota"`
	Horarios       []time.Time `json:"horarios"`
	PrecosCentavos []int       `json:"precos_centavos"`
	Assentos       int         `json:"assentos"`
	Livres         []int       `json:"livres"`
}

// RespostaCaronas é o corpo de GET /federacao/caronas (seção 4.2 da API).
//
// Empresa vem no corpo, e não só no cabeçalho X-VaiJunto-Empresa, porque é
// contra ela que quem recebe confere o campo empresa de cada carona: um par não
// fala pelas caronas de outro.
type RespostaCaronas struct {
	Empresa string           `json:"empresa"`
	Caronas []CaronaFederada `json:"caronas"`
}

// tratarCaronas responde GET /federacao/caronas: as caronas não canceladas
// desta empresa com alguma parada em [desde, ate) (D26, D40).
//
// Leitura sem efeito nenhum: nada é gravado, nada fica reservado. Pode ser
// repetida à vontade, embora quem chama não repita (seção 1.3 da API) — a
// empresa que não responde entra como indisponível e a busca segue parcial.
func tratarCaronas(w http.ResponseWriter, r *http.Request, estado *dominio.Estado, empresa string) {
	desde, ate, erro := janelaPedida(r)
	if erro != nil {
		responderErro(w, *erro)
		return
	}

	locais := estado.CaronasNaJanela(desde, ate)
	resposta := RespostaCaronas{Empresa: empresa, Caronas: make([]CaronaFederada, 0, len(locais))}
	for _, c := range locais {
		resposta.Caronas = append(resposta.Caronas, CaronaFederada{
			ID:             c.ID,
			Empresa:        c.Empresa,
			MotoristaNome:  c.MotoristaNome,
			Rota:           c.Rota,
			Horarios:       c.Horarios,
			PrecosCentavos: c.PrecoTrecho,
			Assentos:       c.Assentos,
			Livres:         c.Livres,
		})
	}
	responderJSON(w, http.StatusOK, resposta)
}

// janelaPedida lê e valida desde e ate da query (seção 4.2 da API).
//
// Toda recusa é 400 PARAMETRO_INVALIDO, com mensagem dizendo qual parâmetro
// errou: é erro de quem chama, e nenhuma repetição o conserta. Ausente,
// malformado, desde >= ate e janela acima do teto caem todos aqui.
//
// Os instantes chegam em RFC 3339, com deslocamento explícito, e são usados como
// vieram: nenhuma conversão para o fuso local, que no contêiner Alpine é UTC
// (PROJETO.md, seção 10.1). A comparação é por Before, nunca por ==.
func janelaPedida(r *http.Request) (desde, ate time.Time, erro *erroHTTP) {
	q := r.URL.Query()

	desde, erro = instanteDaQuery(q.Get("desde"), "desde")
	if erro != nil {
		return time.Time{}, time.Time{}, erro
	}
	ate, erro = instanteDaQuery(q.Get("ate"), "ate")
	if erro != nil {
		return time.Time{}, time.Time{}, erro
	}

	if !desde.Before(ate) {
		return time.Time{}, time.Time{}, &erroHTTP{http.StatusBadRequest, codigoParametroInvalido,
			"O parâmetro desde precisa ser anterior a ate."}
	}
	if ate.Sub(desde) > JANELA_MAXIMA_ACEITA {
		return time.Time{}, time.Time{}, &erroHTTP{http.StatusBadRequest, codigoParametroInvalido,
			"A janela pedida passa do máximo de " + JANELA_MAXIMA_ACEITA.String() + "."}
	}
	return desde, ate, nil
}

// instanteDaQuery decodifica um instante RFC 3339 da query.
//
// Parâmetro vazio e parâmetro malformado dão a mesma recusa com mensagens
// diferentes: os dois são erro de quem chama, mas a mensagem é o que permite
// consertá-lo sem ler o nosso código.
func instanteDaQuery(bruto, nome string) (time.Time, *erroHTTP) {
	if bruto == "" {
		return time.Time{}, &erroHTTP{http.StatusBadRequest, codigoParametroInvalido,
			"Informe o parâmetro " + nome + "."}
	}
	instante, err := time.Parse(time.RFC3339, bruto)
	if err != nil {
		return time.Time{}, &erroHTTP{http.StatusBadRequest, codigoParametroInvalido,
			"O parâmetro " + nome + " precisa ser um instante RFC 3339 (ex.: 2026-11-05T00:00:00-03:00)."}
	}
	return instante, nil
}
