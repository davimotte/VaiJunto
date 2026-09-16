package servidor

import (
	"errors"
	"fmt"
	"io"
	"net"
	"runtime/debug"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// atenderConexao implementa as camadas 2 (enquadramento), 3 (sessão) e 4
// (roteamento) sobre uma única conexão: lê uma linha por vez, decodifica o
// envelope e escreve a resposta, até a conexão fechar. Fechamento abrupto
// (EOF, RST) não é um erro de protocolo — encerra o loop sem erro a devolver,
// como manda a seção 4 do PROTOCOL.md.
//
// A sessão é declarada aqui, como variável local desta goroutine: é o
// mecanismo inteiro de identidade do sistema (D08). Ela nasce com a conexão,
// vive só dentro desta pilha e desaparece quando a função retorna — inclusive
// em queda abrupta, sem que nada precise ser removido de tabela alguma.
//
// O ponteiro do Estado é compartilhado por todas as conexões; é o mutex de
// dentro dele que serializa o acesso (D04), não esta camada.
//
// Um pânico durante o atendimento vira o erro desta conexão (D18): sem o
// recover, um defeito em qualquer handler derrubaria o processo, todas as
// sessões e o estado, que só existe em memória. O mutex não fica preso, porque
// toda seção crítica o libera com defer.
//
// reg nil desliga o registro de operações (D19). O encerramento com erro não
// passa por ele: volta ao chamador, que o registra sempre, com o registro
// ligado ou não.
func atenderConexao(conn net.Conn, estado *dominio.Estado, prazoEscrita time.Duration, reg *registro) (err error) {
	defer conn.Close()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("pânico ao atender a conexão: %v\n%s", r, debug.Stack())
		}
	}()

	endereco := "?"
	if a := conn.RemoteAddr(); a != nil {
		endereco = a.String()
	}
	reg.conexao(endereco, "conexão aberta")

	leitor := protocolo.NovoLeitorMensagens(conn)
	sess := &sessao{}

	for {
		linha, err := leitor.LerLinha()
		if err != nil {
			if errors.Is(err, io.EOF) {
				reg.conexao(endereco, "conexão encerrada")
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

		// Quem executou a operação é lido antes e depois dela: o LOGIN aceito
		// só tem usuário depois, e o LOGOUT só tem usuário antes, porque
		// esvazia a sessão.
		//
		// O registro é escrito depois de processarLinha retornar, quando a
		// seção crítica já terminou: a escrita no terminal nunca acontece com
		// o mutex do estado preso (D05). A duração medida é a do processamento,
		// sem a entrega da resposta, que depende do cliente ler (D17).
		usuario := sess.usuario.Usuario
		inicio := time.Now()
		req, resp := processarLinha(linha, estado, sess)
		duracao := time.Since(inicio)
		if sess.autenticado {
			usuario = sess.usuario.Usuario
		}
		reg.operacao(endereco, usuario, req, resp, duracao)

		// Renovado a cada resposta, e não uma vez por conexão: o prazo mede
		// quanto o cliente demora a ler esta resposta, e não quanto a sessão
		// dura (D17).
		if err := conn.SetWriteDeadline(time.Now().Add(prazoEscrita)); err != nil {
			return err
		}
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
//
// A requisição volta junto com a resposta para o registro de operações (D19),
// inclusive quando o envelope é inválido: o que foi possível ler dela ainda
// identifica a linha no terminal.
func processarLinha(linha []byte, estado *dominio.Estado, sess *sessao) (protocolo.Requisicao, protocolo.Resposta) {
	req, err := protocolo.DecodificarRequisicao(linha)
	if err != nil {
		if errors.Is(err, protocolo.ErrJSONInvalido) {
			return req, respostaErro(req.ID, protocolo.CodigoJSONInvalido, "Linha não decodifica como JSON válido.")
		}
		return req, respostaErro(req.ID, protocolo.CodigoEnvelopeInvalido, "Envelope inválido: id, tipo e dados são obrigatórios, e dados precisa ser um objeto.")
	}
	return req, rotear(req, estado, sess)
}
