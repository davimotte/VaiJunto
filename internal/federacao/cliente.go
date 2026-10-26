package federacao

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/transacao"
)

// Erros do cliente. São sentinelas porque quem decide o caminho seguro é a
// camada 5 (seção 5.2), e ela decide pelo tipo da falha, não pelo texto.
var (
	// ErrParDesconhecido — empresa fora da lista de --pares (D20). É erro de
	// configuração nossa, não falha do par: nenhuma repetição o conserta.
	ErrParDesconhecido = errors.New("federacao: empresa fora da lista de pares")

	// ErrParIndisponivel — prazo estourado, conexão recusada ou resposta
	// interrompida. É exatamente o que a D30 define como falha detectada por
	// timeout: não é erro fatal, e cada operação tem o seu caminho seguro
	// (abortar, seguir parcial ou tentar de novo mais tarde).
	ErrParIndisponivel = errors.New("federacao: par não respondeu")

	// ErrRespostaInvalida — o par respondeu, mas com corpo que não é a
	// resposta esperada. Diferente de ErrParIndisponivel: aqui alguém está no
	// ar falando outra língua, e repetir não ajuda.
	ErrRespostaInvalida = errors.New("federacao: resposta do par ilegível")
)

// ErroHTTP é uma resposta de erro do par (seção 2 da API), com o status e o
// código que ele devolveu.
//
// É tipo, e não sentinela, porque o código importa para a decisão de quem
// chama: a seção 1.3 manda repetir em alguns casos e parar no 409
// (VIOLACAO_PROTOCOLO), que nenhuma repetição corrige.
type ErroHTTP struct {
	Status   int
	Codigo   string
	Mensagem string
}

func (e *ErroHTTP) Error() string {
	return fmt.Sprintf("federacao: par respondeu %d %s: %s", e.Status, e.Codigo, e.Mensagem)
}

// StatusHTTP satisfaz a interface que o coordenador declara para decidir a
// política de repetição da seção 1.3 — em particular, parar no 409.
//
// Existe como método, e não como leitura direta do campo, porque quem decide
// está em internal/transacao, que não importa este pacote (seção 5.3).
func (e *ErroHTTP) StatusHTTP() int { return e.Status }

// limiteResposta é o teto do corpo de resposta que o cliente aceita ler.
//
// A API limita o corpo da requisição a 64 KB, mas não o da resposta: a lista
// de caronas de /federacao/caronas pode passar disso. O teto aqui é só
// defensivo, bem acima do caso real: um par com defeito não pode nos fazer
// alocar sem limite.
const limiteResposta = 4 << 20

// Cliente é a ponta que chama os pares (camada 1–4' da seção 5.2).
//
// Tem o seu próprio http.Client, e nunca usa http.DefaultClient nem http.Get:
// o cliente padrão é global, não tem prazo e é compartilhado com qualquer
// biblioteca do processo, o que tornaria impossível garantir a D41.
type Cliente struct {
	empresa  string
	pares    map[string]string
	relogio  *transacao.Relogio
	registro *Registro
	prazo    time.Duration
	http     *http.Client
}

// NovoCliente monta o cliente a partir da mesma Config do servidor REST, para
// que as duas pontas compartilhem obrigatoriamente o mesmo relógio.
func NovoCliente(cfg Config) *Cliente {
	return &Cliente{
		empresa:  cfg.Empresa,
		pares:    cfg.Pares,
		relogio:  cfg.Relogio,
		registro: cfg.Registro,
		prazo:    cfg.PrazoChamada,
		http: &http.Client{
			// Sem Timeout de propósito: o prazo vem do context de cada
			// chamada (D41), que é configurável por flag. Dois lugares com
			// prazo seriam dois lugares para ajustar nos testes, e o do
			// http.Client venceria calado o menor dos dois.
			Transport: &http.Transport{
				// Uma transação fala várias vezes com o mesmo par (preparar,
				// confirmar); reaproveitar a conexão evita um aperto de mão
				// TCP por fase do 2PC.
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     60 * time.Second,
			},
		},
	}
}

// Saude pergunta a um par se ele está no ar (seção 4.1 da API, D30).
func (c *Cliente) Saude(ctx context.Context, destino string) (Saude, error) {
	var s Saude
	err := c.chamar(ctx, destino, http.MethodGet, "/saude", nil, &s)
	return s, err
}

// Caronas pede a um par as caronas dele com alguma parada em [desde, ate)
// (seção 4.2 da API; passo 4 da busca federada).
//
// O prazo é o do ctx que a busca entrega, que é o prazo único do leque (D41), e
// não o PrazoChamada: chamar é quem aplica o menor dos dois, porque
// context.WithTimeout sobre um ctx que já tem prazo mantém o mais apertado.
//
// Os instantes vão em RFC 3339, codificados por url.Values: o deslocamento de
// fuso tem "+", que sem codificação viraria espaço na query.
func (c *Cliente) Caronas(ctx context.Context, destino string, desde, ate time.Time) (RespostaCaronas, error) {
	parametros := url.Values{}
	parametros.Set("desde", desde.Format(time.RFC3339))
	parametros.Set("ate", ate.Format(time.RFC3339))

	var r RespostaCaronas
	err := c.chamar(ctx, destino, http.MethodGet, "/federacao/caronas?"+parametros.Encode(), nil, &r)
	return r, err
}

// O Cliente é a implementação HTTP da rede do 2PC (seção 5.4). A asserção é em
// tempo de compilação para que uma mudança na interface apareça aqui, e não só
// no teste que por acaso use as duas pontas.
var _ transacao.Rede = (*Cliente)(nil)

// Preparar envia a fase 1 do 2PC a um par (seção 4.3 da API).
//
// O txID vai no caminho sem codificação: o caractere ":" é válido num segmento
// de URL (seção 1.4 da API), e escapá-lo faria o par receber um identificador
// diferente do que foi enviado.
//
// Um voto que não é SIM, NÃO ou AGUARDE, um SIM sem pernas ou um NÃO sem motivo
// são ErrRespostaInvalida, e não falha de par: alguém está no ar falando outra
// língua, e repetir não ajuda. O coordenador precisa das pernas do SIM para a
// validação da D34, então um SIM vazio não é utilizável.
func (c *Cliente) Preparar(ctx context.Context, destino string, pedido dominio.PedidoPreparacao) (transacao.RespostaPreparar, error) {
	var corpo respostaVoto
	if err := c.chamar(ctx, destino, http.MethodPost, caminhoDaTransacao(pedido.TxID, "preparar"), pedidoDeFio(pedido), &corpo); err != nil {
		return transacao.RespostaPreparar{}, err
	}

	voto := transacao.Voto(corpo.Voto)
	switch {
	case voto == transacao.VotoSim && len(corpo.Pernas) > 0:
	case voto == transacao.VotoNao && corpo.Motivo != "":
	case voto == transacao.VotoAguarde:
	default:
		return transacao.RespostaPreparar{}, fmt.Errorf("%w: %s: voto %q sem a forma da seção 4.3", ErrRespostaInvalida, destino, corpo.Voto)
	}

	resposta := transacao.RespostaPreparar{
		Voto:     voto,
		Pernas:   pernasDoFio(corpo.Pernas),
		Motivo:   corpo.Motivo,
		CaronaID: corpo.CaronaID,
	}
	if corpo.Trecho != nil {
		resposta.Trecho = *corpo.Trecho
	}
	return resposta, nil
}

// Confirmar envia a decisão de confirmar (seção 4.4 da API).
func (c *Cliente) Confirmar(ctx context.Context, destino, txID string) (dominio.Situacao, error) {
	return c.decidir(ctx, destino, txID, "confirmar")
}

// Abortar envia a decisão de abortar (seção 4.5 da API).
func (c *Cliente) Abortar(ctx context.Context, destino, txID string) (dominio.Situacao, error) {
	return c.decidir(ctx, destino, txID, "abortar")
}

// Cancelar envia a decisão de cancelar (D39; seção 4.7 da API).
func (c *Cliente) Cancelar(ctx context.Context, destino, txID string) (dominio.Situacao, error) {
	return c.decidir(ctx, destino, txID, "cancelar")
}

// AvisarCaronaCancelada envia o aviso inverso da D39 (seção 4.8 da API):
// avisa o coordenador de txID que a carona caronaID caiu, para que ele
// decida o cancelamento do itinerário inteiro (seção 10.2 do PROJETO_P2.md).
func (c *Cliente) AvisarCaronaCancelada(ctx context.Context, destino, txID, caronaID string) (string, error) {
	corpo := pedidoAvisoCaronaCancelada{Tx: txID, CaronaID: caronaID}
	var resposta respostaAvisoCaronaCancelada
	if err := c.chamar(ctx, destino, http.MethodPost, "/avisos/carona-cancelada", corpo, &resposta); err != nil {
		return "", err
	}
	if resposta.SituacaoReserva == "" {
		return "", fmt.Errorf("%w: %s: resposta do aviso sem situacao_reserva", ErrRespostaInvalida, destino)
	}
	return resposta.SituacaoReserva, nil
}

// Consultar pergunta a um par a situação de uma transação (seção 4.6 da API).
//
// Quem a chama é o Terminador de um participante em dúvida: primeiro o
// coordenador e, sem resposta dele, os demais participantes (D38). Cada
// chamada é uma só; a repetição de 3 em 3 s é de quem chama (seção 1.3).
//
// Resposta fora das duas formas da seção 4.6 é ErrRespostaInvalida, e não
// falha de par, pelo mesmo critério do Preparar: alguém está no ar falando
// outra língua, e repetir não ajuda. A validação é estrita de propósito —
// agir sobre um papel ou uma decisão que não se entendeu é justamente como um
// participante acabaria abortando algo confirmado.
func (c *Cliente) Consultar(ctx context.Context, destino, txID string) (transacao.RespostaConsulta, error) {
	var corpo respostaConsulta
	if err := c.chamar(ctx, destino, http.MethodPost, caminhoDaTransacao(txID, "consultar"), struct{}{}, &corpo); err != nil {
		return transacao.RespostaConsulta{}, err
	}

	switch transacao.Papel(corpo.Papel) {
	case transacao.PapelCoordenador:
		switch dominio.Decisao(corpo.Decisao) {
		case dominio.DecisaoConfirmada, dominio.DecisaoEmAndamento, dominio.DecisaoAbortada:
			return transacao.RespostaConsulta{
				Papel:   transacao.PapelCoordenador,
				Decisao: dominio.Decisao(corpo.Decisao),
			}, nil
		}
		return transacao.RespostaConsulta{}, fmt.Errorf("%w: %s: decisão %q desconhecida", ErrRespostaInvalida, destino, corpo.Decisao)

	case transacao.PapelParticipante:
		switch corpo.Situacao {
		case string(dominio.SituacaoPreparado), string(dominio.SituacaoConfirmado),
			string(dominio.SituacaoAbortado), string(dominio.SituacaoCancelado),
			transacao.SituacaoDesconhecida:
			return transacao.RespostaConsulta{
				Papel:    transacao.PapelParticipante,
				Situacao: corpo.Situacao,
			}, nil
		}
		return transacao.RespostaConsulta{}, fmt.Errorf("%w: %s: situação %q desconhecida", ErrRespostaInvalida, destino, corpo.Situacao)
	}
	return transacao.RespostaConsulta{}, fmt.Errorf("%w: %s: papel %q desconhecido", ErrRespostaInvalida, destino, corpo.Papel)
}

// decidir é o corpo comum dos dois recursos da fase 2: mesmo corpo vazio, mesma
// resposta. O corpo é "{}", e não ausente, como manda a seção 1 da API.
func (c *Cliente) decidir(ctx context.Context, destino, txID, recurso string) (dominio.Situacao, error) {
	var corpo respostaSituacao
	if err := c.chamar(ctx, destino, http.MethodPost, caminhoDaTransacao(txID, recurso), struct{}{}, &corpo); err != nil {
		return "", err
	}

	switch situacao := dominio.Situacao(corpo.Situacao); situacao {
	case dominio.SituacaoPreparado, dominio.SituacaoConfirmado, dominio.SituacaoAbortado, dominio.SituacaoCancelado:
		return situacao, nil
	default:
		return "", fmt.Errorf("%w: %s: situação %q desconhecida", ErrRespostaInvalida, destino, corpo.Situacao)
	}
}

// caminhoDaTransacao monta o caminho de um recurso de transação (seção 5.7).
func caminhoDaTransacao(txID, recurso string) string {
	return "/transacoes/" + txID + "/" + recurso
}

// Pares devolve os nomes das empresas da lista estática (D20), em ordem.
//
// Ordenados porque é a partir desta lista que a busca abre o leque e monta
// empresas_indisponiveis: a iteração de um mapa em Go é aleatória, e sem a
// ordem a mesma falha produziria a mesma lista em ordens diferentes, o que
// tornaria a resposta não comparável num teste.
//
// Devolve uma fatia nova, e não o mapa: a lista de pares é fixa no boot, e quem
// consulta não pode alterá-la de dentro.
func (c *Cliente) Pares() []string {
	nomes := make([]string, 0, len(c.pares))
	for empresa := range c.pares {
		nomes = append(nomes, empresa)
	}
	sort.Strings(nomes)
	return nomes
}

// chamar é o caminho único de toda chamada a um par: monta a URL, põe os
// cabeçalhos, aplica o prazo da D41, atualiza o relógio com o valor da
// resposta e escreve a linha do registro de operações.
//
// corpo nil envia requisição sem corpo (GET). resposta nil descarta o corpo da
// resposta.
//
// Por que o prazo vem de context.WithTimeout, e não de um Timeout no
// http.Client: o context cobre resolução de nome, conexão, envio e espera pela
// resposta, e é cancelado no retorno pelo defer. Uma chamada que estoura o
// prazo não deixa goroutine nem conexão penduradas, que é o que a D30 precisa
// para tratar a queda de um par como evento comum.
func (c *Cliente) chamar(ctx context.Context, destino, metodo, caminho string, corpo, resposta any) error {
	base, conhecido := c.pares[destino]
	if !conhecido {
		return fmt.Errorf("%w: %q", ErrParDesconhecido, destino)
	}

	var leitor io.Reader
	if corpo != nil {
		bruto, err := json.Marshal(corpo)
		if err != nil {
			return fmt.Errorf("federacao: serializar corpo de %s %s: %v", metodo, caminho, err)
		}
		leitor = bytes.NewReader(bruto)
	}

	ctx, cancelar := context.WithTimeout(ctx, c.prazo)
	defer cancelar()

	req, err := http.NewRequestWithContext(ctx, metodo, base+caminho, leitor)
	if err != nil {
		return fmt.Errorf("federacao: montar %s %s%s: %v", metodo, base, caminho, err)
	}

	// Ponto único do relógio no envio: vai o L atual, sem incrementar
	// (seção 8.1).
	enviado := c.relogio.Agora()
	req.Header.Set(cabecalhoEmpresa, c.empresa)
	req.Header.Set(cabecalhoLamport, strconv.FormatUint(enviado, 10))
	if leitor != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	inicio := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		// Prazo estourado, conexão recusada, nome que não resolve: para quem
		// chama é tudo a mesma coisa (D30), e a linha do registro mostra qual
		// foi.
		c.registro.enviada(destino, metodo, caminho, enviado, "sem resposta", time.Since(inicio))
		return fmt.Errorf("%w: %s: %v", ErrParIndisponivel, destino, err)
	}
	defer resp.Body.Close()

	// O relógio é atualizado antes de usar a resposta (seção 1.1).
	c.aoReceber(resp)

	bruto, errLeitura := io.ReadAll(io.LimitReader(resp.Body, limiteResposta+1))
	c.registro.enviada(destino, metodo, caminho, enviado, strconv.Itoa(resp.StatusCode), time.Since(inicio))

	switch {
	case errLeitura != nil:
		// Resposta que começou e não terminou é falha de rede, não de
		// protocolo: mesmo tratamento de quem não respondeu.
		return fmt.Errorf("%w: %s: corpo interrompido: %v", ErrParIndisponivel, destino, errLeitura)
	case len(bruto) > limiteResposta:
		return fmt.Errorf("%w: %s: corpo acima de %d bytes", ErrRespostaInvalida, destino, limiteResposta)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return erroDoCorpo(resp.StatusCode, bruto)
	}

	if resposta != nil {
		// Campos desconhecidos são ignorados (seção 1 da API): um par com uma
		// versão mais nova pode mandar campo que ainda não conhecemos.
		if err := json.Unmarshal(bruto, resposta); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrRespostaInvalida, destino, err)
		}
	}
	return nil
}

// aoReceber atualiza o relógio com o valor que veio na resposta.
//
// Resposta sem o cabeçalho, ou com valor malformado, não derruba a chamada: o
// relógio simplesmente não avança, e o corpo continua utilizável. Falhar aqui
// transformaria a divergência de versão de um par — que a seção 4.1 manda
// apenas aparecer no registro — em "empresa indisponível" (D30), abortando
// transações por um cabeçalho. A correção do relógio não depende de ele avançar
// em toda resposta, e sim de nunca andar para trás (seção 8.1).
func (c *Cliente) aoReceber(resp *http.Response) {
	bruto := resp.Header.Get(cabecalhoLamport)
	if bruto == "" {
		return
	}
	recebido, err := strconv.ParseUint(bruto, 10, 64)
	if err != nil {
		return
	}
	c.relogio.AoReceber(recebido)
}

// erroDoCorpo monta o ErroHTTP a partir da resposta de erro da seção 2.
//
// Corpo que não tem o formato esperado continua sendo um erro com o status
// recebido: o que não pode é virar sucesso. O código fica ERRO_INTERNO, para
// que a política de repetição de quem chama (seção 1.3) trate como falha do
// servidor, e não confunda com um 409, que manda parar.
func erroDoCorpo(status int, bruto []byte) error {
	var corpo corpoDeErro
	if err := json.Unmarshal(bruto, &corpo); err != nil || corpo.Codigo == "" {
		return &ErroHTTP{Status: status, Codigo: codigoErroInterno, Mensagem: "Resposta de erro sem código."}
	}
	return &ErroHTTP{Status: status, Codigo: corpo.Codigo, Mensagem: corpo.Mensagem}
}
