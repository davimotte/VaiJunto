package servidor

import (
	"encoding/json"
	"errors"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// Tabela única de tradução de erro de domínio para código do protocolo
// (PROJETO.md, seção 5.3).
//
// internal/dominio não importa internal/protocolo — o domínio não conhece a
// rede —, então os sentinelas não carregam código nem texto de protocolo. É
// aqui, e só aqui, que os dois vocabulários se encontram. Concentrar isso em
// uma tabela, em vez de espalhar por dentro dos handlers, é o que torna
// possível o teste que exige entrada para todo sentinela exportado.
//
// A mensagem é escrita para ser exibida direto no CLI, sem que o cliente
// precise conhecer o código.
type erroTraduzido struct {
	Codigo   string
	Mensagem string
}

var traducaoErros = map[error]erroTraduzido{
	dominio.ErrCidadeDesconhecida:   {protocolo.CodigoCidadeDesconhecida, "Cidade não atendida pelo sistema."},
	dominio.ErrRotaInvalida:         {protocolo.CodigoRotaInvalida, "Rota inválida: são necessárias ao menos duas cidades diferentes, horários crescentes e um preço por trecho."},
	dominio.ErrPartidaInvalida:      {protocolo.CodigoPartidaInvalida, "A partida precisa ser um instante futuro."},
	dominio.ErrAssentosInvalidos:    {protocolo.CodigoCampoInvalido, "A carona precisa ter pelo menos um assento."},
	dominio.ErrPrecoInvalido:        {protocolo.CodigoCampoInvalido, "O preço de um trecho não pode ser negativo."},
	dominio.ErrCredenciaisInvalidas: {protocolo.CodigoCredenciaisInvalidas, "Usuário ou senha incorretos."},
	dominio.ErrCaronaNaoEncontrada:  {protocolo.CodigoCaronaNaoEncontrada, "Carona não encontrada."},
	// A mensagem é neutra porque o mesmo sentinela cobre carona e reserva: o
	// domínio trata "pertence a outro usuário" como uma única regra, e inventar
	// um segundo sentinela só para variar o texto duplicaria o conceito.
	dominio.ErrNaoEDono:        {protocolo.CodigoNaoEDono, "Este recurso pertence a outro usuário."},
	dominio.ErrCaronaCancelada: {protocolo.CodigoCaronaCancelada, "Esta carona foi cancelada."},
	// Os dois erros do passo 1 da seção 7 do PROJETO.md se separam por natureza:
	// De, Ate e a lista vazia são valor fora de faixa (CAMPO_INVALIDO), enquanto
	// carona repetida e falta de encadeamento são propriedades do itinerário
	// como um todo (ITINERARIO_INVALIDO).
	dominio.ErrItemInvalido:              {protocolo.CodigoCampoInvalido, "Trecho inválido: verifique carona_id, de e ate."},
	dominio.ErrItinerarioInvalido:        {protocolo.CodigoItinerarioInvalido, "Os trechos escolhidos não formam um itinerário válido."},
	dominio.ErrSemAssento:                {protocolo.CodigoSemAssento, "Não há mais assento livre em um dos trechos."},
	dominio.ErrConflitoHorario:           {protocolo.CodigoConflitoHorario, "Você já tem uma reserva ativa nesse período."},
	dominio.ErrReservaNaoEncontrada:      {protocolo.CodigoReservaNaoEncontrada, "Reserva não encontrada."},
	dominio.ErrReservaJaCancelada:        {protocolo.CodigoReservaJaCancelada, "Esta reserva já foi cancelada."},
	dominio.ErrPrazoCancelamentoExpirado: {protocolo.CodigoPrazoCancelamentoExpirado, "Fora do prazo de cancelamento."},
	dominio.ErrGeracaoDeID:               {protocolo.CodigoErroInterno, "Falha interna ao gerar o identificador."},
}

// respostaDeErroDeDominio monta a resposta de erro correspondente ao erro
// devolvido pelo domínio.
//
// A comparação é por errors.Is, e não por igualdade: o domínio embrulha os
// sentinelas com %w para acrescentar contexto à mensagem interna (qual cidade,
// qual carona), e o embrulho precisa continuar sendo reconhecido.
//
// Sentinela sem entrada na tabela cai em ERRO_INTERNO: um código errado é
// pior que um código genérico, porque o cliente tomaria decisão em cima dele.
// O teste de cobertura de erros_test.go existe para que esse caminho nunca
// seja exercido por um erro previsto.
func respostaDeErroDeDominio(id string, err error) protocolo.Resposta {
	if detalhada := comDetalhe(id, err); detalhada != nil {
		return *detalhada
	}
	for sentinela, traducao := range traducaoErros {
		if errors.Is(err, sentinela) {
			return respostaErro(id, traducao.Codigo, traducao.Mensagem)
		}
	}
	return respostaErro(id, protocolo.CodigoErroInterno, "Falha inesperada ao processar a operação.")
}

// comDetalhe monta a resposta dos três erros que o PROTOCOL.md manda
// acompanhar de dados: SEM_ASSENTO diz qual trecho esgotou, CONFLITO_HORARIO
// diz qual reserva ocupa o período (seção 5.9), e PRAZO_CANCELAMENTO_EXPIRADO
// diz de que partida o prazo foi contado (seção 5.11). Devolve nil quando o
// erro não é nenhum deles, e aí a tabela genérica resolve.
//
// O código continua vindo da tabela, por errors.Is dentro de respostaErroDe:
// esta função só acrescenta o "dados" e refina a mensagem. Manter o código em
// um lugar só é o que preserva a propriedade da seção 5.3 — existe **uma**
// tradução de sentinela para código, e o teste de cobertura continua valendo.
//
// A extração é por errors.As, e não por type assertion, para atravessar
// eventuais embrulhos com %w da mesma forma que errors.Is.
func comDetalhe(id string, err error) *protocolo.Resposta {
	var semAssento *dominio.ErroSemAssento
	if errors.As(err, &semAssento) {
		resp := respostaErroDe(id, dominio.ErrSemAssento,
			"Assento esgotado no trecho "+semAssento.Origem+" → "+semAssento.Destino+".",
			protocolo.SemAssentoDados{
				CaronaID:     semAssento.CaronaID,
				IndiceTrecho: semAssento.IndiceTrecho,
			})
		return &resp
	}

	var conflito *dominio.ErroConflitoHorario
	if errors.As(err, &conflito) {
		resp := respostaErroDe(id, dominio.ErrConflitoHorario,
			"Você já tem a reserva "+conflito.ReservaID+" nesse período.",
			protocolo.ConflitoHorarioDados{ReservaID: conflito.ReservaID})
		return &resp
	}

	var prazo *dominio.ErroPrazoCancelamento
	if errors.As(err, &prazo) {
		resp := respostaErroDe(id, dominio.ErrPrazoCancelamentoExpirado,
			"Fora do prazo de cancelamento.",
			protocolo.PrazoCancelamentoExpiradoDados{Partida: prazo.Partida})
		return &resp
	}

	return nil
}

// respostaErroDe monta uma resposta de erro cujo código vem da tabela, com
// mensagem própria e dados de detalhe.
//
// Sentinela fora da tabela não pode acontecer aqui — os três chamadores usam
// sentinelas que o teste de cobertura garante presentes —, mas a saída por
// ERRO_INTERNO existe para que um esquecimento futuro apareça como código
// genérico, e não como string vazia no campo "codigo".
func respostaErroDe(id string, sentinela error, mensagem string, dados any) protocolo.Resposta {
	traducao, ok := traducaoErros[sentinela]
	if !ok {
		return respostaErro(id, protocolo.CodigoErroInterno, "Falha inesperada ao processar a operação.")
	}

	corpo, err := json.Marshal(dados)
	if err != nil {
		return respostaErro(id, traducao.Codigo, mensagem)
	}

	resp := respostaErro(id, traducao.Codigo, mensagem)
	resp.Dados = corpo
	return resp
}
