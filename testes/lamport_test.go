package testes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/persistencia"
	"vaijunto/internal/servidor"
	"vaijunto/internal/transacao"
)

// subirComRelogio repete o boot de cmd/servidor inteiro, incluindo o relógio
// de Lamport: semente, relógio injetado ANTES de qualquer gravação, arquivo
// aberto, replay e Restaurar com o maior valor do log (D24, seção 4.8).
//
// É o caminho que main.go percorre; um teste que montasse o estado de outra
// forma não provaria nada sobre o boot real.
func subirComRelogio(t *testing.T, caminho string) (*dominio.Estado, *transacao.Relogio, *persistencia.Arquivo) {
	t.Helper()

	estado, err := servidor.CarregarSemente("vaijunto", diretorioSemente)
	if err != nil {
		t.Fatalf("semente: %v", err)
	}

	relogio := transacao.NovoRelogio()
	estado.UsarRelogio(relogio.Agora)

	arq, registros, err := persistencia.Abrir(caminho)
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	maiorLamport, err := estado.Reaplicar(registros)
	if err != nil {
		t.Fatalf("Reaplicar: %v", err)
	}
	relogio.Restaurar(maiorLamport)
	estado.UsarLog(arq)

	return estado, relogio, arq
}

// O relógio sobrevive ao reinício: cada registro vai ao log com o L do
// momento da gravação (seção 4.8), e o boot seguinte volta no maior deles.
//
// Sem isto, um servidor religado reemitiria carimbos já usados: duas
// transações distintas apareceriam como simultâneas na ordem total da
// seção 8.1, e a precedência por wait-die (fase 7) decidiria a disputa de
// formas diferentes em servidores diferentes.
func TestReinicioRestauraORelogioDeLamport(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "vaijunto.wal")

	antes, relogio, arq := subirComRelogio(t, caminho)

	// Mensagens recebidas de pares antes da publicação: é o que move o relógio
	// na vida real (seção 8.1). Depois de receber 56, L = 57.
	relogio.AoReceber(56)
	if got := relogio.Agora(); got != 57 {
		t.Fatalf("L = %d depois de receber 56, want 57", got)
	}

	amanha := time.Now().Add(24 * time.Hour)
	publicada, err := antes.PublicarCarona("joao", []string{"Salvador", "Feira de Santana"},
		[]time.Time{amanha, amanha.Add(90 * time.Minute)}, 2, []int{1500}, time.Now())
	if err != nil {
		t.Fatalf("PublicarCarona: %v", err)
	}
	arq.Fechar()

	// A linha gravada carrega o carimbo, e não um zero fixo.
	if got := lamportDoRegistro(t, caminho, publicada.ID); got != 57 {
		t.Errorf("lamport do registro = %d, want 57", got)
	}

	// Religa: o relógio volta no maior valor do log.
	_, depois, arq2 := subirComRelogio(t, caminho)
	defer arq2.Fechar()

	if got := depois.Agora(); got != 57 {
		t.Errorf("L depois do reinício = %d, want 57", got)
	}
	// E continua de onde parou: a próxima mensagem recebida segue a partir dele.
	if got := depois.AoReceber(1); got != 58 {
		t.Errorf("L do primeiro recebimento após o reinício = %d, want 58", got)
	}
}

// lamportDoRegistro devolve o campo lamport da linha que gravou a carona.
func lamportDoRegistro(t *testing.T, caminho, caronaID string) uint64 {
	t.Helper()

	bruto, err := os.ReadFile(caminho)
	if err != nil {
		t.Fatalf("ler %s: %v", caminho, err)
	}

	for _, linha := range strings.Split(strings.TrimRight(string(bruto), "\n"), "\n") {
		var env struct {
			Lamport uint64 `json:"lamport"`
			Dados   struct {
				Carona string `json:"carona"`
			} `json:"dados"`
		}
		if err := json.Unmarshal([]byte(linha), &env); err != nil {
			t.Fatalf("linha ilegível %q: %v", linha, err)
		}
		if env.Dados.Carona == caronaID {
			return env.Lamport
		}
	}
	t.Fatalf("nenhum registro da carona %s em %s", caronaID, caminho)
	return 0
}
