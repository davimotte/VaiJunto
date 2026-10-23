package testes

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"vaijunto/internal/federacao"
	"vaijunto/internal/transacao"
)

// Testes multiempresa da pilha REST: as três empresas sobem no mesmo
// processo, cada uma com a sua porta HTTP de verdade numa porta efêmera de
// loopback, e conversam pelo cliente da federação (PROJETO_P2.md, seção 13.3;
// CLAUDE.md). O compose fica para o teste final e para a demonstração.

// saidaSegura é o destino do registro de operações nos testes.
//
// O log.Logger já serializa as suas escritas, mas o teste LÊ o buffer
// enquanto a goroutine do servidor pode estar escrevendo nele: sem o mutex, é
// corrida de verdade, e o -race acusaria.
type saidaSegura struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *saidaSegura) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *saidaSegura) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// empresaREST é uma empresa do consórcio do ponto de vista da pilha REST.
type empresaREST struct {
	nome     string
	pares    map[string]string
	relogio  *transacao.Relogio
	cliente  *federacao.Cliente
	servidor *federacao.Servidor
	registro *saidaSegura
}

// subirConsorcio sobe uma porta HTTP por empresa e cruza as listas de pares
// com os endereços efetivos.
//
// Os endereços só existem depois do Escutar (porta 0), e por isso as listas de
// pares são preenchidas depois — antes de qualquer Servir, de modo que nenhuma
// goroutine leia o mapa enquanto ele é escrito.
//
// **Não** é o subirConsorcioP2 de ajuda_test.go, e a diferença é deliberada:
// este consórcio sobe sem Estado nenhum, porque os testes deste arquivo
// exercitam só a pilha REST — cabeçalhos, relógio e detecção de par fora do ar.
// Montar domínio, participante e coordenador aqui faria uma falha de
// enquadramento HTTP aparecer como falha do 2PC. Quem precisa das peças de
// cima usa o helper compartilhado.
func subirConsorcio(t *testing.T, nomes ...string) map[string]*empresaREST {
	t.Helper()

	consorcio := make(map[string]*empresaREST, len(nomes))
	configs := make(map[string]federacao.Config, len(nomes))

	for _, nome := range nomes {
		e := &empresaREST{
			nome:     nome,
			pares:    make(map[string]string),
			relogio:  transacao.NovoRelogio(),
			registro: &saidaSegura{},
		}
		cfg := federacao.Config{
			Empresa:      nome,
			Pares:        e.pares,
			Relogio:      e.relogio,
			Registro:     federacao.NovoRegistro(e.registro, nome),
			PrazoChamada: 500 * time.Millisecond,
		}

		srv, err := federacao.Escutar("127.0.0.1:0", cfg)
		if err != nil {
			t.Fatalf("empresa %s: escutar: %v", nome, err)
		}
		e.servidor = srv
		consorcio[nome] = e
		configs[nome] = cfg
	}

	for nome, e := range consorcio {
		for outro, o := range consorcio {
			if outro != nome {
				e.pares[outro] = "http://" + o.servidor.Endereco()
			}
		}
		e.cliente = federacao.NovoCliente(configs[nome])

		servidor := e.servidor
		go func() { _ = servidor.Servir() }()
		t.Cleanup(func() { servidor.Fechar() })
	}
	return consorcio
}

// Cada empresa responde /saude para as outras duas (D30), e o relógio das
// duas pontas avança em cada troca (seção 8.1). É o critério de pronto da
// fase, exercitado sem Docker.
func TestSaudeEntreAsTresEmpresas(t *testing.T) {
	consorcio := subirConsorcio(t, "vaijunto", "rotacapixaba", "rotafluminense")

	for nome, quemChama := range consorcio {
		for destino := range consorcio {
			if destino == nome {
				continue
			}

			antesDeQuemChama := quemChama.relogio.Agora()
			antesDoDestino := consorcio[destino].relogio.Agora()

			saude, err := quemChama.cliente.Saude(context.Background(), destino)
			if err != nil {
				t.Fatalf("%s → %s: %v", nome, destino, err)
			}

			if saude.Empresa != destino {
				t.Errorf("%s → %s: empresa = %q, want %q", nome, destino, saude.Empresa, destino)
			}
			if saude.VersaoAPI != federacao.VersaoAPI {
				t.Errorf("%s → %s: versao_api = %d, want %d", nome, destino, saude.VersaoAPI, federacao.VersaoAPI)
			}
			if got := consorcio[destino].relogio.Agora(); got <= antesDoDestino {
				t.Errorf("%s → %s: L do destino não avançou (%d → %d)", nome, destino, antesDoDestino, got)
			}
			if got := quemChama.relogio.Agora(); got <= antesDeQuemChama {
				t.Errorf("%s → %s: L de quem chamou não avançou (%d → %d)", nome, destino, antesDeQuemChama, got)
			}
		}
	}
}

// O registro de operações (D19) mostra as duas pontas da mesma chamada: a
// linha "→" de quem chamou e a linha "←" de quem recebeu. É por elas que o
// protocolo fica visível na demonstração (seção 5.2).
func TestRegistroDeOperacoesMostraAsDuasPontas(t *testing.T) {
	consorcio := subirConsorcio(t, "vaijunto", "rotacapixaba")

	if _, err := consorcio["vaijunto"].cliente.Saude(context.Background(), "rotacapixaba"); err != nil {
		t.Fatalf("Saude: %v", err)
	}

	esperarLinha(t, consorcio["vaijunto"].registro, "→ rotacapixaba GET /saude")
	esperarLinha(t, consorcio["rotacapixaba"].registro, "← vaijunto GET /saude")
}

// esperarLinha espera o trecho aparecer no registro.
//
// A linha de quem recebe é escrita depois de a resposta ser enviada, então ela
// pode chegar ao buffer um instante após a chamada retornar. Esperar com
// prazo, em vez de ler uma vez só, é o que torna o teste determinístico sem
// depender do escalonador.
func esperarLinha(t *testing.T, saida *saidaSegura, trecho string) {
	t.Helper()

	limite := time.Now().Add(2 * time.Second)
	for time.Now().Before(limite) {
		if strings.Contains(saida.String(), trecho) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("registro sem %q:\n%s", trecho, saida.String())
}

// Par derrubado não é erro fatal: vira ErrParIndisponivel, detectado dentro do
// prazo (D30, D41). É o caminho que a busca federada e o 2PC usam para seguir
// sem a empresa que caiu.
func TestParDerrubadoViraIndisponivel(t *testing.T) {
	consorcio := subirConsorcio(t, "vaijunto", "rotacapixaba")

	// Derruba a empresa de destino, mas mantém a URL na lista de pares: é
	// exatamente o que acontece quando o tutor dá docker kill numa máquina.
	if err := consorcio["rotacapixaba"].servidor.Fechar(); err != nil {
		t.Fatalf("Fechar: %v", err)
	}

	inicio := time.Now()
	_, err := consorcio["vaijunto"].cliente.Saude(context.Background(), "rotacapixaba")

	if !errors.Is(err, federacao.ErrParIndisponivel) {
		t.Fatalf("erro = %v, want ErrParIndisponivel", err)
	}
	if decorrido := time.Since(inicio); decorrido > 2*time.Second {
		t.Errorf("a falha levou %s para ser detectada", decorrido)
	}
}
