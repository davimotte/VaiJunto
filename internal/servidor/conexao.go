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
			// Linha acima de 64 KB: a seção 1 manda descartá-la e encerrar a
			// conexão sem resposta, já que não há como saber onde a
			// mensagem parcial termina.
			return err
		}

		resp := processarLinha(linha)

		if err := protocolo.EscreverLinha(conn, resp); err != nil {
			return err
		}
	}
}

// processarLinha decodifica uma linha e devolve a resposta correspondente,
// tratando as duas falhas de enquadramento antes de rotear (seção 1 e 6 do
// PROTOCOL.md).
func processarLinha(linha []byte) protocolo.Resposta {
	req, err := protocolo.DecodificarRequisicao(linha)
	if err != nil {
		return respostaErro("", protocolo.CodigoJSONInvalido, "Linha não decodifica como JSON válido.")
	}
	if req.ID == "" || req.Tipo == "" || req.Dados == nil {
		return respostaErro(req.ID, protocolo.CodigoEnvelopeInvalido, "Faltam os campos id, tipo ou dados no envelope.")
	}
	return rotear(req)
}
