package federacao

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vaijunto/internal/dominio"
)

// GET /saude responde o corpo da seção 4.1 da API (D30).
func TestSaude_CorpoDaSecao41(t *testing.T) {
	cfg := configDeTeste()

	rec := pedir(cfg, httptest.NewRequest(http.MethodGet, "/saude", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var corpo Saude
	if err := json.Unmarshal(rec.Body.Bytes(), &corpo); err != nil {
		t.Fatalf("corpo ilegível (%q): %v", rec.Body.String(), err)
	}
	if corpo.Empresa != cfg.Empresa {
		t.Errorf("empresa = %q, want %q", corpo.Empresa, cfg.Empresa)
	}
	if corpo.VersaoAPI != VersaoAPI {
		t.Errorf("versao_api = %d, want %d", corpo.VersaoAPI, VersaoAPI)
	}
	if desvio := time.Since(corpo.Instante); desvio < 0 || desvio > time.Minute {
		t.Errorf("instante = %s, longe demais de agora", corpo.Instante)
	}
}

// O instante sai com deslocamento explícito, no fuso das cidades, e nunca em
// UTC: dentro do contêiner Alpine o fuso local é UTC, e a conferência no
// laboratório mostraria 3 h a mais que os horários das caronas.
func TestSaude_InstanteComDeslocamentoDasCidades(t *testing.T) {
	rec := pedir(configDeTeste(), httptest.NewRequest(http.MethodGet, "/saude", nil))

	var bruto struct {
		Instante string `json:"instante"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &bruto); err != nil {
		t.Fatalf("corpo ilegível: %v", err)
	}

	instante, err := time.Parse(time.RFC3339, bruto.Instante)
	if err != nil {
		t.Fatalf("instante %q não é RFC 3339: %v", bruto.Instante, err)
	}

	_, desejado := time.Now().In(dominio.FusoDasCidades()).Zone()
	if _, got := instante.Zone(); got != desejado {
		t.Errorf("deslocamento do instante = %d s, want %d s (%q)", got, desejado, bruto.Instante)
	}
}
