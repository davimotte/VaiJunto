package servidor

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"vaijunto/internal/protocolo"
)

// processar é um atalho para os testes: manda a linha por processarLinha e
// devolve a Resposta montada, que é o que o cliente enxergaria na conexão.
func processar(linha string) protocolo.Resposta {
	return processarLinha([]byte(linha))
}

// TestProcessarLinha_PingValido garante que o caminho feliz continua intacto:
// envelope completo, com "dados" objeto vazio, é roteado normalmente.
func TestProcessarLinha_PingValido(t *testing.T) {
	resp := processar(`{"id":"1","tipo":"PING","dados":{}}`)
	if resp.Status != protocolo.StatusOK {
		t.Fatalf("status = %q, want %q (resposta: %+v)", resp.Status, protocolo.StatusOK, resp)
	}
	if resp.ID != "1" {
		t.Fatalf("ID = %q, want %q", resp.ID, "1")
	}
}

// TestProcessarLinha_JSONInvalido confere a seção 1: linha que não decodifica
// como JSON responde JSON_INVALIDO, com id vazio porque não houve como lê-lo.
func TestProcessarLinha_JSONInvalido(t *testing.T) {
	for _, linha := range []string{"isso não é json", `{"id":"1","tipo":`} {
		resp := processar(linha)
		if resp.Codigo != protocolo.CodigoJSONInvalido {
			t.Errorf("linha %q: codigo = %q, want %q", linha, resp.Codigo, protocolo.CodigoJSONInvalido)
		}
		if resp.ID != "" {
			t.Errorf("linha %q: ID = %q, want \"\"", linha, resp.ID)
		}
	}
}

// TestProcessarLinha_DadosNaoObjeto é o teste da divergência 2: a seção 2.1
// exige "dados" objeto. Ausente, nulo, número, string, booleano ou array
// respondem ENVELOPE_INVALIDO, e não OK.
func TestProcessarLinha_DadosNaoObjeto(t *testing.T) {
	casos := map[string]string{
		"ausente":  `{"id":"9","tipo":"PING"}`,
		"nulo":     `{"id":"9","tipo":"PING","dados":null}`,
		"número":   `{"id":"9","tipo":"PING","dados":5}`,
		"string":   `{"id":"9","tipo":"PING","dados":"x"}`,
		"booleano": `{"id":"9","tipo":"PING","dados":true}`,
		"array":    `{"id":"9","tipo":"PING","dados":[1,2]}`,
	}
	for nome, linha := range casos {
		resp := processar(linha)
		if resp.Status != protocolo.StatusErro || resp.Codigo != protocolo.CodigoEnvelopeInvalido {
			t.Errorf("dados %s: got status=%q codigo=%q, want ERRO/%s", nome, resp.Status, resp.Codigo, protocolo.CodigoEnvelopeInvalido)
		}
	}
}

// TestProcessarLinha_EcoDoID é o teste da divergência 3: quando o envelope é
// inválido mas o id é legível, a resposta ecoa o id (seção 2.2 — `id` só vem
// "" quando não foi possível lê-lo). É o id que o teste de carga usa para
// correlacionar requisição e resposta, inclusive nas que falham.
func TestProcessarLinha_EcoDoID(t *testing.T) {
	casos := map[string]string{
		"tipo com tipo errado": `{"id":"req-7","tipo":5,"dados":{}}`,
		"tipo ausente":         `{"id":"req-7","dados":{}}`,
		"dados ausente":        `{"id":"req-7","tipo":"PING"}`,
		"dados nulo":           `{"id":"req-7","tipo":"PING","dados":null}`,
		"dados array":          `{"id":"req-7","tipo":"PING","dados":[1,2]}`,
	}
	for nome, linha := range casos {
		resp := processar(linha)
		if resp.Codigo != protocolo.CodigoEnvelopeInvalido {
			t.Errorf("%s: codigo = %q, want %q", nome, resp.Codigo, protocolo.CodigoEnvelopeInvalido)
		}
		if resp.ID != "req-7" {
			t.Errorf("%s: ID = %q, want %q", nome, resp.ID, "req-7")
		}
	}
}

// TestProcessarLinha_NaoObjeto confere que um JSON válido que não é objeto cai
// em ENVELOPE_INVALIDO (a linha decodificou, mas não como envelope) e não tem
// id a ecoar.
func TestProcessarLinha_NaoObjeto(t *testing.T) {
	for _, linha := range []string{`[1,2,3]`, `5`, `"texto"`, `null`} {
		resp := processar(linha)
		if resp.Codigo != protocolo.CodigoEnvelopeInvalido {
			t.Errorf("linha %q: codigo = %q, want %q", linha, resp.Codigo, protocolo.CodigoEnvelopeInvalido)
		}
		if resp.ID != "" {
			t.Errorf("linha %q: ID = %q, want \"\"", linha, resp.ID)
		}
	}
}

// TestProcessarLinha_TipoDesconhecido confere que o tipo inexistente continua
// respondendo TIPO_DESCONHECIDO com o id ecoado — o envelope estava correto,
// quem não existe é a operação.
func TestProcessarLinha_TipoDesconhecido(t *testing.T) {
	resp := processar(`{"id":"4","tipo":"NAO_EXISTE","dados":{}}`)
	if resp.Codigo != protocolo.CodigoTipoDesconhecido {
		t.Fatalf("codigo = %q, want %q", resp.Codigo, protocolo.CodigoTipoDesconhecido)
	}
	if resp.ID != "4" {
		t.Fatalf("ID = %q, want %q", resp.ID, "4")
	}
}

// TestProcessarLinha_DadosSemprePresente confere a seção 2.2: o campo "dados"
// existe em toda resposta, inclusive nas de erro, como objeto vazio.
func TestProcessarLinha_DadosSemprePresente(t *testing.T) {
	linhas := []string{
		`{"id":"1","tipo":"PING","dados":{}}`,
		`isso não é json`,
		`{"id":"9","tipo":"PING","dados":null}`,
		`{"id":"4","tipo":"NAO_EXISTE","dados":{}}`,
	}
	for _, linha := range linhas {
		b, err := json.Marshal(processar(linha))
		if err != nil {
			t.Fatalf("linha %q: marshal da resposta: %v", linha, err)
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(b, &envelope); err != nil {
			t.Fatalf("linha %q: unmarshal da resposta: %v", linha, err)
		}
		dados, ok := envelope["dados"]
		if !ok {
			t.Errorf("linha %q: resposta sem campo dados: %s", linha, b)
			continue
		}
		var objeto map[string]json.RawMessage
		if err := json.Unmarshal(dados, &objeto); err != nil || objeto == nil {
			t.Errorf("linha %q: dados não é um objeto: %s", linha, dados)
		}
	}
}

// conexaoFalsa é uma net.Conn de teste: lê de um io.Reader roteirizado e
// acumula em saida tudo que o servidor escreve, para que o teste possa afirmar
// que nada foi respondido. net.Pipe não serve aqui — é síncrono e sem buffer,
// então uma escrita indevida bloquearia o teste em vez de falhá-lo.
type conexaoFalsa struct {
	entrada io.Reader
	saida   bytes.Buffer
	fechada bool
}

func (c *conexaoFalsa) Read(p []byte) (int, error)       { return c.entrada.Read(p) }
func (c *conexaoFalsa) Write(p []byte) (int, error)      { return c.saida.Write(p) }
func (c *conexaoFalsa) Close() error                     { c.fechada = true; return nil }
func (c *conexaoFalsa) LocalAddr() net.Addr              { return nil }
func (c *conexaoFalsa) RemoteAddr() net.Addr             { return nil }
func (c *conexaoFalsa) SetDeadline(time.Time) error      { return nil }
func (c *conexaoFalsa) SetReadDeadline(time.Time) error  { return nil }
func (c *conexaoFalsa) SetWriteDeadline(time.Time) error { return nil }

// TestAtenderConexao_EOFLimpo confere que o cliente que encerra depois de uma
// mensagem completa é desconexão normal (PROTOCOL.md, seção 4): a conexão
// fecha, sem erro a registrar.
func TestAtenderConexao_EOFLimpo(t *testing.T) {
	conn := &conexaoFalsa{entrada: strings.NewReader(`{"id":"1","tipo":"PING","dados":{}}` + "\n")}

	if err := atenderConexao(conn); err != nil {
		t.Fatalf("EOF limpo não deveria produzir erro: %v", err)
	}
	if !conn.fechada {
		t.Fatalf("a conexão deveria ter sido fechada")
	}
	if !strings.Contains(conn.saida.String(), `"status":"OK"`) {
		t.Fatalf("a mensagem completa deveria ter sido respondida: %q", conn.saida.String())
	}
}

// TestAtenderConexao_LinhaIncompletaNaoResponde é o teste da regra da seção 1:
// bytes pendentes sem o terminador no EOF são descartados **sem resposta**. O
// erro chega ao chamador para virar registro, já que encerrar no meio de uma
// mensagem é o cliente violando o protocolo, e não uma desconexão comum.
func TestAtenderConexao_LinhaIncompletaNaoResponde(t *testing.T) {
	// JSON sintaticamente completo, mas sem o '\n': não é uma mensagem.
	conn := &conexaoFalsa{entrada: strings.NewReader(`{"id":"1","tipo":"PING","dados":{}}`)}

	err := atenderConexao(conn)
	if !errors.Is(err, protocolo.ErrLinhaIncompleta) {
		t.Fatalf("err = %v, want protocolo.ErrLinhaIncompleta", err)
	}
	if conn.saida.Len() != 0 {
		t.Fatalf("nada deveria ter sido respondido a uma linha incompleta: %q", conn.saida.String())
	}
	if !conn.fechada {
		t.Fatalf("a conexão deveria ter sido fechada")
	}
}

// TestAtenderConexao_RespondeAntesDeDescartarFragmento confere que o descarte
// atinge só o fragmento final: a mensagem que chegou inteira antes dele é
// respondida normalmente.
func TestAtenderConexao_RespondeAntesDeDescartarFragmento(t *testing.T) {
	entrada := `{"id":"1","tipo":"PING","dados":{}}` + "\n" + `{"id":"2","tipo":"PIN`
	conn := &conexaoFalsa{entrada: strings.NewReader(entrada)}

	if err := atenderConexao(conn); !errors.Is(err, protocolo.ErrLinhaIncompleta) {
		t.Fatalf("err = %v, want protocolo.ErrLinhaIncompleta", err)
	}

	respostas := strings.Count(conn.saida.String(), "\n")
	if respostas != 1 {
		t.Fatalf("esperava exatamente 1 resposta (só a mensagem completa), got %d: %q", respostas, conn.saida.String())
	}
	if !strings.Contains(conn.saida.String(), `"id":"1"`) {
		t.Fatalf("a resposta deveria ser a da mensagem completa: %q", conn.saida.String())
	}
}
