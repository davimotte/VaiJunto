// Package servidor implementa o listener TCP, a leitura de mensagens por
// conexão e o roteamento por tipo de operação do protocolo VAIJUNTO
// (PROTOCOL.md, seções 1, 2 e 4; PROJETO.md, seção 5.1, camadas 1 a 4).
package servidor

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// prazoEscrita limita a entrega de cada resposta ao cliente (D17).
const prazoEscrita = 10 * time.Second

// pausaAposFalhaDeAccept evita um laço ocupado enquanto o recurso que faltou
// ao Accept não volta (D18).
const pausaAposFalhaDeAccept = 100 * time.Millisecond

// Servidor aceita conexões TCP e despacha cada uma para sua própria
// goroutine (camada 1 — modelo thread-per-connection, PROJETO.md, decisão
// D03).
// O Estado é único e compartilhado por todas as conexões: é ele que carrega o
// mutex global (D04). O Servidor apenas o repassa a cada goroutine.
type Servidor struct {
	listener net.Listener
	estado   *dominio.Estado
	registro *registro // nil enquanto RegistrarOperacoes não é chamado
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
//
// Qualquer outra falha do Accept é tratada como transitória (D18): o caso
// típico é o esgotamento de descritores, que some quando conexões fecham, e
// encerrar o laço por ela derrubaria o servidor inteiro.
func (s *Servidor) Aceitar() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			log.Printf("servidor: falha ao aceitar conexão, nova tentativa em %s: %v", pausaAposFalhaDeAccept, err)
			time.Sleep(pausaAposFalhaDeAccept)
			continue
		}
		go func() {
			if err := atenderConexao(conn, s.estado, prazoEscrita, s.registro); err != nil {
				log.Printf("servidor: conexão %s encerrada: %v", conn.RemoteAddr(), err)
			}
		}()
	}
}

// Fechar interrompe o listener, encerrando o loop de Aceitar.
func (s *Servidor) Fechar() error {
	return s.listener.Close()
}

// RegistrarOperacoes liga o registro de operações (D19): uma linha por
// conexão aberta, por operação atendida e por conexão encerrada, escrita em
// saida.
//
// Precisa ser chamado antes de Aceitar. O campo é lido pelas goroutines das
// conexões sem sincronização, o que só é seguro porque ninguém o altera
// depois que elas existem.
func (s *Servidor) RegistrarOperacoes(saida io.Writer) {
	s.registro = novoRegistro(saida)
}

// registro escreve o registro de operações no terminal do servidor (D19).
//
// Um *registro nil não escreve nada, e é assim que o registro fica desligado:
// quem chama não precisa de um if antes de cada linha, e os testes e o teste
// de carga, que sobem o servidor por Escutar, ficam sem registro por padrão.
//
// A escrita passa por um log.Logger, e não por fmt.Fprintf direto na saída,
// porque o Logger é seguro para uso concorrente: cada linha sai numa única
// escrita, protegida pelo mutex interno dele, e linhas de conexões diferentes
// não se misturam no meio. Esse mutex é outro, e nada tem a ver com o do
// estado (D04).
type registro struct {
	saida *log.Logger
}

func novoRegistro(saida io.Writer) *registro {
	// Sem prefixo e sem as flags de data do log: o instante é montado em
	// escrever, no fuso das cidades.
	return &registro{saida: log.New(saida, "", 0)}
}

// conexao registra um evento do ciclo de vida da conexão.
func (r *registro) conexao(endereco, evento string) {
	if r == nil {
		return
	}
	r.escrever(fmt.Sprintf("[%s] %s", endereco, evento))
}

// operacao registra uma requisição atendida: quem pediu, o quê, e o que o
// servidor respondeu.
//
// Só tipo, id e resultado vão para a linha, nunca o campo dados: ele traz a
// senha do LOGIN, guardada em texto claro (D12).
//
// id e tipo vêm do cliente e podem conter "\n". Por isso o id sai sempre
// entre aspas, com escapes, e o tipo também quando não é uma operação
// conhecida: sem isso, um cliente conseguiria forjar linhas no registro. O
// tipo conhecido sai sem aspas porque só pode ser um dos nomes da tabela de
// perfis.
func (r *registro) operacao(endereco, usuario string, req protocolo.Requisicao, resp protocolo.Resposta, duracao time.Duration) {
	if r == nil {
		return
	}

	if usuario == "" {
		usuario = "-"
	}

	tipo := req.Tipo
	_, conhecido := perfilExigido[tipo]
	switch {
	case tipo == "":
		tipo = "?"
	case !conhecido:
		tipo = strconv.Quote(tipo)
	}

	resultado := resp.Status
	if resp.Status == protocolo.StatusErro {
		resultado += " " + resp.Codigo
	}

	// float só na exibição da duração, em milissegundos com três casas.
	ms := float64(duracao.Microseconds()) / 1000
	r.escrever(fmt.Sprintf("[%s] %-8s %-22s id=%q → %s (%.3f ms)", endereco, usuario, tipo, req.ID, resultado, ms))
}

// escrever acrescenta o instante à linha e a entrega ao Logger.
//
// O instante é convertido para o fuso das cidades, e não deixado em
// time.Local, pelo mesmo motivo da seção 10.1 do PROJETO.md: no contêiner
// Alpine o fuso local é UTC, e o terminal mostraria 3 h a mais que os
// horários das caronas.
func (r *registro) escrever(texto string) {
	instante := time.Now().In(dominio.FusoDasCidades()).Format("2006/01/02 15:04:05.000")
	r.saida.Print(instante + " " + texto)
}
