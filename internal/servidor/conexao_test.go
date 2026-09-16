package servidor

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// processar é um atalho para os testes de envelope: manda a linha por
// processarLinha sobre um estado vazio e uma conexão recém-aberta, e devolve
// a Resposta montada, que é o que o cliente enxergaria.
//
// Sessão nova a cada chamada é proposital: estes testes tratam do envelope
// (PROTOCOL.md, seções 1 e 2), que é validado antes de qualquer regra de
// acesso, e o resultado não pode depender de quem está logado.
func processar(linha string) protocolo.Resposta {
	_, resp := processarLinha([]byte(linha), dominio.NovoEstado(), &sessao{})
	return resp
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

	if err := atenderConexao(conn, dominio.NovoEstado(), prazoEscrita, nil); err != nil {
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

	err := atenderConexao(conn, dominio.NovoEstado(), prazoEscrita, nil)
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

	if err := atenderConexao(conn, dominio.NovoEstado(), prazoEscrita, nil); !errors.Is(err, protocolo.ErrLinhaIncompleta) {
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

// TestAtenderConexao_ClienteQueNaoLeEncerraNoPrazo confere o prazo de escrita
// da D17: um cliente que envia uma requisição e nunca lê a resposta não pode
// prender a goroutine do servidor para sempre.
//
// Aqui net.Pipe é exatamente o que se quer, pelo motivo que o descarta em
// conexaoFalsa: sem buffer, a escrita do servidor bloqueia até alguém ler do
// outro lado, e ninguém lê.
func TestAtenderConexao_ClienteQueNaoLeEncerraNoPrazo(t *testing.T) {
	ladoServidor, ladoCliente := net.Pipe()
	t.Cleanup(func() { _ = ladoCliente.Close() })

	resultado := make(chan error, 1)
	go func() {
		resultado <- atenderConexao(ladoServidor, dominio.NovoEstado(), 50*time.Millisecond, nil)
	}()

	if _, err := ladoCliente.Write([]byte(`{"id":"1","tipo":"PING","dados":{}}` + "\n")); err != nil {
		t.Fatalf("enviar requisição: %v", err)
	}

	select {
	case err := <-resultado:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("err = %v, want os.ErrDeadlineExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("o servidor ficou preso escrevendo para um cliente que não lê")
	}
}

// TestAtenderConexao_PanicoEncerraSoAConexao confere a D18: um pânico no
// atendimento vira erro da conexão, e não a queda do processo.
//
// O Estado nil provoca um pânico de verdade no primeiro acesso ao estado,
// dentro do handler de LOGIN — o mesmo tipo de defeito que o recover existe
// para conter.
func TestAtenderConexao_PanicoEncerraSoAConexao(t *testing.T) {
	conn := &conexaoFalsa{entrada: strings.NewReader(`{"id":"1","tipo":"LOGIN","dados":{"usuario":"joao","senha":"1234"}}` + "\n")}

	err := atenderConexao(conn, nil, prazoEscrita, nil)
	if err == nil || !strings.Contains(err.Error(), "pânico") {
		t.Fatalf("err = %v, want erro de pânico recuperado", err)
	}
	if !conn.fechada {
		t.Fatalf("a conexão deveria ter sido fechada")
	}
}

// listenerFalso devolve, na ordem, os erros roteirizados e depois
// net.ErrClosed, como um listener que falhou e em seguida foi fechado.
type listenerFalso struct {
	erros    []error
	chamadas int
}

func (l *listenerFalso) Accept() (net.Conn, error) {
	l.chamadas++
	if l.chamadas <= len(l.erros) {
		return nil, l.erros[l.chamadas-1]
	}
	return nil, net.ErrClosed
}
func (l *listenerFalso) Close() error   { return nil }
func (l *listenerFalso) Addr() net.Addr { return nil }

// TestAceitar_FalhaTransitoriaNaoEncerra confere a D18: esgotar os descritores
// do processo é transitório, e não pode encerrar o laço de aceitação.
func TestAceitar_FalhaTransitoriaNaoEncerra(t *testing.T) {
	semDescritor := &net.OpError{Op: "accept", Net: "tcp", Err: syscall.EMFILE}
	listener := &listenerFalso{erros: []error{semDescritor, semDescritor}}
	s := &Servidor{listener: listener, estado: dominio.NovoEstado()}

	if err := s.Aceitar(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Aceitar = %v, want net.ErrClosed", err)
	}
	if listener.chamadas != 3 {
		t.Fatalf("Accept chamado %d vezes, want 3: o laço parou na falha transitória", listener.chamadas)
	}
}

// TestAceitar_RetornaQuandoOListenerFecha garante o outro lado da regra: com
// um listener real, Fechar ainda encerra o laço, em vez de fazê-lo tentar de
// novo para sempre.
func TestAceitar_RetornaQuandoOListenerFecha(t *testing.T) {
	s, err := Escutar("127.0.0.1:0", dominio.NovoEstado())
	if err != nil {
		t.Fatalf("escutar: %v", err)
	}

	resultado := make(chan error, 1)
	go func() { resultado <- s.Aceitar() }()
	if err := s.Fechar(); err != nil {
		t.Fatalf("fechar: %v", err)
	}

	select {
	case err := <-resultado:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Aceitar = %v, want net.ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Aceitar continuou tentando depois de o listener fechar")
	}
}

// atenderComRegistro atende uma conexão roteirizada sobre a carga de
// demonstração, com o registro de operações ligado, e devolve as linhas que
// foram registradas.
//
// A carga vem de dados/, e não de NovoEstado, porque o LOGIN precisa de um
// usuário que exista: sem ele não há como conferir que o registro mostra quem
// executou cada operação.
func atenderComRegistro(t *testing.T, mensagens ...string) []string {
	t.Helper()

	estado, err := dominio.CarregarEstado("../../dados/usuarios.json", "../../dados/caronas.json")
	if err != nil {
		t.Fatalf("carga inicial: %v", err)
	}
	conn := &conexaoFalsa{entrada: strings.NewReader(strings.Join(mensagens, "\n") + "\n")}

	var saida bytes.Buffer
	if err := atenderConexao(conn, estado, prazoEscrita, novoRegistro(&saida)); err != nil {
		t.Fatalf("atender conexão: %v", err)
	}
	if saida.Len() == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(saida.String(), "\n"), "\n")
}

// linhaDeOperacao é o que um teste espera de uma linha do registro. Os campos
// são comparados um a um, e não por substring, para que "maria" aparecer na
// linha errada, ou "-" aparecer só dentro do horário, não passe por acerto.
type linhaDeOperacao struct {
	usuario   string
	tipo      string
	id        string
	resultado string // "OK" ou "ERRO <CÓDIGO>"
}

// formatoDoInstante é o começo de toda linha do registro: data, hora com
// milissegundos e o endereço remoto entre colchetes.
var formatoDoInstante = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}\.\d{3} \[[^\]]*\] `)

// conferirOperacao compara uma linha do registro com o esperado, no formato
// "<data> <hora> [<endereço>] <usuário> <tipo> id=<id> → <resultado> (<duração> ms)".
func conferirOperacao(t *testing.T, linha string, esperado linhaDeOperacao) {
	t.Helper()

	if !formatoDoInstante.MatchString(linha) {
		t.Errorf("linha sem instante e endereço no início: %q", linha)
		return
	}
	campos := strings.Fields(formatoDoInstante.ReplaceAllString(linha, ""))
	if len(campos) < 6 {
		t.Errorf("linha com campos a menos: %q", linha)
		return
	}
	if campos[0] != esperado.usuario {
		t.Errorf("usuário = %q, want %q na linha %q", campos[0], esperado.usuario, linha)
	}
	if campos[1] != esperado.tipo {
		t.Errorf("tipo = %q, want %q na linha %q", campos[1], esperado.tipo, linha)
	}
	if campos[2] != "id="+esperado.id {
		t.Errorf("id = %q, want %q na linha %q", campos[2], "id="+esperado.id, linha)
	}
	if !strings.Contains(linha, "→ "+esperado.resultado+" (") || !strings.HasSuffix(linha, " ms)") {
		t.Errorf("resultado diferente de %q, ou sem duração, na linha %q", esperado.resultado, linha)
	}
}

// TestRegistro_UmaLinhaPorOperacao confere que toda operação atendida aparece
// no registro, com sucesso ou erro, entre a abertura e o encerramento da
// conexão.
//
// O usuário mostrado é o de quem executou a operação: o LOGIN aceito já sai
// com o nome, o recusado sai com "-", e o LOGOUT sai com quem acabou de sair,
// e não com a sessão já vazia.
func TestRegistro_UmaLinhaPorOperacao(t *testing.T) {
	linhas := atenderComRegistro(t,
		`{"id":"1","tipo":"PING","dados":{}}`,
		`{"id":"2","tipo":"LOGIN","dados":{"usuario":"maria","senha":"errada"}}`,
		`{"id":"3","tipo":"LOGIN","dados":{"usuario":"maria","senha":"abcd"}}`,
		`{"id":"4","tipo":"CANCELAR_RESERVA","dados":{"reserva_id":"res-inexistente"}}`,
		`{"id":"5","tipo":"LOGOUT","dados":{}}`,
	)

	if len(linhas) != 7 {
		t.Fatalf("esperava 7 linhas (abertura, 5 operações, encerramento), got %d:\n%s", len(linhas), strings.Join(linhas, "\n"))
	}
	if !strings.HasSuffix(linhas[0], "conexão aberta") {
		t.Errorf("primeira linha deveria registrar a abertura: %q", linhas[0])
	}
	conferirOperacao(t, linhas[1], linhaDeOperacao{"-", "PING", `"1"`, "OK"})
	conferirOperacao(t, linhas[2], linhaDeOperacao{"-", "LOGIN", `"2"`, "ERRO CREDENCIAIS_INVALIDAS"})
	conferirOperacao(t, linhas[3], linhaDeOperacao{"maria", "LOGIN", `"3"`, "OK"})
	conferirOperacao(t, linhas[4], linhaDeOperacao{"maria", "CANCELAR_RESERVA", `"4"`, "ERRO RESERVA_NAO_ENCONTRADA"})
	conferirOperacao(t, linhas[5], linhaDeOperacao{"maria", "LOGOUT", `"5"`, "OK"})
	if !strings.HasSuffix(linhas[6], "conexão encerrada") {
		t.Errorf("última linha deveria registrar o encerramento: %q", linhas[6])
	}
}

// TestRegistro_NaoExpoeSenha garante que o conteúdo de "dados" não vai para o
// registro. As senhas ficam em texto claro (D12), e o terminal do servidor é
// visível para quem estiver na frente da máquina.
func TestRegistro_NaoExpoeSenha(t *testing.T) {
	linhas := atenderComRegistro(t,
		`{"id":"1","tipo":"LOGIN","dados":{"usuario":"maria","senha":"senha-que-nao-pode-vazar"}}`,
	)

	// Sem esta checagem, um registro que não escreve nada passaria no teste.
	if len(linhas) != 3 {
		t.Fatalf("esperava 3 linhas, got %d:\n%s", len(linhas), strings.Join(linhas, "\n"))
	}
	registro := strings.Join(linhas, "\n")
	if strings.Contains(registro, "senha-que-nao-pode-vazar") || strings.Contains(registro, `"senha"`) {
		t.Fatalf("o registro expôs a senha:\n%s", registro)
	}
}

// TestRegistro_EnvelopeInvalido confere que a linha que nem chega a ser
// operação também é registrada, com "?" no lugar do tipo que não foi possível
// ler e o código de erro que o cliente recebeu.
func TestRegistro_EnvelopeInvalido(t *testing.T) {
	linhas := atenderComRegistro(t,
		`isso não é json`,
		`{"id":"8","tipo":"PING"}`,
	)

	if len(linhas) != 4 {
		t.Fatalf("esperava 4 linhas, got %d:\n%s", len(linhas), strings.Join(linhas, "\n"))
	}
	conferirOperacao(t, linhas[1], linhaDeOperacao{"-", "?", `""`, "ERRO JSON_INVALIDO"})
	conferirOperacao(t, linhas[2], linhaDeOperacao{"-", "PING", `"8"`, "ERRO ENVELOPE_INVALIDO"})
}

// TestRegistro_CampoDoClienteNaoQuebraLinha confere que id e tipo, que vêm do
// cliente, não conseguem forjar linhas no registro: um "\n" dentro deles sai
// escapado, e a operação continua ocupando uma linha só.
func TestRegistro_CampoDoClienteNaoQuebraLinha(t *testing.T) {
	linhas := atenderComRegistro(t,
		`{"id":"a\nfalsa","tipo":"X\nY","dados":{}}`,
	)

	if len(linhas) != 3 {
		t.Fatalf("esperava 3 linhas, got %d:\n%s", len(linhas), strings.Join(linhas, "\n"))
	}
	conferirOperacao(t, linhas[1], linhaDeOperacao{"-", `"X\nY"`, `"a\nfalsa"`, "ERRO TIPO_DESCONHECIDO"})
}
