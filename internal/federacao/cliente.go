package federacao

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

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
