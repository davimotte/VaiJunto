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

	// Códigos do P2 (seção 5.6 do PROJETO_P2.md).
	//
	// CodigoReservaEmAndamento é a D35: o passageiro já tem uma reserva em
	// andamento. CodigoEmpresaIndisponivel é a queda de um dono detectada por
	// timeout (D30): nenhum assento fica preso, o coordenador aborta.
	CodigoReservaEmAndamento  = "RESERVA_EM_ANDAMENTO"
	CodigoEmpresaIndisponivel = "EMPRESA_INDISPONIVEL"

	// CodigoOperacaoIndisponivel é **provisório**, da fase 5 à 7: os dois
	// cancelamentos que dependem do outbox da D39 ainda não estão ligados.
	// Sai na fase 8.
	CodigoOperacaoIndisponivel = "OPERACAO_INDISPONIVEL"
)
