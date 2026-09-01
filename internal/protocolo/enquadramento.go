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

// LerLinha devolve a próxima linha não vazia, sem o delimitador final.
// Linhas em branco são ignoradas silenciosamente (seção 1). O slice
// devolvido é uma cópia, válida além da próxima chamada a LerLinha.
func (l *LeitorMensagens) LerLinha() ([]byte, error) {
	for {
		linha, err := l.br.ReadSlice('\n')
		if err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				return nil, ErrLinhaMuitoGrande
			}
			if len(linha) == 0 {
				return nil, err
			}
			// Fim do stream sem '\n' final: ainda assim processa o que veio.
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

// DecodificarRequisicao decodifica uma linha (sem o '\n') como Requisicao.
func DecodificarRequisicao(linha []byte) (Requisicao, error) {
	var req Requisicao
	err := json.Unmarshal(linha, &req)
	return req, err
}

// DecodificarResposta decodifica uma linha (sem o '\n') como Resposta.
func DecodificarResposta(linha []byte) (Resposta, error) {
	var resp Resposta
	err := json.Unmarshal(linha, &resp)
	return resp, err
}
