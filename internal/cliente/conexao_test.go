package cliente

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"vaijunto/internal/protocolo"
)

// Os testes deste arquivo falam com um servidor falso, e não com
// internal/servidor. É proposital: o que se quer verificar aqui é como o
// cliente reage a respostas que um servidor correto nunca produz — id
// divergente, mensagem vazia, conexão que some no meio —, e um servidor
// correto, por definição, não consegue encená-las. A verificação contra o
// servidor de verdade já existe em testes/integracao_test.go.

// servidorFalso responde uma linha pré-escrita para cada requisição recebida,
// na ordem, e guarda as requisições para que o teste confira o envelope.
type servidorFalso struct {
	listener  net.Listener
	respostas []string

	mu        sync.Mutex
	recebidas []protocolo.Requisicao
}

func novoServidorFalso(t *testing.T, respostas ...string) *servidorFalso {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("escutar: %v", err)
	}
	s := &servidorFalso{listener: listener, respostas: respostas}
	go s.atender()
	t.Cleanup(func() { _ = listener.Close() })
	return s
}

func (s *servidorFalso) endereco() string { return s.listener.Addr().String() }

// atender lê uma requisição, registra-a e devolve a resposta da vez. Quando as
// respostas acabam, fecha a conexão: assim um teste que enviar mais
// requisições do que o previsto falha com EOF em vez de travar esperando uma
// resposta que não virá.
func (s *servidorFalso) atender() {
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	leitor := protocolo.NovoLeitorMensagens(conn)
	for i := 0; ; i++ {
		linha, err := leitor.LerLinha()
		if err != nil {
			return
		}
		req, _ := protocolo.DecodificarRequisicao(linha)

		s.mu.Lock()
		s.recebidas = append(s.recebidas, req)
		s.mu.Unlock()

		if i >= len(s.respostas) {
			return
		}
		if _, err := conn.Write([]byte(s.respostas[i] + "\n")); err != nil {
			return
		}
	}
}

func (s *servidorFalso) requisicoes() []protocolo.Requisicao {
	s.mu.Lock()
	defer s.mu.Unlock()
	copia := make([]protocolo.Requisicao, len(s.recebidas))
	copy(copia, s.recebidas)
	return copia
}

// conectarNoFalso abre a conexão do cliente contra o servidor falso.
func conectarNoFalso(t *testing.T, s *servidorFalso) *Conexao {
	t.Helper()

	conexao, err := Conectar(s.endereco())
	if err != nil {
		t.Fatalf("conectar: %v", err)
	}
	t.Cleanup(func() { _ = conexao.Fechar() })
	return conexao
}

// TestConexao_MontaOEnvelopeDaSecao21 confere que a requisição sai no formato
// do PROTOCOL.md: id de string, tipo em maiúsculas e dados como objeto.
func TestConexao_MontaOEnvelopeDaSecao21(t *testing.T) {
	servidor := novoServidorFalso(t,
		`{"id":"req-1","status":"OK","dados":{"usuario":"joao","nome":"João Silva","perfil":"MOTORISTA"}}`)
	conexao := conectarNoFalso(t, servidor)

	login, err := conexao.Login("joao", "1234")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if login.Nome != "João Silva" || login.Perfil != protocolo.PerfilMotorista {
		t.Errorf("resposta decodificada = %+v", login)
	}

	recebidas := servidor.requisicoes()
	if len(recebidas) != 1 {
		t.Fatalf("requisições recebidas = %d, want 1", len(recebidas))
	}
	if recebidas[0].ID != "req-1" {
		t.Errorf("id = %q, want %q", recebidas[0].ID, "req-1")
	}
	if recebidas[0].Tipo != protocolo.TipoLogin {
		t.Errorf("tipo = %q, want %q", recebidas[0].Tipo, protocolo.TipoLogin)
	}
	if got := string(recebidas[0].Dados); got != `{"usuario":"joao","senha":"1234"}` {
		t.Errorf("dados = %s", got)
	}
}

// TestConexao_IdSequencialPorRequisicao verifica que cada requisição da sessão
// recebe um id próprio. É esse pareamento que a seção 2.2 exige e que o teste
// de carga usa para correlacionar resposta e requisição.
func TestConexao_IdSequencialPorRequisicao(t *testing.T) {
	servidor := novoServidorFalso(t,
		`{"id":"req-1","status":"OK","dados":{}}`,
		`{"id":"req-2","status":"OK","dados":{}}`,
		`{"id":"req-3","status":"OK","dados":{}}`)
	conexao := conectarNoFalso(t, servidor)

	for i := 0; i < 3; i++ {
		if _, err := conexao.ListarMinhasReservas(false); err != nil {
			t.Fatalf("requisição %d: %v", i+1, err)
		}
	}

	recebidas := servidor.requisicoes()
	for i, req := range recebidas {
		esperado := []string{"req-1", "req-2", "req-3"}[i]
		if req.ID != esperado {
			t.Errorf("id da requisição %d = %q, want %q", i+1, req.ID, esperado)
		}
	}
}

// TestConexao_ErroDoProtocoloNaoQuebraASessao é a propriedade central do
// tratamento de erro do cliente: uma recusa prevista pelo PROTOCOL.md deixa a
// conexão utilizável, e a operação seguinte funciona sobre o mesmo socket.
//
// É o que sustenta D15 na prática — sem isso, perder a disputa por um assento
// obrigaria a reabrir a conexão e reautenticar.
func TestConexao_ErroDoProtocoloNaoQuebraASessao(t *testing.T) {
	servidor := novoServidorFalso(t,
		`{"id":"req-1","status":"ERRO","codigo":"SEM_ASSENTO","mensagem":"Assento esgotado no trecho Salvador → Feira de Santana.","dados":{"carona_id":"car-1","indice_trecho":0}}`,
		`{"id":"req-2","status":"OK","dados":{"reserva_id":"res-91c","preco_total_centavos":11500}}`)
	conexao := conectarNoFalso(t, servidor)

	_, err := conexao.Reservar([]protocolo.ItemReserva{{CaronaID: "car-1", De: 0, Ate: 1}})

	var erroServidor *ErroServidor
	if !errors.As(err, &erroServidor) {
		t.Fatalf("erro = %v (%T), want *ErroServidor", err, err)
	}
	if erroServidor.Codigo != protocolo.CodigoSemAssento {
		t.Errorf("codigo = %q, want %q", erroServidor.Codigo, protocolo.CodigoSemAssento)
	}
	// A mensagem precisa chegar íntegra ao CLI: é ela que explica qual trecho
	// esgotou, e o cliente não tem como reconstruir isso sozinho.
	if !strings.Contains(erroServidor.Mensagem, "Salvador → Feira de Santana") {
		t.Errorf("mensagem = %q, esperava o trecho esgotado", erroServidor.Mensagem)
	}
	// O "dados" do erro também precisa sobreviver (seção 5.9).
	if !strings.Contains(string(erroServidor.Dados), `"carona_id":"car-1"`) {
		t.Errorf("dados = %s", erroServidor.Dados)
	}

	// A mesma conexão continua servindo: nada foi fechado nem invalidado.
	reserva, err := conexao.Reservar([]protocolo.ItemReserva{{CaronaID: "car-2", De: 0, Ate: 1}})
	if err != nil {
		t.Fatalf("segunda reserva na mesma conexão: %v", err)
	}
	if reserva.ReservaID != "res-91c" {
		t.Errorf("reserva_id = %q, want %q", reserva.ReservaID, "res-91c")
	}
}

// TestConexao_IdDivergenteNaoEErroDeServidor: uma resposta pareada com a
// requisição errada é dessincronização, não recusa. Precisa sair como erro
// fatal, porque continuar leria toda resposta seguinte fora de lugar.
func TestConexao_IdDivergenteNaoEErroDeServidor(t *testing.T) {
	servidor := novoServidorFalso(t, `{"id":"req-99","status":"OK","dados":{}}`)
	conexao := conectarNoFalso(t, servidor)

	_, err := conexao.Ping()
	if err == nil {
		t.Fatal("id divergente aceito sem erro")
	}
	var erroServidor *ErroServidor
	if errors.As(err, &erroServidor) {
		t.Errorf("erro classificado como *ErroServidor: o menu continuaria sobre uma conexão fora de sincronia")
	}
}

// TestConexao_ServidorQueSomeNaoEErroDeServidor: queda da conexão também é
// fatal, e pelo mesmo motivo — não há como o menu seguir.
func TestConexao_ServidorQueSomeNaoEErroDeServidor(t *testing.T) {
	servidor := novoServidorFalso(t) // nenhuma resposta: fecha na primeira requisição
	conexao := conectarNoFalso(t, servidor)

	_, err := conexao.Ping()
	if err == nil {
		t.Fatal("conexão encerrada aceita sem erro")
	}
	var erroServidor *ErroServidor
	if errors.As(err, &erroServidor) {
		t.Errorf("erro de transporte classificado como *ErroServidor")
	}
}

// novoServidorMudo aceita uma conexão, lê tudo o que chega e nunca responde:
// é o servidor travado da D17. A goroutine termina quando o cliente fecha a
// conexão.
func novoServidorMudo(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("escutar: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = io.Copy(io.Discard, conn)
	}()
	return listener.Addr().String()
}

// TestConexao_ServidorMudoEncerraASessaoNoPrazo confere o prazo de resposta da
// D17: o cliente não fica preso esperando, o erro é de transporte (encerra a
// sessão, não volta ao menu), e a conexão é descartada, para que nenhuma
// resposta atrasada seja lida fora de lugar.
func TestConexao_ServidorMudoEncerraASessaoNoPrazo(t *testing.T) {
	conexao, err := Conectar(novoServidorMudo(t))
	if err != nil {
		t.Fatalf("conectar: %v", err)
	}
	t.Cleanup(func() { _ = conexao.Fechar() })
	conexao.prazoResposta = 50 * time.Millisecond

	resultado := make(chan error, 1)
	go func() {
		_, err := conexao.Ping()
		resultado <- err
	}()

	select {
	case err := <-resultado:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("Ping = %v, want os.ErrDeadlineExceeded", err)
		}
		var erroServidor *ErroServidor
		if errors.As(err, &erroServidor) {
			t.Fatalf("estouro de prazo classificado como *ErroServidor: o menu continuaria")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("o cliente ficou preso esperando um servidor que não responde")
	}

	if _, err := conexao.Ping(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Ping depois do estouro = %v, want net.ErrClosed: a conexão deveria ter sido descartada", err)
	}
}

// TestErroServidor_MensagemVaziaCaiNoCodigo garante que o CLI nunca recuse uma
// operação sem dizer nada, mesmo diante de uma resposta sem o campo mensagem.
func TestErroServidor_MensagemVaziaCaiNoCodigo(t *testing.T) {
	erro := &ErroServidor{Codigo: protocolo.CodigoErroInterno}
	if !strings.Contains(erro.Error(), protocolo.CodigoErroInterno) {
		t.Errorf("Error() = %q, esperava o código", erro.Error())
	}
}

// TestTratarErro aplica a política dos menus: recusa explicada volta ao menu,
// canal quebrado encerra a sessão.
func TestTratarErro(t *testing.T) {
	t.Run("erro do protocolo exibe a mensagem e segue", func(t *testing.T) {
		var saida bytes.Buffer
		term := NovoTerminal(strings.NewReader(""), &saida)

		erro := &ErroServidor{Codigo: protocolo.CodigoPrazoCancelamentoExpirado,
			Mensagem: "Cancelamento permitido até 1h antes da partida."}

		if err := TratarErro(term, erro); err != nil {
			t.Fatalf("TratarErro devolveu %v, want nil", err)
		}
		// A mensagem exibida é a do servidor, e não um texto inventado pelo
		// cliente: traduzir código aqui duplicaria a tabela de
		// internal/servidor e envelheceria mal.
		if !strings.Contains(saida.String(), "Cancelamento permitido até 1h antes da partida.") {
			t.Errorf("saída = %q", saida.String())
		}
	})

	t.Run("erro de transporte sobe", func(t *testing.T) {
		var saida bytes.Buffer
		term := NovoTerminal(strings.NewReader(""), &saida)

		erro := errors.New("conexão perdida")
		if err := TratarErro(term, erro); !errors.Is(err, erro) {
			t.Errorf("TratarErro devolveu %v, want o erro original", err)
		}
	})

	t.Run("nil segue sendo nil", func(t *testing.T) {
		var saida bytes.Buffer
		term := NovoTerminal(strings.NewReader(""), &saida)

		if err := TratarErro(term, nil); err != nil {
			t.Errorf("TratarErro(nil) = %v", err)
		}
		if saida.Len() != 0 {
			t.Errorf("saída = %q, esperava vazia", saida.String())
		}
	})
}

// TestEntrar_PerfilErradoDesautenticaAntesDeTentarDeNovo cobre o caminho que
// justifica o LOGOUT no cliente: um segundo LOGIN sobre a conexão já
// autenticada responderia JA_AUTENTICADO (PROTOCOL.md, seção 4).
func TestEntrar_PerfilErradoDesautenticaAntesDeTentarDeNovo(t *testing.T) {
	servidor := novoServidorFalso(t,
		`{"id":"req-1","status":"OK","dados":{"usuario":"maria","nome":"Maria Souza","perfil":"PASSAGEIRO"}}`,
		`{"id":"req-2","status":"OK","dados":{}}`,
		`{"id":"req-3","status":"OK","dados":{"usuario":"joao","nome":"João Silva","perfil":"MOTORISTA"}}`)
	conexao := conectarNoFalso(t, servidor)

	var saida bytes.Buffer
	term := NovoTerminal(strings.NewReader("maria\nabcd\njoao\n1234\n"), &saida)

	login, err := Entrar(term, conexao, protocolo.PerfilMotorista)
	if err != nil {
		t.Fatalf("Entrar: %v", err)
	}
	if login.Usuario != "joao" {
		t.Errorf("usuário autenticado = %q, want %q", login.Usuario, "joao")
	}

	recebidas := servidor.requisicoes()
	if len(recebidas) != 3 {
		t.Fatalf("requisições = %d, want 3", len(recebidas))
	}
	if recebidas[1].Tipo != protocolo.TipoLogout {
		t.Errorf("segunda requisição = %q, want %q", recebidas[1].Tipo, protocolo.TipoLogout)
	}
}

// TestEntrar_CredencialInvalidaRepeteSemLogout: um LOGIN recusado não
// autenticou nada, então não há o que desautenticar antes de tentar de novo.
func TestEntrar_CredencialInvalidaRepeteSemLogout(t *testing.T) {
	servidor := novoServidorFalso(t,
		`{"id":"req-1","status":"ERRO","codigo":"CREDENCIAIS_INVALIDAS","mensagem":"Usuário ou senha incorretos.","dados":{}}`,
		`{"id":"req-2","status":"OK","dados":{"usuario":"joao","nome":"João Silva","perfil":"MOTORISTA"}}`)
	conexao := conectarNoFalso(t, servidor)

	var saida bytes.Buffer
	term := NovoTerminal(strings.NewReader("joao\nerrada\njoao\n1234\n"), &saida)

	login, err := Entrar(term, conexao, protocolo.PerfilMotorista)
	if err != nil {
		t.Fatalf("Entrar: %v", err)
	}
	if login.Usuario != "joao" {
		t.Errorf("usuário autenticado = %q", login.Usuario)
	}
	if !strings.Contains(saida.String(), "Usuário ou senha incorretos.") {
		t.Errorf("saída = %q, esperava a mensagem do protocolo", saida.String())
	}

	recebidas := servidor.requisicoes()
	if len(recebidas) != 2 {
		t.Fatalf("requisições = %d, want 2", len(recebidas))
	}
	for i, req := range recebidas {
		if req.Tipo != protocolo.TipoLogin {
			t.Errorf("requisição %d = %q, want %q", i+1, req.Tipo, protocolo.TipoLogin)
		}
	}
}

// TestEntrar_FimDeEntradaEncerra: sem ninguém para responder, Entrar sai por
// ErrEntradaEncerrada em vez de repetir a pergunta para sempre.
func TestEntrar_FimDeEntradaEncerra(t *testing.T) {
	servidor := novoServidorFalso(t)
	conexao := conectarNoFalso(t, servidor)

	var saida bytes.Buffer
	term := NovoTerminal(strings.NewReader(""), &saida)

	if _, err := Entrar(term, conexao, protocolo.PerfilPassageiro); !errors.Is(err, ErrEntradaEncerrada) {
		t.Errorf("Entrar = %v, want ErrEntradaEncerrada", err)
	}
}
