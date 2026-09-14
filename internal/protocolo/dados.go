package protocolo

import "time"

// Structs do campo "dados" de cada operação (PROTOCOL.md, seção 5).
//
// Convenções de tipo (seção 3): instantes são time.Time (serializados em
// RFC 3339 pelo encoding/json), a data da busca é string "AAAA-MM-DD", e
// dinheiro é sempre int em centavos.

// PingResposta — seção 5.1.
type PingResposta struct {
	ServidorEm time.Time `json:"servidor_em"`
}

// LoginRequisicao e LoginResposta — seção 5.2.
type LoginRequisicao struct {
	Usuario string `json:"usuario"`
	Senha   string `json:"senha"`
}

type LoginResposta struct {
	Usuario string `json:"usuario"`
	Nome    string `json:"nome"`
	Perfil  string `json:"perfil"`
}

// Parada é um ponto da rota de PUBLICAR_CARONA: a cidade e o instante em que
// o carro passa por ela. Há um único horário por parada, sem espera no local
// (D09).
//
// Cidade e horário andam juntos num objeto, e não em duas listas paralelas,
// para que "mais cidades que horários" nem seja representável na requisição.
type Parada struct {
	Cidade  string    `json:"cidade"`
	Horario time.Time `json:"horario"`
}

// PublicarCaronaRequisicao e PublicarCaronaResposta — seção 5.4.
//
// PrecosCentavos[t] é o preço do trecho entre Paradas[t] e Paradas[t+1].
type PublicarCaronaRequisicao struct {
	Paradas        []Parada `json:"paradas"`
	Assentos       int      `json:"assentos"`
	PrecosCentavos []int    `json:"precos_centavos"`
}

type PublicarCaronaResposta struct {
	CaronaID string      `json:"carona_id"`
	Rota     []string    `json:"rota"`
	Horarios []time.Time `json:"horarios"`
}

// ListarMinhasCaronasRequisicao e ListarMinhasCaronasResposta — seção 5.5.
type ListarMinhasCaronasRequisicao struct {
	IncluirCanceladas bool `json:"incluir_canceladas"`
}

type TrechoResumo struct {
	Indice        int    `json:"indice"`
	Origem        string `json:"origem"`
	Destino       string `json:"destino"`
	PrecoCentavos int    `json:"preco_centavos"`
	Livres        int    `json:"livres"`
}

type CaronaResumo struct {
	CaronaID  string         `json:"carona_id"`
	Rota      []string       `json:"rota"`
	Horarios  []time.Time    `json:"horarios"`
	Assentos  int            `json:"assentos"`
	Cancelada bool           `json:"cancelada"`
	Trechos   []TrechoResumo `json:"trechos"`
}

type ListarMinhasCaronasResposta struct {
	Caronas []CaronaResumo `json:"caronas"`
}

// DetalharCaronaRequisicao e DetalharCaronaResposta — seção 5.6.
type DetalharCaronaRequisicao struct {
	CaronaID string `json:"carona_id"`
}

type PassageiroTrecho struct {
	ReservaID string `json:"reserva_id"`
	Usuario   string `json:"usuario"`
	Nome      string `json:"nome"`
}

type TrechoDetalhado struct {
	Indice      int                `json:"indice"`
	Origem      string             `json:"origem"`
	Destino     string             `json:"destino"`
	Livres      int                `json:"livres"`
	Passageiros []PassageiroTrecho `json:"passageiros"`
}

type DetalharCaronaResposta struct {
	CaronaID string            `json:"carona_id"`
	Trechos  []TrechoDetalhado `json:"trechos"`
}

// CancelarCaronaRequisicao e CancelarCaronaResposta — seção 5.7.
type CancelarCaronaRequisicao struct {
	CaronaID string `json:"carona_id"`
}

type CancelarCaronaResposta struct {
	CaronaID           string `json:"carona_id"`
	ReservasCanceladas int    `json:"reservas_canceladas"`
}

// BuscarItinerariosRequisicao e BuscarItinerariosResposta — seção 5.8.
type BuscarItinerariosRequisicao struct {
	Origem  string `json:"origem"`
	Destino string `json:"destino"`
	Data    string `json:"data"`
}

type TrechoItinerario struct {
	CaronaID      string    `json:"carona_id"`
	Motorista     string    `json:"motorista"`
	De            int       `json:"de"`
	Ate           int       `json:"ate"`
	Origem        string    `json:"origem"`
	Destino       string    `json:"destino"`
	Partida       time.Time `json:"partida"`
	Chegada       time.Time `json:"chegada"`
	PrecoCentavos int       `json:"preco_centavos"`
}

type Itinerario struct {
	PrecoTotalCentavos int                `json:"preco_total_centavos"`
	Partida            time.Time          `json:"partida"`
	Chegada            time.Time          `json:"chegada"`
	Baldeacoes         int                `json:"baldeacoes"`
	Trechos            []TrechoItinerario `json:"trechos"`
}

type BuscarItinerariosResposta struct {
	Itinerarios []Itinerario `json:"itinerarios"`
}

// ReservarRequisicao e ReservarResposta — seção 5.9.
type ItemReserva struct {
	CaronaID string `json:"carona_id"`
	De       int    `json:"de"`
	Ate      int    `json:"ate"`
}

type ReservarRequisicao struct {
	Trechos []ItemReserva `json:"trechos"`
}

type ReservarResposta struct {
	ReservaID          string `json:"reserva_id"`
	PrecoTotalCentavos int    `json:"preco_total_centavos"`
}

// SemAssentoDados é o "dados" da resposta de erro CodigoSemAssento.
type SemAssentoDados struct {
	CaronaID     string `json:"carona_id"`
	IndiceTrecho int    `json:"indice_trecho"`
}

// ConflitoHorarioDados é o "dados" da resposta de erro CodigoConflitoHorario.
type ConflitoHorarioDados struct {
	ReservaID string `json:"reserva_id"`
}

// ListarMinhasReservasRequisicao e ListarMinhasReservasResposta — seção 5.10.
type ListarMinhasReservasRequisicao struct {
	IncluirCanceladas bool `json:"incluir_canceladas"`
}

type TrechoReserva struct {
	CaronaID      string    `json:"carona_id"`
	Motorista     string    `json:"motorista"`
	Origem        string    `json:"origem"`
	Destino       string    `json:"destino"`
	Partida       time.Time `json:"partida"`
	Chegada       time.Time `json:"chegada"`
	PrecoCentavos int       `json:"preco_centavos"`
}

type ReservaResumo struct {
	ReservaID          string          `json:"reserva_id"`
	Ativa              bool            `json:"ativa"`
	CriadaEm           time.Time       `json:"criada_em"`
	PrecoTotalCentavos int             `json:"preco_total_centavos"`
	Partida            time.Time       `json:"partida"`
	Chegada            time.Time       `json:"chegada"`
	Trechos            []TrechoReserva `json:"trechos"`
}

type ListarMinhasReservasResposta struct {
	Reservas []ReservaResumo `json:"reservas"`
}

// CancelarReservaRequisicao e CancelarReservaResposta — seção 5.11.
type CancelarReservaRequisicao struct {
	ReservaID string `json:"reserva_id"`
}

type CancelarReservaResposta struct {
	ReservaID string `json:"reserva_id"`
}

// PrazoCancelamentoExpiradoDados é o "dados" da resposta de erro
// CodigoPrazoCancelamentoExpirado.
type PrazoCancelamentoExpiradoDados struct {
	Partida time.Time `json:"partida"`
}
