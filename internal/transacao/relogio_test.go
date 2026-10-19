package transacao

import (
	"sync"
	"testing"
)

// Um relógio novo começa em zero, e ler não altera: o envio de uma mensagem
// não é um evento que avance o relógio (seção 8.1).
func TestAgora_NaoIncrementa(t *testing.T) {
	r := NovoRelogio()
	if got := r.Agora(); got != 0 {
		t.Fatalf("relógio novo = %d, want 0", got)
	}
	for i := 0; i < 3; i++ {
		if got := r.Agora(); got != 0 {
			t.Fatalf("leitura %d alterou o relógio: %d, want 0", i, got)
		}
	}
}

// A regra da seção 8.1: L ← max(L, recebido) + 1.
func TestAoReceber_MaxMaisUm(t *testing.T) {
	casos := []struct {
		nome          string
		antes         uint64
		recebido      uint64
		querResultado uint64
	}{
		{"recebido maior", 3, 10, 11},
		{"recebido igual", 7, 7, 8},
		// O caso que uma implementação distraída erra: com recebido menor, o
		// max não muda nada, mas o recebimento é um evento e o +1 continua
		// valendo. Sem ele, duas requisições recebidas em sequência teriam o
		// mesmo carimbo.
		{"recebido menor", 9, 2, 10},
		{"relógio novo", 0, 0, 1},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			r := NovoRelogio()
			r.Restaurar(c.antes)

			if got := r.AoReceber(c.recebido); got != c.querResultado {
				t.Errorf("AoReceber(%d) com L=%d devolveu %d, want %d", c.recebido, c.antes, got, c.querResultado)
			}
			if got := r.Agora(); got != c.querResultado {
				t.Errorf("L depois de AoReceber(%d) = %d, want %d", c.recebido, got, c.querResultado)
			}
		})
	}
}

// Restaurar é o caminho do replay (seção 4.8): leva o relógio ao maior valor
// do log e nunca o faz recuar.
func TestRestaurar_NuncaDiminui(t *testing.T) {
	r := NovoRelogio()

	r.Restaurar(57)
	if got := r.Agora(); got != 57 {
		t.Fatalf("Restaurar(57) deixou L=%d, want 57", got)
	}

	r.Restaurar(12)
	if got := r.Agora(); got != 57 {
		t.Fatalf("Restaurar(12) fez o relógio recuar para %d, want 57", got)
	}
}

// O relógio é tocado por uma goroutine por requisição REST, todas ao mesmo
// tempo. Com -race, este teste acusa qualquer acesso sem o mutex; o valor
// final acusa um incremento perdido, que uma leitura e uma escrita separadas
// produziriam.
func TestAoReceber_Concorrente(t *testing.T) {
	const goroutines = 50
	r := NovoRelogio()

	var espera sync.WaitGroup
	espera.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer espera.Done()
			r.AoReceber(0)
			r.Agora()
		}()
	}
	espera.Wait()

	if got := r.Agora(); got != goroutines {
		t.Fatalf("L = %d depois de %d recebimentos, want %d", got, goroutines, goroutines)
	}
}
