package protocolo

// Status possíveis do campo "status" da Resposta (seção 2.2).
const (
	StatusOK   = "OK"
	StatusErro = "ERRO"
)

// Tipos de operação aceitos no campo "tipo" da Requisicao (seção 5).
const (
	TipoPing                 = "PING"
	TipoLogin                = "LOGIN"
	TipoLogout               = "LOGOUT"
	TipoPublicarCarona       = "PUBLICAR_CARONA"
	TipoListarMinhasCaronas  = "LISTAR_MINHAS_CARONAS"
	TipoDetalharCarona       = "DETALHAR_CARONA"
	TipoCancelarCarona       = "CANCELAR_CARONA"
	TipoBuscarItinerarios    = "BUSCAR_ITINERARIOS"
	TipoReservar             = "RESERVAR"
	TipoListarMinhasReservas = "LISTAR_MINHAS_RESERVAS"
	TipoCancelarReserva      = "CANCELAR_RESERVA"
)

// Perfis de usuário (seção 5.2).
const (
	PerfilMotorista  = "MOTORISTA"
	PerfilPassageiro = "PASSAGEIRO"
)
