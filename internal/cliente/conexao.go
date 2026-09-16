// Package cliente implementa o lado cliente do protocolo VAIJUNTO.
//
// O centro do pacote é a Conexao: um socket TCP aberto uma única vez, no
// início da execução, e reaproveitado do LOGIN ao LOGOUT (D15). Toda operação
// do PROTOCOL.md tem um método correspondente, e nenhum deles reabre a
// conexão nem reenvia credencial — é a materialização de D08 do lado do
// cliente: quem sou eu é o socket, não um token repetido a cada requisição.
//
// O pacote também reúne o que os dois CLIs compartilham além da conexão: a
// leitura de terminal tolerante a entrada inválida e a formatação de dinheiro
// e de instantes. Isso vive aqui, e não duplicado em cmd/motorista e
// cmd/passageiro, para que os dois menus tenham exatamente o mesmo
// comportamento na frente do usuário.
package cliente

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"vaijunto/internal/protocolo"
)

// VariavelServidor é a variável de ambiente que informa onde o servidor
// escuta. Os clientes rodam em contêiner (Dockerfile.cliente), onde passar
// flag em `docker run` é mais incômodo que passar `-e`.
const VariavelServidor = "VAIJUNTO_SERVIDOR"

// enderecoPadrao vale quando a variável não está definida, que é o caso do
// desenvolvimento com servidor e cliente na mesma máquina.
const enderecoPadrao = "localhost:9000"

// prazoConexao limita o estabelecimento do socket (D17).
const prazoConexao = 10 * time.Second

// prazoRespostaPadrao limita cada requisição, do envio à leitura da resposta
// (D17). Sem ele, um servidor travado ou uma máquina desligada congelaria o
// menu sem mensagem nenhuma.
//
// O estouro descarta a conexão, e é isso que torna o prazo seguro: uma resposta
// atrasada nunca chega a ser lida como se fosse da requisição seguinte, porque
// não existe requisição seguinte naquela conexão.
const prazoRespostaPadrao = 30 * time.Second

// EnderecoDoServidor devolve o endereço lido de VAIJUNTO_SERVIDOR, ou o
// padrão de desenvolvimento.
func EnderecoDoServidor() string {
	if endereco := strings.TrimSpace(os.Getenv(VariavelServidor)); endereco != "" {
		return endereco
	}
	return enderecoPadrao
}

// ErroServidor é uma resposta de status ERRO (PROTOCOL.md, seção 2.2).
//
// É deliberadamente distinto dos demais erros que uma operação pode devolver:
// um ErroServidor significa que o servidor entendeu a requisição, recusou-a e
// **explicou o motivo**, então a conexão continua válida e o menu segue. Já um
// erro de transporte ou de dessincronização torna a conexão inútil e obriga o
// cliente a encerrar. TratarErro é quem aplica essa distinção.
//
// Mensagem é o texto do protocolo, escrito pelo servidor para ser exibido
// direto ao usuário: é ele que o CLI mostra, nunca um texto genérico do
// cliente.
type ErroServidor struct {
	Codigo   string
	Mensagem string
	Dados    json.RawMessage
}

func (e *ErroServidor) Error() string {
	// Mensagem vazia não deveria ocorrer — o servidor sempre preenche —, mas
	// devolver string vazia aqui produziria um CLI que recusa a operação sem
	// dizer nada. O código é a informação mínima que resta.
	if e.Mensagem == "" {
		return "o servidor recusou a operação (" + e.Codigo + ")"
	}
	return e.Mensagem
}

// Conexao é a conexão única de uma sessão de cliente.
//
// Não tem sincronização porque é usada por uma só goroutine: o menu é
// sequencial e bloqueia no terminal entre uma operação e outra. Duas
// goroutines compartilhando a mesma Conexao intercalariam requisições em um
// canal que é estritamente um-para-um.
type Conexao struct {
	endereco string
	conn     net.Conn
	leitor   *protocolo.LeitorMensagens

	// prazoResposta é prazoRespostaPadrao fora dos testes, que o encurtam para
	// não esperar 30 s a cada execução.
	prazoResposta time.Duration

	// seq numera as requisições da sessão para formar o campo "id" do
	// envelope (seção 2.1). O id é conferido na resposta: como o servidor o
	// ecoa, um id diferente do enviado denuncia que requisição e resposta
	// saíram de sincronia.
	seq int
}

// Conectar abre a conexão TCP da sessão.
func Conectar(endereco string) (*Conexao, error) {
	conn, err := net.DialTimeout("tcp", endereco, prazoConexao)
	if err != nil {
		return nil, err
	}
	return &Conexao{
		endereco:      endereco,
		conn:          conn,
		leitor:        protocolo.NovoLeitorMensagens(conn),
		prazoResposta: prazoRespostaPadrao,
	}, nil
}

// Endereco devolve o endereço do servidor, para o cabeçalho do menu.
func (c *Conexao) Endereco() string { return c.endereco }

// Fechar encerra o socket. Fechar sem LOGOUT é legítimo: a seção 4 do
// PROTOCOL.md trata o fechamento como desconexão normal, e não há estado
// transitório preso à conexão para limpar (D07).
func (c *Conexao) Fechar() error { return c.conn.Close() }

// descartar fecha a conexão depois de uma falha de transporte e devolve o erro.
//
// Fechar aqui, e não confiar que o menu vá encerrar a sessão, é o que garante
// por construção a propriedade da D17: depois de um prazo estourado, nenhuma
// requisição nova sai por este socket, então a resposta atrasada da anterior
// nunca é lida como se fosse dela.
func (c *Conexao) descartar(err error) error {
	_ = c.conn.Close()
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return fmt.Errorf("o servidor não respondeu em %s: %w", c.prazoResposta, err)
	}
	return err
}

// executar envia uma requisição e devolve a resposta correspondente,
// decodificando o "dados" em destino quando ele não é nil.
//
// É o único ponto do cliente que fala com o socket; todos os métodos de
// operação passam por aqui. Concentrar isso é o que garante que o id seja
// sempre gerado e sempre conferido.
func (c *Conexao) executar(tipo string, dados any, destino any) error {
	c.seq++
	id := fmt.Sprintf("req-%d", c.seq)

	corpo, err := json.Marshal(dados)
	if err != nil {
		return fmt.Errorf("montar os dados de %s: %w", tipo, err)
	}

	// Um único prazo para envio e leitura, renovado a cada requisição: o que
	// importa ao usuário é quanto a operação inteira demora (D17).
	if err := c.conn.SetDeadline(time.Now().Add(c.prazoResposta)); err != nil {
		return c.descartar(fmt.Errorf("preparar o prazo de %s: %w", tipo, err))
	}

	if err := protocolo.EscreverLinha(c.conn, protocolo.Requisicao{ID: id, Tipo: tipo, Dados: corpo}); err != nil {
		return c.descartar(fmt.Errorf("enviar %s ao servidor: %w", tipo, err))
	}

	linha, err := c.leitor.LerLinha()
	if err != nil {
		return c.descartar(fmt.Errorf("ler a resposta de %s: %w", tipo, err))
	}
	resposta, err := protocolo.DecodificarResposta(linha)
	if err != nil {
		return fmt.Errorf("decodificar a resposta de %s: %w", tipo, err)
	}

	if resposta.ID != id {
		return fmt.Errorf("resposta de %s com id %q, esperado %q: conexão fora de sincronia", tipo, resposta.ID, id)
	}

	if resposta.Status != protocolo.StatusOK {
		return &ErroServidor{Codigo: resposta.Codigo, Mensagem: resposta.Mensagem, Dados: resposta.Dados}
	}

	if destino == nil {
		return nil
	}
	if err := json.Unmarshal(resposta.Dados, destino); err != nil {
		return fmt.Errorf("decodificar os dados de %s: %w", tipo, err)
	}
	return nil
}
