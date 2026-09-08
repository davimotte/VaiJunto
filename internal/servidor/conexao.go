package servidor

import (
	"errors"
	"io"
	"net"

	"vaijunto/internal/protocolo"
)

// atenderConexao implementa as camadas 2 (enquadramento) e 4 (roteamento)
// sobre uma única conexão: lê uma linha por vez, decodifica o envelope e
// escreve a resposta, até a conexão fechar. Fechamento abrupto (EOF, RST)
// não é um erro de protocolo — encerra o loop silenciosamente, como manda a
// seção 4 do PROTOCOL.md.
func atenderConexao(conn net.Conn) error {
	defer conn.Close()

	leitor := protocolo.NovoLeitorMensagens(conn)

	for {
		linha, err := leitor.LerLinha()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			// Os dois jeitos de uma mensagem chegar incompleta — estourar os
			// 64 KB ou o stream acabar sem o '\n' — têm o mesmo tratamento na
			// seção 1: descartar sem resposta e encerrar a conexão. Ambos
			// voltam como erro, e não como o EOF acima, porque são o cliente
			// violando o protocolo: vale registrar, ao contrário da
			// desconexão normal.
			return err
		}

		resp := processarLinha(linha)

		if err := protocolo.EscreverLinha(conn, resp); err != nil {
			return err
		}
	}
}

// processarLinha decodifica uma linha e devolve a resposta correspondente,
// traduzindo as falhas de envelope para os códigos da seção 6 do PROTOCOL.md.
//
// A validação do envelope em si é de internal/protocolo: aqui só se escolhe o
// código. Nos dois casos a resposta usa req.ID, e não "": a decodificação em
// dois estágios extrai o id antes de validar tipo e dados, então o id vem
// preenchido sempre que era legível, e vazio só quando não havia como lê-lo
// (seção 2.2).
func processarLinha(linha []byte) protocolo.Resposta {
	req, err := protocolo.DecodificarRequisicao(linha)
	if err != nil {
		if errors.Is(err, protocolo.ErrJSONInvalido) {
			return respostaErro(req.ID, protocolo.CodigoJSONInvalido, "Linha não decodifica como JSON válido.")
		}
		return respostaErro(req.ID, protocolo.CodigoEnvelopeInvalido, "Envelope inválido: id, tipo e dados são obrigatórios, e dados precisa ser um objeto.")
	}
	return rotear(req)
}
