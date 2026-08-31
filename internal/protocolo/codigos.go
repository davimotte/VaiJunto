package protocolo

// Códigos de erro do campo "codigo" da Resposta (PROTOCOL.md, seção 6).
const (
	CodigoJSONInvalido              = "JSON_INVALIDO"
	CodigoEnvelopeInvalido          = "ENVELOPE_INVALIDO"
	CodigoTipoDesconhecido          = "TIPO_DESCONHECIDO"
	CodigoCampoInvalido             = "CAMPO_INVALIDO"
	CodigoNaoAutenticado            = "NAO_AUTENTICADO"
	CodigoJaAutenticado             = "JA_AUTENTICADO"
	CodigoCredenciaisInvalidas      = "CREDENCIAIS_INVALIDAS"
	CodigoPerfilIncorreto           = "PERFIL_INCORRETO"
	CodigoCidadeDesconhecida        = "CIDADE_DESCONHECIDA"
	CodigoRotaInvalida              = "ROTA_INVALIDA"
	CodigoPartidaInvalida           = "PARTIDA_INVALIDA"
	CodigoCaronaNaoEncontrada       = "CARONA_NAO_ENCONTRADA"
	CodigoCaronaCancelada           = "CARONA_CANCELADA"
	CodigoNaoEDono                  = "NAO_E_DONO"
	CodigoItinerarioInvalido        = "ITINERARIO_INVALIDO"
	CodigoSemAssento                = "SEM_ASSENTO"
	CodigoConflitoHorario           = "CONFLITO_HORARIO"
	CodigoReservaNaoEncontrada      = "RESERVA_NAO_ENCONTRADA"
	CodigoReservaJaCancelada        = "RESERVA_JA_CANCELADA"
	CodigoPrazoCancelamentoExpirado = "PRAZO_CANCELAMENTO_EXPIRADO"
	CodigoErroInterno               = "ERRO_INTERNO"
)
