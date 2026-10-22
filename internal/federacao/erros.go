package federacao

import (
	"encoding/json"
	"errors"
	"net/http"

	"vaijunto/internal/dominio"
)

// Códigos de erro HTTP da seção 5.1 do API_CONSORCIO.md. São constantes pelo
// mesmo motivo dos códigos do PROTOCOL.md: string repetida à mão em dois
// handlers é string que um dia vai divergir num deles.
const (
	codigoCabecalhoInvalido   = "CABECALHO_INVALIDO"
	codigoCorpoInvalido       = "CORPO_INVALIDO"
	codigoParametroInvalido   = "PARAMETRO_INVALIDO"
	codigoItemDeOutraEmpresa  = "ITEM_DE_OUTRA_EMPRESA"
	codigoNaoCoordenador      = "NAO_COORDENADOR"
	codigoEmpresaDesconhecida = "EMPRESA_DESCONHECIDA"
	codigoPapelInvalido       = "PAPEL_INVALIDO"
	codigoRecursoInexistente  = "RECURSO_INEXISTENTE"
	codigoViolacaoProtocolo   = "VIOLACAO_PROTOCOLO"
	codigoCorpoGrande         = "CORPO_GRANDE"
	codigoErroInterno         = "ERRO_INTERNO"
)

// Motivos do voto NÃO da seção 5.2. Não são erros HTTP: vão no corpo de uma
// resposta 200, porque "o participante processou e respondeu" (seção 2 da
// API). Quem os usa é o participante, na fase 4; aqui eles existem porque a
// tabela abaixo é a única tradução de sentinela da borda REST.
const (
	motivoSemAssento          = "SEM_ASSENTO"
	motivoPrecedencia         = "PRECEDENCIA"
	motivoCaronaNaoEncontrada = "CARONA_NAO_ENCONTRADA"
	motivoCaronaCancelada     = "CARONA_CANCELADA"
	motivoItinerarioInvalido  = "ITINERARIO_INVALIDO"
	motivoTransacaoAbortada   = "TRANSACAO_ABORTADA"
)

// erroHTTP é o destino de um erro na borda REST: o status e o código que vão
// para o par, com a mensagem legível da seção 2 da API.
//
// Status 200 não é contradição: é o caso dos motivos do voto NÃO, em que o
// participante processou a requisição e respondeu com uma recusa (seção 2).
type erroHTTP struct {
	Status   int
	Codigo   string
	Mensagem string
}

// Tabela única de tradução de erro de domínio para a borda REST
// (PROJETO_P2.md, seção 5.5). A outra tabela, para o protocolo TCP, está em
// internal/servidor/erros.go.
//
// internal/dominio não conhece HTTP nem a API do consórcio, então os
// sentinelas não carregam status nem código. É aqui, e só aqui, que os dois
// vocabulários se encontram — é isso que torna possível o teste que exige
// entrada para todo sentinela exportado.
//
// Os sentinelas se dividem em três grupos, e a divisão é o conteúdo da D34:
//
//  1. **Voto NÃO (status 200).** O participante os descobre ao avaliar um
//     PREPARAR; não são falhas de protocolo, e por isso voltam como resposta
//     processada, com o motivo da seção 5.2.
//  2. **Erro HTTP de verdade.** Falha do servidor que recebeu a requisição.
//  3. **Sentinelas que não atravessam esta borda.** São regras sobre o
//     passageiro e sobre a publicação de carona, decididas na empresa dona do
//     cliente, na borda TCP. Entram na tabela para a cobertura do teste e
//     mapeiam para ERRO_INTERNO: se um dia vazarem para cá, aparecem como
//     falha do servidor, e não como um código inventado por proximidade. É o
//     mesmo critério que internal/servidor/erros.go usa com ErrEmpresaInvalida.
var traducaoErros = map[error]erroHTTP{
	// 1. Motivos do voto NÃO (seção 5.2).
	dominio.ErrSemAssento:          {http.StatusOK, motivoSemAssento, "Trecho sem assento livre."},
	dominio.ErrCaronaNaoEncontrada: {http.StatusOK, motivoCaronaNaoEncontrada, "Carona inexistente nesta empresa."},
	dominio.ErrCaronaCancelada:     {http.StatusOK, motivoCaronaCancelada, "Carona cancelada."},
	dominio.ErrItinerarioInvalido:  {http.StatusOK, motivoItinerarioInvalido, "Os trechos pedidos não formam um itinerário válido."},
	// ErrItemInvalido é "índices fora da rota ou de >= ate", exatamente o que a
	// seção 5.2 descreve como ITINERARIO_INVALIDO. Na borda TCP ele vira
	// CAMPO_INVALIDO; a API do consórcio não tem esse código, e inventar um
	// seria divergir do documento.
	dominio.ErrItemInvalido: {http.StatusOK, motivoItinerarioInvalido, "Índices do trecho fora da rota da carona."},
	// A transação já foi abortada aqui, inclusive por veto (D38). É voto NÃO, e
	// não erro: o participante processou o PREPARAR e respondeu que não vai dar.
	dominio.ErrTransacaoAbortada: {http.StatusOK, motivoTransacaoAbortada, "A transação já foi abortada neste participante."},

	// 2. Erros HTTP.
	//
	// D22: o registro não chegou ao disco, então a operação não foi aplicada.
	// Para o par é falha do servidor, e a política da seção 1.3 manda repetir.
	dominio.ErrFalhaNoLog:  {http.StatusInternalServerError, codigoErroInterno, "Falha ao gravar o registro da operação; nada foi alterado."},
	dominio.ErrGeracaoDeID: {http.StatusInternalServerError, codigoErroInterno, "Falha ao gerar o identificador."},
	//
	// Os dois 400 abaixo são erro de quem chama, e nenhuma repetição os
	// conserta (seções 4.3 e 5.1 da API).
	dominio.ErrPedidoInvalido:     {http.StatusBadRequest, codigoCorpoInvalido, "Pedido de preparação sem a forma da seção 4.3: confira coordenador, participantes, carimbo, passageiro e itens."},
	dominio.ErrItemDeOutraEmpresa: {http.StatusBadRequest, codigoItemDeOutraEmpresa, "Algum item cita carona que não é desta empresa."},
	//
	// 409 encerra a repetição (seção 1.3): é transição impossível pela seção
	// 7.4, que nenhuma retransmissão corrige. Quem recebe registra a violação e
	// para.
	dominio.ErrViolacaoProtocolo: {http.StatusConflict, codigoViolacaoProtocolo, "Transição impossível para esta transação."},

	// 3. Sentinelas da borda TCP, que não atravessam o REST.
	dominio.ErrCidadeDesconhecida:        {http.StatusInternalServerError, codigoErroInterno, "Falha inesperada: cidade não atendida."},
	dominio.ErrRotaInvalida:              {http.StatusInternalServerError, codigoErroInterno, "Falha inesperada: rota inválida."},
	dominio.ErrPartidaInvalida:           {http.StatusInternalServerError, codigoErroInterno, "Falha inesperada: partida inválida."},
	dominio.ErrAssentosInvalidos:         {http.StatusInternalServerError, codigoErroInterno, "Falha inesperada: quantidade de assentos inválida."},
	dominio.ErrPrecoInvalido:             {http.StatusInternalServerError, codigoErroInterno, "Falha inesperada: preço de trecho inválido."},
	dominio.ErrCredenciaisInvalidas:      {http.StatusInternalServerError, codigoErroInterno, "Falha inesperada: autenticação não é assunto entre servidores."},
	dominio.ErrNaoEDono:                  {http.StatusInternalServerError, codigoErroInterno, "Falha inesperada: recurso de outro usuário."},
	dominio.ErrConflitoHorario:           {http.StatusInternalServerError, codigoErroInterno, "Falha inesperada: conflito de horário é decidido pelo coordenador."},
	dominio.ErrReservaNaoEncontrada:      {http.StatusInternalServerError, codigoErroInterno, "Falha inesperada: reserva não encontrada."},
	dominio.ErrReservaJaCancelada:        {http.StatusInternalServerError, codigoErroInterno, "Falha inesperada: reserva já cancelada."},
	dominio.ErrPrazoCancelamentoExpirado: {http.StatusInternalServerError, codigoErroInterno, "Falha inesperada: prazo de cancelamento é decidido pelo coordenador."},
	dominio.ErrEmpresaInvalida:           {http.StatusInternalServerError, codigoErroInterno, "Falha inesperada de configuração da empresa."},
	dominio.ErrIDSemEmpresa:              {http.StatusInternalServerError, codigoErroInterno, "Falha inesperada: identificador sem empresa."},
}

// erroHTTPDeDominio traduz um erro do domínio para a resposta da borda REST.
//
// A comparação é por errors.Is, e não por igualdade: o domínio embrulha os
// sentinelas com %w para acrescentar contexto, e o embrulho precisa continuar
// sendo reconhecido.
//
// Sentinela fora da tabela cai em ERRO_INTERNO, pelo mesmo motivo da borda
// TCP: um código errado é pior que um genérico, porque o par tomaria decisão
// em cima dele — a política de repetição da seção 1.3 depende do código.
func erroHTTPDeDominio(err error) erroHTTP {
	for sentinela, traducao := range traducaoErros {
		if errors.Is(err, sentinela) {
			return traducao
		}
	}
	return erroHTTP{http.StatusInternalServerError, codigoErroInterno, "Falha inesperada ao processar a requisição."}
}

// corpoDeErro é o formato de erro da seção 2 da API, o mesmo do PROTOCOL.md.
type corpoDeErro struct {
	Codigo   string `json:"codigo"`
	Mensagem string `json:"mensagem"`
}

// responderErro escreve a resposta de erro. Os cabeçalhos X-VaiJunto-* já
// foram postos pelo middleware antes de qualquer escrita.
//
// Um erro de status 200 aqui seria um motivo de voto NÃO escapando para o
// caminho de erro: voto não é erro (seção 2 da API), e o participante da fase
// 4 o devolve no corpo de um 200 com o formato do voto. Se acontecer, vira
// ERRO_INTERNO em vez de uma resposta 200 com cara de erro, que confundiria o
// coordenador justamente na distinção que a seção 2 protege.
func responderErro(w http.ResponseWriter, e erroHTTP) {
	if e.Status == http.StatusOK {
		e = erroHTTP{http.StatusInternalServerError, codigoErroInterno, "Falha inesperada ao processar a requisição."}
	}
	responderJSON(w, e.Status, corpoDeErro{Codigo: e.Codigo, Mensagem: e.Mensagem})
}

// responderJSON serializa o corpo antes de escrever o status: assim uma falha
// de serialização ainda pode virar 500, em vez de deixar uma resposta 200 com
// corpo truncado, que o par leria como JSON inválido.
func responderJSON(w http.ResponseWriter, status int, corpo any) {
	bruto, err := json.Marshal(corpo)
	if err != nil {
		bruto = []byte(`{"codigo":"ERRO_INTERNO","mensagem":"Falha ao serializar a resposta."}`)
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Resposta parcialmente escrita não tem conserto: o status já foi enviado.
	// Quem perceber a falha é o par, pelo JSON incompleto, e a política de
	// repetição da seção 1.3 resolve.
	_, _ = w.Write(append(bruto, '\n'))
}
