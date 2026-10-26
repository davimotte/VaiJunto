package simulador

import (
	"testing"

	"vaijunto/internal/dominio"
)

// TestParSorteadoNuncaRepeteACidade: origem igual ao destino é recusada com
// ROTA_INVALIDA (PROTOCOL.md, seção 6), que é defeito do cliente e não
// resultado da carga.
func TestParSorteadoNuncaRepeteACidade(t *testing.T) {
	cidades := dominio.CidadesAtendidas()
	sorteio := sorteioDoPassageiro(7, 0)

	vistas := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		origem, destino := parSorteado(sorteio, cidades)
		if origem == destino {
			t.Fatalf("sorteio %d devolveu %s nas duas pontas", i, origem)
		}
		if !dominio.CidadeConhecida(origem) || !dominio.CidadeConhecida(destino) {
			t.Fatalf("sorteio %d devolveu cidade fora do catálogo: %s → %s", i, origem, destino)
		}
		vistas[origem] = true
		vistas[destino] = true
	}

	// Em mil sorteios, as dez cidades do catálogo precisam ter aparecido: um
	// sorteio que só cobre metade do catálogo esconde metade das buscas vazias.
	if len(vistas) != len(cidades) {
		t.Errorf("mil sorteios cobriram %d das %d cidades", len(vistas), len(cidades))
	}
}

// TestMesmaSementeMesmoSorteio é a garantia de rodada reproduzível da seção
// 11.4. O que a semente fixa é a sequência de escolhas de cada passageiro; o
// entrelaçamento entre eles é do escalonador.
func TestMesmaSementeMesmoSorteio(t *testing.T) {
	cidades := dominio.CidadesAtendidas()

	escolhas := func(semente int64, indice int) []string {
		sorteio := sorteioDoPassageiro(semente, indice)
		var sequencia []string
		for i := 0; i < 20; i++ {
			origem, destino := parSorteado(sorteio, cidades)
			sequencia = append(sequencia, origem+"→"+destino)
		}
		return sequencia
	}

	primeira := escolhas(42, 3)
	segunda := escolhas(42, 3)
	for i := range primeira {
		if primeira[i] != segunda[i] {
			t.Fatalf("escolha %d divergiu entre duas rodadas com a mesma semente: %s e %s",
				i, primeira[i], segunda[i])
		}
	}

	// Índices diferentes não podem sortear igual: se sorteassem, todos os
	// passageiros disputariam sempre o mesmo dia, e a carga mediria um só.
	outro := escolhas(42, 4)
	iguais := 0
	for i := range primeira {
		if primeira[i] == outro[i] {
			iguais++
		}
	}
	if iguais == len(primeira) {
		t.Errorf("os passageiros de índice 3 e 4 fizeram as mesmas %d escolhas", iguais)
	}

	// Semente diferente, sorteio diferente.
	terceira := escolhas(43, 3)
	iguais = 0
	for i := range primeira {
		if primeira[i] == terceira[i] {
			iguais++
		}
	}
	if iguais == len(primeira) {
		t.Errorf("sementes 42 e 43 produziram as mesmas %d escolhas", iguais)
	}
}
