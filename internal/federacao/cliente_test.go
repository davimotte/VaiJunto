package federacao

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vaijunto/internal/transacao"
)

// clienteApontadoPara sobe um par de verdade (httptest) e devolve um cliente
// da empresa "vaijunto" com ele na lista de pares.
//
// Os testes do cliente usam servidor HTTP real, e não um transporte de
// mentira: o que se afirma aqui é o comportamento de rede (prazo, cabeçalhos,
// corpo), e um transporte falso afirmaria só o que ele mesmo implementa.
func clienteApontadoPara(t *testing.T, par string, h http.Handler, prazo time.Duration) (*Cliente, Config) {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	cfg := Config{
		Empresa:      "vaijunto",
		Pares:        map[string]string{par: srv.URL},
		Relogio:      transacao.NovoRelogio(),
		PrazoChamada: prazo,
	}
	return NovoCliente(cfg), cfg
}

// configDoPar é a Config do servidor do outro lado: empresa diferente, e a
// nossa na lista de pares dele.
func configDoPar(empresa string) Config {
	return Config{
		Empresa:      empresa,
		Pares:        map[string]string{"vaijunto": "http://vaijunto.invalido:8080"},
		Relogio:      transacao.NovoRelogio(),
		PrazoChamada: time.Second,
	}
}

// Ida e volta completa contra o handler real: o corpo volta decodificado e o
// relógio das duas pontas avança conforme a seção 8.1.
//
// Com os dois relógios em zero: vaijunto envia L=0; o par faz
// max(0,0)+1 = 1 e responde 1; vaijunto faz max(0,1)+1 = 2.
func TestCliente_SaudeIdaEVolta(t *testing.T) {
	cfgPar := configDoPar("rotacapixaba")
	cliente, cfg := clienteApontadoPara(t, "rotacapixaba", NovoHandler(cfgPar), time.Second)

	saude, err := cliente.Saude(context.Background(), "rotacapixaba")
	if err != nil {
		t.Fatalf("Saude: %v", err)
	}

	if saude.Empresa != "rotacapixaba" {
		t.Errorf("empresa = %q, want %q", saude.Empresa, "rotacapixaba")
	}
	if saude.VersaoAPI != VersaoAPI {
		t.Errorf("versao_api = %d, want %d", saude.VersaoAPI, VersaoAPI)
	}
	if got := cfgPar.Relogio.Agora(); got != 1 {
		t.Errorf("L do par = %d, want 1", got)
	}
	if got := cfg.Relogio.Agora(); got != 2 {
		t.Errorf("L de quem chamou = %d, want 2", got)
	}
}

// O cliente envia os dois cabeçalhos da seção 1.1, com o L atual e sem
// incrementá-lo no envio.
func TestCliente_EnviaOsCabecalhos(t *testing.T) {
	var empresa, lamport string
	espiao := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		empresa = r.Header.Get(cabecalhoEmpresa)
		lamport = r.Header.Get(cabecalhoLamport)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	cliente, cfg := clienteApontadoPara(t, "rotacapixaba", espiao, time.Second)
	cfg.Relogio.Restaurar(41)

	if _, err := cliente.Saude(context.Background(), "rotacapixaba"); err != nil {
		t.Fatalf("Saude: %v", err)
	}

	if empresa != "vaijunto" {
		t.Errorf("%s = %q, want %q", cabecalhoEmpresa, empresa, "vaijunto")
	}
	if lamport != "41" {
		t.Errorf("%s = %q, want \"41\" (o envio não incrementa)", cabecalhoLamport, lamport)
	}
}

// Par que não responde: o erro sai dentro do prazo (D30, D41) e nada fica
// pendurado.
//
// O canal cancelado é a prova de que a conexão não ficou aberta à espera: o
// context da chamada é cancelado no retorno, e o servidor do outro lado vê o
// cancelamento. Sem isso, cada chamada estourada deixaria uma goroutine e um
// descritor presos até o par responder.
func TestCliente_ParQueDormeFalhaNoPrazo(t *testing.T) {
	const prazo = 50 * time.Millisecond
	cancelado := make(chan struct{})

	dorminhoco := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(10 * time.Second):
		case <-r.Context().Done():
			close(cancelado)
		}
	})

	cliente, _ := clienteApontadoPara(t, "rotacapixaba", dorminhoco, prazo)

	inicio := time.Now()
	_, err := cliente.Saude(context.Background(), "rotacapixaba")
	decorrido := time.Since(inicio)

	if !errors.Is(err, ErrParIndisponivel) {
		t.Fatalf("erro = %v, want ErrParIndisponivel", err)
	}
	// A margem é generosa porque a máquina de teste pode estar carregada; o
	// que se afirma é que o prazo decide, e não os 10 s do handler.
	if decorrido > time.Second {
		t.Errorf("a chamada levou %s, muito além do prazo de %s", decorrido, prazo)
	}

	select {
	case <-cancelado:
	case <-time.After(2 * time.Second):
		t.Error("o par não viu o cancelamento: a conexão ficou pendurada")
	}
}

// Empresa fora de --pares é erro de configuração nossa, e nem chega à rede.
func TestCliente_ParDesconhecido(t *testing.T) {
	cliente, _ := clienteApontadoPara(t, "rotacapixaba", NovoHandler(configDoPar("rotacapixaba")), time.Second)

	_, err := cliente.Saude(context.Background(), "rotamineira")
	if !errors.Is(err, ErrParDesconhecido) {
		t.Fatalf("erro = %v, want ErrParDesconhecido", err)
	}
}

// Resposta de erro vira ErroHTTP com status e código, que é o que a política
// de repetição da seção 1.3 usa para decidir entre repetir e parar.
func TestCliente_RespostaDeErroViraErroHTTP(t *testing.T) {
	cliente, _ := clienteApontadoPara(t, "rotacapixaba", NovoHandler(configDoPar("rotacapixaba")), time.Second)

	// O par conhece "vaijunto", mas não este caminho: 404 RECURSO_INEXISTENTE.
	err := cliente.chamar(context.Background(), "rotacapixaba", http.MethodGet, "/inventado", nil, nil)

	var erroHTTP *ErroHTTP
	if !errors.As(err, &erroHTTP) {
		t.Fatalf("erro = %v, want *ErroHTTP", err)
	}
	if erroHTTP.Status != http.StatusNotFound || erroHTTP.Codigo != codigoRecursoInexistente {
		t.Errorf("erro = %+v, want 404 %s", erroHTTP, codigoRecursoInexistente)
	}
}

// Resposta sem o cabeçalho de Lamport não derruba a chamada: o relógio apenas
// não avança. Falhar transformaria um par com cabeçalho faltando em "empresa
// indisponível" (D30) e abortaria transações por um detalhe de cabeçalho.
func TestCliente_RespostaSemLamportNaoDerruba(t *testing.T) {
	mudo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"empresa":"rotacapixaba","versao_api":1,"instante":"2026-11-05T08:15:02-03:00"}`))
	})

	cliente, cfg := clienteApontadoPara(t, "rotacapixaba", mudo, time.Second)
	cfg.Relogio.Restaurar(9)

	saude, err := cliente.Saude(context.Background(), "rotacapixaba")
	if err != nil {
		t.Fatalf("Saude: %v", err)
	}
	if saude.Empresa != "rotacapixaba" {
		t.Errorf("empresa = %q, want %q", saude.Empresa, "rotacapixaba")
	}
	if got := cfg.Relogio.Agora(); got != 9 {
		t.Errorf("L = %d, want 9 (sem cabeçalho, o relógio não se move)", got)
	}
}

// Corpo que não é a resposta esperada é ErrRespostaInvalida, e não sucesso
// silencioso: alguém está no ar falando outra língua, e repetir não ajuda.
func TestCliente_CorpoIlegivelEhRespostaInvalida(t *testing.T) {
	confuso := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`isto não é json`))
	})

	cliente, _ := clienteApontadoPara(t, "rotacapixaba", confuso, time.Second)

	if _, err := cliente.Saude(context.Background(), "rotacapixaba"); !errors.Is(err, ErrRespostaInvalida) {
		t.Fatalf("erro = %v, want ErrRespostaInvalida", err)
	}
}

// O registro de operações (D19) escreve a linha da chamada enviada, com a seta
// no sentido contrário da linha de quem recebe.
func TestCliente_RegistraChamadaEnviada(t *testing.T) {
	var saida bytes.Buffer
	cfgPar := configDoPar("rotacapixaba")
	srv := httptest.NewServer(NovoHandler(cfgPar))
	t.Cleanup(srv.Close)

	cfg := Config{
		Empresa:      "vaijunto",
		Pares:        map[string]string{"rotacapixaba": srv.URL},
		Relogio:      transacao.NovoRelogio(),
		PrazoChamada: time.Second,
	}
	cfg.Registro = NovoRegistro(&saida, cfg.Empresa)

	if _, err := NovoCliente(cfg).Saude(context.Background(), "rotacapixaba"); err != nil {
		t.Fatalf("Saude: %v", err)
	}

	linha := saida.String()
	for _, parte := range []string{"[vaijunto]", "→ rotacapixaba", "GET /saude", "L=0", "→ 200", "ms"} {
		if !strings.Contains(linha, parte) {
			t.Errorf("linha do registro sem %q: %s", parte, linha)
		}
	}
}
