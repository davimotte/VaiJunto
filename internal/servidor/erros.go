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
	// Os dois abaixo nascem no boot e na carga, não de requisição; entram na
	// tabela só para a cobertura da seção 5.3 e, se um dia vazarem, virarem
	// ERRO_INTERNO em vez de um código inventado.
	dominio.ErrEmpresaInvalida: {protocolo.CodigoErroInterno, "Falha interna de configuração da empresa."},
	// Até a fase 4 nenhuma entrada de cliente alcançava este sentinela, e ele
	// era falha interna. Com o coordenador, o carona_id do RESERVAR passa pelo
	// ValidarItens, e um identificador sem prefixo de empresa é exatamente um
	// campo malformado vindo do cliente (D27).
	dominio.ErrIDSemEmpresa: {protocolo.CodigoCampoInvalido, "O carona_id precisa vir com o prefixo da empresa, como \"vaijunto:car-1\"."},
	// D22: o registro não chegou ao disco e a operação não foi aplicada. Para o
	// cliente é falha do servidor, e ele pode tentar de novo.
	dominio.ErrFalhaNoLog: {protocolo.CodigoErroInterno, "Falha interna ao gravar o registro da operação; nada foi alterado."},
	// Os erros do participante do 2PC nascem de uma chamada de outra empresa,
	// nunca de uma requisição de cliente, e são tratados pela tabela da borda
	// REST (internal/federacao/erros.go). Entram aqui pelo mesmo critério de
	// ErrEmpresaInvalida: se um dia vazarem para o TCP, aparecem como falha do
	// servidor em vez de um código inventado por proximidade.
	dominio.ErrPedidoInvalido:     {protocolo.CodigoErroInterno, "Falha interna: pedido de preparação inválido."},
	dominio.ErrItemDeOutraEmpresa: {protocolo.CodigoErroInterno, "Falha interna: trecho de carona de outra empresa."},
	dominio.ErrTransacaoAbortada:  {protocolo.CodigoErroInterno, "Falha interna: transação já abortada."},
	dominio.ErrViolacaoProtocolo:  {protocolo.CodigoErroInterno, "Falha interna: violação de protocolo entre servidores."},
	// Os dois do wait-die (seção 8.2) são a avaliação de **um** dono sobre um
	// trecho, e o passageiro nunca os vê: o coordenador os consome — AGUARDE
	// repetindo o PREPARAR, PRECEDENCIA tentando de novo (D36) — e só o
	// resultado da disputa inteira chega aqui, como DISPUTA_PERDIDA.
	dominio.ErrAguarde:     {protocolo.CodigoErroInterno, "Falha interna: a espera do wait-die é tratada pelo coordenador."},
	dominio.ErrPrecedencia: {protocolo.CodigoErroInterno, "Falha interna: a precedência do wait-die é tratada pelo coordenador."},
	// Os erros que o coordenador produz, e que são respostas legítimas ao
	// passageiro (seção 5.6 do PROJETO_P2.md).
	dominio.ErrReservaEmAndamento: {protocolo.CodigoReservaEmAndamento, "Você já tem uma reserva em andamento; aguarde o resultado dela."},
	// D30: algum dono não respondeu no prazo de votação. Nenhum assento fica
	// preso — o coordenador abortou a transação (D23).
	dominio.ErrEmpresaIndisponivel: {protocolo.CodigoEmpresaIndisponivel, "Uma das empresas do itinerário não respondeu; nenhum assento foi reservado."},
	// D36: a disputa pelo assento foi perdida nas três tentativas, ou o prazo
	// de votação acabou com alguém ainda pedindo para esperar. Diferente de
	// SEM_ASSENTO, que afirma que o trecho está cheio: aqui havia assento em
	// disputa, e outra transação levou.
	dominio.ErrDisputaPerdida: {protocolo.CodigoDisputaPerdida, "O assento foi disputado por outra reserva; nenhum assento foi reservado. Tente de novo."},
	// Fase 8 (D39): a carona tem participação em preparação, e cancelar não
	// pode abortar quem já votou SIM. O motorista tenta de novo depois.
	dominio.ErrCaronaEmTransacao: {protocolo.CodigoCaronaEmTransacao, "A carona tem uma reserva em preparação; tente cancelar novamente em alguns segundos."},
	// Os dois do aviso de carona cancelada nascem só entre servidores
	// (internal/federacao), nunca de um cliente TCP. Entram aqui pelo mesmo
	// critério dos demais deste grupo, para a cobertura do teste.
	dominio.ErrNaoCoordenador:  {protocolo.CodigoErroInterno, "Falha interna: aviso de carona cancelada é tratado entre servidores."},
	dominio.ErrNaoParticipante: {protocolo.CodigoErroInterno, "Falha interna: aviso de carona cancelada é tratado entre servidores."},
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
