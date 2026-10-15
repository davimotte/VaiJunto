package servidor

import (
	"errors"
	"reflect"
	"testing"
)

// TestParsearPares_FormaValida confere a forma da D20: "empresa=url,...", sem
// a própria empresa. Lista vazia é válida: um servidor pode subir sozinho.
func TestParsearPares_FormaValida(t *testing.T) {
	casos := []struct {
		nome  string
		lista string
		want  map[string]string
	}{
		{"vazia", "", map[string]string{}},
		{"um par", "rotacapixaba=http://10.0.0.2:8080", map[string]string{"rotacapixaba": "http://10.0.0.2:8080"}},
		{"dois pares",
			"rotacapixaba=http://rotacapixaba:8080,rotafluminense=http://10.0.0.3:8080",
			map[string]string{"rotacapixaba": "http://rotacapixaba:8080", "rotafluminense": "http://10.0.0.3:8080"}},
		{"https", "rotafluminense=https://exemplo.com", map[string]string{"rotafluminense": "https://exemplo.com"}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got, err := ParsearPares("vaijunto", c.lista)
			if err != nil {
				t.Fatalf("ParsearPares(%q): %v", c.lista, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ParsearPares(%q) = %v, want %v", c.lista, got, c.want)
			}
		})
	}
}

func TestParsearPares_Recusa(t *testing.T) {
	casos := []struct {
		nome  string
		lista string
	}{
		{"a própria empresa", "vaijunto=http://localhost:8080,rotacapixaba=http://10.0.0.2:8080"},
		{"empresa repetida", "rotacapixaba=http://10.0.0.2:8080,rotacapixaba=http://10.0.0.9:8080"},
		{"URL sem esquema", "rotacapixaba=10.0.0.2:8080"},
		{"esquema que não é http", "rotacapixaba=ftp://10.0.0.2:8080"},
		{"URL sem host", "rotacapixaba=http://"},
		{"URL com espaço", "rotacapixaba=http://10.0.0 .2:8080"},
		{"URL vazia", "rotacapixaba="},
		{"sem igual", "rotacapixaba"},
		{"empresa vazia", "=http://10.0.0.2:8080"},
		{"nome de empresa inválido", "RotaCapixaba=http://10.0.0.2:8080"},
		{"vírgula sobrando", "rotacapixaba=http://10.0.0.2:8080,"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, err := ParsearPares("vaijunto", c.lista)
			if !errors.Is(err, ErrParesInvalidos) {
				t.Errorf("ParsearPares(%q) erro = %v, want ErrParesInvalidos", c.lista, err)
			}
		})
	}
}
