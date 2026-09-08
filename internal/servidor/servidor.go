// Package servidor implementa o listener TCP, a leitura de mensagens por
// conexão e o roteamento por tipo de operação do protocolo VAIJUNTO
// (PROTOCOL.md, seções 1, 2 e 4; PROJETO.md, seção 5.1, camadas 1 a 4).
package servidor

import (
	"log"
	"net"

	"vaijunto/internal/dominio"
)

// Servidor aceita conexões TCP e despacha cada uma para sua própria
// goroutine (camada 1 — modelo thread-per-connection, PROJETO.md, decisão
// D03).
// O Estado é único e compartilhado por todas as conexões: é ele que carrega o
// mutex global (D04). O Servidor apenas o repassa a cada goroutine.
type Servidor struct {
	listener net.Listener
	estado   *dominio.Estado
}

// Escutar abre o listener TCP no endereço informado (ex.: "0.0.0.0:9000") e
// o associa ao estado de domínio que as conexões vão compartilhar.
func Escutar(endereco string, estado *dominio.Estado) (*Servidor, error) {
	l, err := net.Listen("tcp", endereco)
	if err != nil {
		return nil, err
	}
	return &Servidor{listener: l, estado: estado}, nil
}

// Endereco devolve o endereço em que o servidor está escutando.
func (s *Servidor) Endereco() string {
	return s.listener.Addr().String()
}

// Aceitar entra em loop aceitando conexões, cada uma tratada em sua própria
// goroutine. Uma conexão que falha não derruba as demais nem o servidor: o
// erro fica isolado dentro da goroutine que a atende. Aceitar só retorna
// quando o listener é fechado.
func (s *Servidor) Aceitar() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return err
		}
		go func() {
			if err := atenderConexao(conn, s.estado); err != nil {
				log.Printf("servidor: conexão %s encerrada: %v", conn.RemoteAddr(), err)
			}
		}()
	}
}

// Fechar interrompe o listener, encerrando o loop de Aceitar.
func (s *Servidor) Fechar() error {
	return s.listener.Close()
}
