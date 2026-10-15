package dominio

import "testing"

// TestCatalogo_DezCidadesComAsUFsDaD33 confere o catálogo inteiro contra a
// tabela da D33, na ordem do menu (BA, ES, RJ).
func TestCatalogo_DezCidadesComAsUFsDaD33(t *testing.T) {
	esperado := []struct{ nome, uf string }{
		{"Salvador", "BA"},
		{"Feira de Santana", "BA"},
		{"Jequié", "BA"},
		{"Vitória da Conquista", "BA"},
		{"Vitória", "ES"},
		{"Cachoeiro de Itapemirim", "ES"},
		{"Marataízes", "ES"},
		{"Rio de Janeiro", "RJ"},
		{"Nilópolis", "RJ"},
		{"Petrópolis", "RJ"},
	}

	cidades := CidadesAtendidas()
	if len(cidades) != len(esperado) {
		t.Fatalf("CidadesAtendidas tem %d cidades, want %d: %v", len(cidades), len(esperado), cidades)
	}
	for i, want := range esperado {
		if cidades[i] != want.nome {
			t.Errorf("CidadesAtendidas()[%d] = %q, want %q", i, cidades[i], want.nome)
		}
		uf, ok := UFDaCidade(want.nome)
		if !ok || uf != want.uf {
			t.Errorf("UFDaCidade(%q) = (%q, %t), want (%q, true)", want.nome, uf, ok, want.uf)
		}
		if !CidadeConhecida(want.nome) {
			t.Errorf("CidadeConhecida(%q) = false, want true", want.nome)
		}
	}
}

// TestUFDaCidade_VitoriaEVitoriaDaConquistaSaoDistintas protege a razão de a
// UF existir (D33): o nome é a chave, e as duas "Vitória" não se confundem.
func TestUFDaCidade_VitoriaEVitoriaDaConquistaSaoDistintas(t *testing.T) {
	es, _ := UFDaCidade("Vitória")
	ba, _ := UFDaCidade("Vitória da Conquista")
	if es != "ES" || ba != "BA" {
		t.Errorf("Vitória = %q, Vitória da Conquista = %q, want ES e BA", es, ba)
	}
}

func TestUFDaCidade_CidadeDesconhecida(t *testing.T) {
	if uf, ok := UFDaCidade("Atlântida"); ok || uf != "" {
		t.Errorf("UFDaCidade(Atlântida) = (%q, %t), want (\"\", false)", uf, ok)
	}
	if CidadeConhecida("Vitoria") {
		t.Errorf("CidadeConhecida aceitou grafia sem acento")
	}
}

// TestCidadesAtendidas_DevolveCopia garante que o chamador não altera o
// catálogo por dentro.
func TestCidadesAtendidas_DevolveCopia(t *testing.T) {
	cidades := CidadesAtendidas()
	cidades[0] = "Outra"
	if CidadesAtendidas()[0] != "Salvador" {
		t.Errorf("alterar a fatia devolvida mudou o catálogo")
	}
}
