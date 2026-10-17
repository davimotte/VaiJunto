package dominio

import (
	"errors"
	"regexp"
	"testing"
	"time"
)

// TestEmpresaDoID cobre D27: o prefixo responde quem é o dono, e ID sem
// prefixo é rejeitado em vez de assumir a empresa local.
func TestEmpresaDoID(t *testing.T) {
	casos := []struct {
		id      string
		empresa string
		valido  bool
	}{
		{"vaijunto:car-1", "vaijunto", true},
		{"rotacapixaba:car-91c2a07e", "rotacapixaba", true},
		{"vaijunto:res-3f0b11c4", "vaijunto", true},
		{"car-1", "", false},
		{"", "", false},
		{":car-1", "", false},
		{"vaijunto:", "", false},
	}
	for _, c := range casos {
		empresa, err := EmpresaDoID(c.id)
		if c.valido {
			if err != nil || empresa != c.empresa {
				t.Errorf("EmpresaDoID(%q) = (%q, %v), want (%q, nil)", c.id, empresa, err, c.empresa)
			}
			continue
		}
		if !errors.Is(err, ErrIDSemEmpresa) {
			t.Errorf("EmpresaDoID(%q) erro = %v, want ErrIDSemEmpresa", c.id, err)
		}
	}
}

func TestValidarEmpresa(t *testing.T) {
	validas := []string{"vaijunto", "rotacapixaba", "rotafluminense", "empresa2", "a-b"}
	for _, nome := range validas {
		if err := ValidarEmpresa(nome); err != nil {
			t.Errorf("ValidarEmpresa(%q) = %v, want nil", nome, err)
		}
	}
	invalidas := []string{"", "VaiJunto", "vai:junto", "vai junto", "vaijunto\n", "1empresa", "-x"}
	for _, nome := range invalidas {
		if err := ValidarEmpresa(nome); !errors.Is(err, ErrEmpresaInvalida) {
			t.Errorf("ValidarEmpresa(%q) = %v, want ErrEmpresaInvalida", nome, err)
		}
	}
}

// TestIDsGerados_LevamPrefixoDaEmpresa confere que carona e reserva criadas
// pelo protocolo recebem o prefixo da empresa configurada, e não um fixo.
func TestIDsGerados_LevamPrefixoDaEmpresa(t *testing.T) {
	e := NovoEstado("rotacapixaba")
	e.usuarios["rafael"] = &Usuario{Usuario: "rafael", Perfil: "MOTORISTA"}
	e.usuarios["maria"] = &Usuario{Usuario: "maria", Perfil: "PASSAGEIRO"}

	agora := time.Date(2026, 9, 1, 8, 0, 0, 0, fusoBrasilia())
	partida := agora.Add(48 * time.Hour)
	carona, err := e.PublicarCarona("rafael",
		[]string{"Vitória", "Cachoeiro de Itapemirim"},
		[]time.Time{partida, partida.Add(2 * time.Hour)}, 2, []int{3000}, agora)
	if err != nil {
		t.Fatalf("PublicarCarona: %v", err)
	}
	if !regexp.MustCompile(`^rotacapixaba:car-[0-9a-f]{8}$`).MatchString(carona.ID) {
		t.Errorf("ID da carona = %q, want rotacapixaba:car-<8 hex>", carona.ID)
	}

	reserva, _, err := e.Reservar("maria", []ItemReserva{{CaronaID: carona.ID, De: 0, Ate: 1}}, agora)
	if err != nil {
		t.Fatalf("Reservar: %v", err)
	}
	if !regexp.MustCompile(`^rotacapixaba:res-[0-9a-f]{8}$`).MatchString(reserva.ID) {
		t.Errorf("ID da reserva = %q, want rotacapixaba:res-<8 hex>", reserva.ID)
	}
}

// TestCarregarCaronas_RecusaIDForaDaEmpresa confere que a semente não pode
// trazer carona de outra empresa nem ID sem prefixo (D27): o prefixo é o que
// diz quem é o dono do trecho.
func TestCarregarCaronas_RecusaIDForaDaEmpresa(t *testing.T) {
	rota := []string{"Salvador", "Feira de Santana"}
	horarios := []string{"2026-10-01T06:00:00-03:00", "2026-10-01T07:45:00-03:00"}

	for _, id := range []string{"car-1", "rotacapixaba:car-1"} {
		caminho := escreverCaronas(t, caronaDeArquivo(id, rota, horarios, 2, []int{2500}))
		if _, err := lerCaronas("vaijunto", caminho); err == nil {
			t.Errorf("CarregarCaronas aceitou o ID %q numa semente da vaijunto", id)
		}
	}

	caminho := escreverCaronas(t, caronaDeArquivo("vaijunto:car-1", rota, horarios, 2, []int{2500}))
	if _, err := lerCaronas("vaijunto", caminho); err != nil {
		t.Errorf("CarregarCaronas recusou vaijunto:car-1: %v", err)
	}
}
