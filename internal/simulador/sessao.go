package simulador

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"vaijunto/internal/protocolo"
)

// sessao é uma conexão TCP falando o PROTOCOL.md, do jeito que um cliente
// oficial faria: uma conexão por sessão, reaproveitada em todas as operações
// (D15). Não tem sincronização, porque cada passageiro virtual tem a sua: o
// canal requisição/resposta é estritamente um-para-um, e duas goroutines na
// mesma conexão intercalariam envelopes.
//
// Não usa internal/cliente de propósito. A Conexao de lá guarda conn, seq e o
// prazo em campos privados e devolve a recusa embrulhada num error: daqui não
// se veria o id do envelope, não se conseguiria cercar o relógio só em volta do
// socket e o código da resposta só sairia por errors.As. A medição da seção
// 11.4 precisa das três coisas.
type sessao struct {
	conn   net.Conn
	leitor *protocolo.LeitorMensagens
	prazo  time.Duration
	rotulo string
	seq    int
}

// abrir conecta em endereco. O rotulo identifica o passageiro virtual no id do
// envelope, para que o id seja único no consórcio inteiro — é por ele que o
// registro do servidor e a latência medida se correlacionam.
func abrir(endereco, rotulo string, prazo time.Duration) (*sessao, error) {
	conn, err := net.DialTimeout("tcp", endereco, prazo)
	if err != nil {
		return nil, fmt.Errorf("conectar em %s: %w", endereco, err)
	}
	return &sessao{
		conn:   conn,
		leitor: protocolo.NovoLeitorMensagens(conn),
		prazo:  prazo,
		rotulo: rotulo,
	}, nil
}

// fechar encerra a conexão. Fechar sem LOGOUT é legítimo (PROTOCOL.md, seção
// 4): o fechamento abrupto é tratado como desconexão normal e não altera
// estado de domínio nenhum.
func (s *sessao) fechar() { _ = s.conn.Close() }

// executar envia uma requisição e devolve a resposta, a latência e, só em
// falha de transporte, um erro. Uma recusa do servidor **não** é erro: ela vem
// na Resposta, com o seu código, porque é resultado da medição e não defeito do
// simulador.
//
// O relógio envolve apenas a escrita no socket e a leitura da resposta: montar
// o JSON antes e decodificá-lo depois é custo do simulador, e não do servidor.
// É o mesmo recorte do teste de carga do P1 (PROJETO.md, seção 8.3), para que
// os dois CSVs sejam comparáveis.
//
// O id ecoado é conferido em toda requisição: é ele que garante que a latência
// registrada é a desta requisição, e não a de outra.
func (s *sessao) executar(tipo string, dados any, destino any) (protocolo.Resposta, time.Duration, error) {
	s.seq++
	id := fmt.Sprintf("sim-%s-%d", s.rotulo, s.seq)

	corpo, err := json.Marshal(dados)
	if err != nil {
		return protocolo.Resposta{}, 0, fmt.Errorf("montar dados de %s: %w", tipo, err)
	}
	linha, err := json.Marshal(protocolo.Requisicao{ID: id, Tipo: tipo, Dados: corpo})
	if err != nil {
		return protocolo.Resposta{}, 0, fmt.Errorf("montar envelope de %s: %w", tipo, err)
	}
	linha = append(linha, '\n')
	if err := s.conn.SetDeadline(time.Now().Add(s.prazo)); err != nil {
		return protocolo.Resposta{}, 0, fmt.Errorf("prazo de %s: %w", tipo, err)
	}

	envio := time.Now()
	if _, err := s.conn.Write(linha); err != nil {
		return protocolo.Resposta{}, 0, fmt.Errorf("enviar %s: %w", tipo, err)
	}
	bruta, err := s.leitor.LerLinha()
	latencia := time.Since(envio)
	if err != nil {
		return protocolo.Resposta{}, 0, fmt.Errorf("ler resposta de %s: %w", tipo, err)
	}

	resposta, err := protocolo.DecodificarResposta(bruta)
	if err != nil {
		return protocolo.Resposta{}, 0, fmt.Errorf("decodificar resposta de %s: %w", tipo, err)
	}
	if resposta.ID != id {
		return protocolo.Resposta{}, 0, fmt.Errorf("resposta de id %q fora de sincronia com a requisição %s", resposta.ID, id)
	}
	if resposta.Status == protocolo.StatusOK && destino != nil {
		if err := json.Unmarshal(resposta.Dados, destino); err != nil {
			return protocolo.Resposta{}, 0, fmt.Errorf("decodificar dados de %s: %w", tipo, err)
		}
	}
	return resposta, latencia, nil
}

// entrar autentica a conexão. Credencial recusada é erro, e não resultado: o
// simulador não tem o que medir numa sessão que não entrou.
func (s *sessao) entrar(usuario, senha string) error {
	resposta, _, err := s.executar(protocolo.TipoLogin,
		protocolo.LoginRequisicao{Usuario: usuario, Senha: senha}, nil)
	if err != nil {
		return err
	}
	if resposta.Status != protocolo.StatusOK {
		return fmt.Errorf("login de %s recusado com %s: %s", usuario, resposta.Codigo, resposta.Mensagem)
	}
	return nil
}
