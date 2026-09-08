package protocolo

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// TamanhoMaximoLinha é o limite de uma linha da conexão (seção 1). Uma linha
// maior é descartada e a conexão deve ser encerrada pelo chamador.
const TamanhoMaximoLinha = 64 * 1024

// ErrLinhaMuitoGrande é devolvido por LeitorMensagens.LerLinha quando uma
// linha excede TamanhoMaximoLinha sem que o delimitador '\n' seja encontrado.
var ErrLinhaMuitoGrande = errors.New("protocolo: linha excede o tamanho máximo de 64 KB")

// ErrLinhaIncompleta é devolvido quando o stream termina com bytes pendentes
// sem o delimitador '\n'. É o cliente encerrando no meio de uma mensagem, o
// que a seção 1 manda descartar sem resposta — distinto de io.EOF, que é a
// desconexão normal da seção 4, para que o servidor consiga registrar um do
// outro.
var ErrLinhaIncompleta = errors.New("protocolo: fim de stream com mensagem incompleta")

// LeitorMensagens lê mensagens JSON delimitadas por '\n' de uma conexão,
// aplicando o limite de tamanho de linha do protocolo (seção 1). Não
// interpreta o conteúdo da linha: decodificação fica por conta de
// DecodificarRequisicao / DecodificarResposta.
type LeitorMensagens struct {
	br *bufio.Reader
}

// NovoLeitorMensagens cria um LeitorMensagens sobre r. O buffer interno tem
// TamanhoMaximoLinha+1 bytes: só assim ReadSlice consegue distinguir "linha
// cabe exatamente no limite" de "linha estourou o limite" via
// bufio.ErrBufferFull.
func NovoLeitorMensagens(r io.Reader) *LeitorMensagens {
	return &LeitorMensagens{br: bufio.NewReaderSize(r, TamanhoMaximoLinha+1)}
}

// LerLinha devolve a próxima linha não vazia, sem o delimitador final. Linhas
// em branco são ignoradas silenciosamente (seção 1).
//
// Só devolve mensagem terminada em '\n': fragmento no fim do stream vira
// ErrLinhaIncompleta. O slice devolvido é uma cópia, válida além da próxima
// chamada a LerLinha.
func (l *LeitorMensagens) LerLinha() ([]byte, error) {
	for {
		linha, err := l.br.ReadSlice('\n')
		if err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				return nil, ErrLinhaMuitoGrande
			}
			// Uma mensagem só existe quando o '\n' chega (seção 1): o que
			// veio sem terminador é fragmento e é descartado, nunca
			// processado. Um JSON pode estar sintaticamente completo e ainda
			// assim ser o começo de uma mensagem maior, então aceitá-lo seria
			// executar uma operação que o cliente não terminou de pedir.
			if len(bytes.TrimRight(linha, "\r\n")) > 0 {
				return nil, ErrLinhaIncompleta
			}
			return nil, err
		}

		linha = bytes.TrimRight(linha, "\r\n")
		if len(linha) == 0 {
			continue
		}

		copia := make([]byte, len(linha))
		copy(copia, linha)
		return copia, nil
	}
}

// EscreverLinha serializa v como JSON e escreve a linha terminada em '\n'.
func EscreverLinha(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// Erros de envelope devolvidos por DecodificarRequisicao. São sentinelas: quem
// traduz para o código do PROTOCOL.md (seção 6) é a camada de roteamento, que
// é quem monta a Resposta.
var (
	ErrJSONInvalido     = errors.New("protocolo: linha não decodifica como JSON")
	ErrEnvelopeInvalido = errors.New("protocolo: envelope sem id, tipo ou dados válidos")
)

// envelopeBruto é o primeiro estágio da decodificação do envelope: cada campo
// fica como JSON cru, sem tipo imposto.
//
// Decodificar direto em Requisicao não serve: basta um campo com o tipo errado
// para encoding/json abortar, e aí o id se perde junto — mas a seção 2.2 manda
// ecoar o id sempre que ele for legível, inclusive na resposta de erro. É o id
// que o teste de carga usa para correlacionar requisição e resposta.
type envelopeBruto struct {
	ID    json.RawMessage `json:"id"`
	Tipo  json.RawMessage `json:"tipo"`
	Dados json.RawMessage `json:"dados"`
}

// DecodificarRequisicao decodifica uma linha (sem o '\n') como Requisicao, em
// dois estágios (PROTOCOL.md, seções 2.1 e 2.2).
//
// Estágio 1 separa os três campos sem interpretá-los, e distingue "não é JSON"
// (ErrJSONInvalido) de "é JSON, mas não é um envelope" (ErrEnvelopeInvalido):
// a seção 6 dá códigos diferentes aos dois casos. Estágio 2 lê o id primeiro e
// só então valida tipo e dados, de modo que a Requisicao devolvida junto com o
// erro já carrega o id a ecoar.
func DecodificarRequisicao(linha []byte) (Requisicao, error) {
	var bruto envelopeBruto
	if err := json.Unmarshal(linha, &bruto); err != nil {
		var errSintaxe *json.SyntaxError
		if errors.As(err, &errSintaxe) {
			return Requisicao{}, ErrJSONInvalido
		}
		// JSON válido que não é objeto (array, escalar, null): decodifica como
		// JSON, mas não como envelope — e não há id a ecoar.
		return Requisicao{}, ErrEnvelopeInvalido
	}

	var req Requisicao

	// O id vem antes de qualquer outra validação, e só é considerado legível
	// se for mesmo uma string não vazia: id numérico, nulo ou objeto não é o
	// id da seção 2.1, então a resposta sai com id "".
	if err := json.Unmarshal(bruto.ID, &req.ID); err != nil {
		req.ID = ""
	}
	if req.ID == "" {
		return req, ErrEnvelopeInvalido
	}

	if err := json.Unmarshal(bruto.Tipo, &req.Tipo); err != nil {
		req.Tipo = ""
	}
	if req.Tipo == "" {
		return req, ErrEnvelopeInvalido
	}

	// A seção 2.1 exige que dados seja um objeto, vazio quando a operação não
	// tem campos. Como o estágio 1 já garantiu que a linha inteira é JSON
	// válido, e json.RawMessage guarda o valor sem o espaço em branco que o
	// precedia, olhar o primeiro byte basta para saber se é objeto — sem
	// pagar uma segunda decodificação do payload em toda requisição.
	if len(bruto.Dados) == 0 || bruto.Dados[0] != '{' {
		return req, ErrEnvelopeInvalido
	}
	req.Dados = bruto.Dados

	return req, nil
}

// DecodificarResposta decodifica uma linha (sem o '\n') como Resposta.
func DecodificarResposta(linha []byte) (Resposta, error) {
	var resp Resposta
	err := json.Unmarshal(linha, &resp)
	return resp, err
}
