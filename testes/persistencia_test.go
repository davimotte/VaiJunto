package testes

import (
	"path/filepath"
	"testing"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/persistencia"
	"vaijunto/internal/servidor"
)

// subirComLog repete o caminho do boot de cmd/servidor: semente, arquivo,
// replay e só então o arquivo como destino do log (D37, seção 9.2).
func subirComLog(t *testing.T, caminho string) (*dominio.Estado, *persistencia.Arquivo) {
	t.Helper()
	estado, err := servidor.CarregarSemente("vaijunto", diretorioSemente)
	if err != nil {
		t.Fatalf("semente: %v", err)
	}
	arq, registros, err := persistencia.Abrir(caminho)
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	if err := estado.Reaplicar(registros); err != nil {
		t.Fatalf("Reaplicar: %v", err)
	}
	estado.UsarLog(arq)
	return estado, arq
}

// Publicar e cancelar com o arquivo real, "derrubar" (abandonar o estado) e
// subir de novo: a carona publicada volta com o mesmo ID e a cancelada
// continua cancelada.
func TestReinicioPreservaCaronas(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "vaijunto.wal")

	antes, arq := subirComLog(t, caminho)
	amanha := time.Now().Add(24 * time.Hour)
	publicada, err := antes.PublicarCarona("joao", []string{"Salvador", "Feira de Santana"},
		[]time.Time{amanha, amanha.Add(90 * time.Minute)}, 4, []int{2000}, time.Now())
	if err != nil {
		t.Fatalf("PublicarCarona: %v", err)
	}
	outra, err := antes.PublicarCarona("joao", []string{"Salvador", "Jequié"},
		[]time.Time{amanha, amanha.Add(4 * time.Hour)}, 2, []int{5000}, time.Now())
	if err != nil {
		t.Fatalf("PublicarCarona: %v", err)
	}
	if _, err := antes.CancelarCarona(outra.ID, "joao", time.Now()); err != nil {
		t.Fatalf("CancelarCarona: %v", err)
	}
	arq.Fechar()

	depois, arq := subirComLog(t, caminho)
	defer arq.Fechar()

	volta, _, err := depois.DetalharCarona(publicada.ID, "joao")
	if err != nil || volta.Cancelada {
		t.Errorf("carona %s: err=%v, cancelada=%v", publicada.ID, err, volta.Cancelada)
	}
	cancelada, _, err := depois.DetalharCarona(outra.ID, "joao")
	if err != nil || !cancelada.Cancelada {
		t.Errorf("carona %s deveria continuar cancelada: err=%v, %+v", outra.ID, err, cancelada)
	}
}
