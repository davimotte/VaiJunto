package persistencia

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func caminhoTemp(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "empresa.wal")
}

func escrever(t *testing.T, caminho, conteudo string) {
	t.Helper()
	if err := os.WriteFile(caminho, []byte(conteudo), 0o644); err != nil {
		t.Fatalf("preparar log: %v", err)
	}
}

func lerTudo(t *testing.T, caminho string) string {
	t.Helper()
	b, err := os.ReadFile(caminho)
	if err != nil {
		t.Fatalf("ler log: %v", err)
	}
	return string(b)
}

// Log inexistente é o primeiro boot: o arquivo nasce vazio e nada é aplicado.
func TestAbrir_ArquivoInexistenteNasceVazio(t *testing.T) {
	caminho := caminhoTemp(t)
	arq, linhas, err := Abrir(caminho)
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	defer arq.Fechar()
	if len(linhas) != 0 {
		t.Errorf("linhas = %d, want 0", len(linhas))
	}
	if _, err := os.Stat(caminho); err != nil {
		t.Errorf("arquivo não foi criado: %v", err)
	}
}

// O que foi anexado volta, na mesma ordem, depois de reabrir.
func TestAnexarEReabrir(t *testing.T) {
	caminho := caminhoTemp(t)
	arq, _, err := Abrir(caminho)
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	for _, l := range []string{`{"seq":1}`, `{"seq":2}`} {
		if err := arq.Anexar([]byte(l)); err != nil {
			t.Fatalf("Anexar: %v", err)
		}
	}
	arq.Fechar()

	arq, linhas, err := Abrir(caminho)
	if err != nil {
		t.Fatalf("reabrir: %v", err)
	}
	defer arq.Fechar()
	if len(linhas) != 2 || string(linhas[0]) != `{"seq":1}` || string(linhas[1]) != `{"seq":2}` {
		t.Errorf("linhas = %q", linhas)
	}
}

// TP1 (parte 1): a última linha, interrompida pela queda, é descartada, e o
// arquivo é aparado. Sem o truncamento, o próximo Anexar colaria na sobra e
// criaria uma linha inválida NO MEIO do log, que o boot seguinte recusaria.
func TestAbrir_DescartaUltimaLinhaIncompletaETrunca(t *testing.T) {
	caminho := caminhoTemp(t)
	escrever(t, caminho, "{\"seq\":1}\n{\"seq\":2}\n{\"seq\":3,\"ti")

	arq, linhas, err := Abrir(caminho)
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	if len(linhas) != 2 {
		t.Fatalf("linhas = %d, want 2 (a incompleta é descartada)", len(linhas))
	}
	if got := lerTudo(t, caminho); got != "{\"seq\":1}\n{\"seq\":2}\n" {
		t.Errorf("arquivo não foi truncado: %q", got)
	}

	if err := arq.Anexar([]byte(`{"seq":3}`)); err != nil {
		t.Fatalf("Anexar: %v", err)
	}
	arq.Fechar()

	arq, linhas, err = Abrir(caminho)
	if err != nil {
		t.Fatalf("reabrir depois do append: %v", err)
	}
	defer arq.Fechar()
	if len(linhas) != 3 {
		t.Errorf("linhas = %d, want 3", len(linhas))
	}
}

// Sem '\n' final a linha é incompleta mesmo que o JSON até pareça válido: a
// escrita é "json\n" num único write, e sem o fsync completo ninguém foi
// avisado de nada.
func TestAbrir_UltimaLinhaSemQuebraValidaTambemEDescartada(t *testing.T) {
	caminho := caminhoTemp(t)
	escrever(t, caminho, "{\"seq\":1}\n{\"seq\":2}")

	arq, linhas, err := Abrir(caminho)
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	defer arq.Fechar()
	if len(linhas) != 1 {
		t.Errorf("linhas = %d, want 1", len(linhas))
	}
}

// TP1 (parte 2): linha completa e inválida no meio é corrupção, não queda.
func TestAbrir_LinhaInvalidaNoMeioRecusa(t *testing.T) {
	caminho := caminhoTemp(t)
	escrever(t, caminho, "{\"seq\":1}\nisto não é json\n{\"seq\":3}\n")

	arq, _, err := Abrir(caminho)
	if err == nil {
		arq.Fechar()
		t.Fatal("Abrir aceitou log corrompido")
	}
	if !errors.Is(err, ErrLogCorrompido) {
		t.Errorf("erro = %v, want ErrLogCorrompido", err)
	}
	if !strings.Contains(err.Error(), "linha 2") || !strings.Contains(err.Error(), caminho) {
		t.Errorf("mensagem sem linha e caminho: %v", err)
	}
}

// Linha terminada em '\n' e inválida é corrupção também quando é a última:
// uma escrita interrompida não deixa o '\n' para trás.
func TestAbrir_UltimaLinhaCompletaEInvalidaRecusa(t *testing.T) {
	caminho := caminhoTemp(t)
	escrever(t, caminho, "{\"seq\":1}\nlixo\n")

	arq, _, err := Abrir(caminho)
	if err == nil {
		arq.Fechar()
		t.Fatal("Abrir aceitou linha completa e inválida")
	}
	if !errors.Is(err, ErrLogCorrompido) {
		t.Errorf("erro = %v, want ErrLogCorrompido", err)
	}
}

// A recusa não pode alterar o arquivo: o operador precisa inspecioná-lo.
func TestAbrir_RecusaNaoMexeNoArquivo(t *testing.T) {
	caminho := caminhoTemp(t)
	original := "{\"seq\":1}\nlixo\n{\"seq\":3,\"ti"
	escrever(t, caminho, original)

	if arq, _, err := Abrir(caminho); err == nil {
		arq.Fechar()
		t.Fatal("Abrir aceitou log corrompido")
	}
	if got := lerTudo(t, caminho); got != original {
		t.Errorf("arquivo alterado na recusa: %q", got)
	}
}
