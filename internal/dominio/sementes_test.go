package dominio

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// empresasDoConsorcio são as três empresas da seção 1.3 do PROJETO_P2.md, com a
// semente que cada uma carrega no boot (seções 12.1 e 12.2).
var empresasDoConsorcio = []struct {
	nome           string
	motoristas     []string
	passageiros    []string
	caronasEsperas []string
}{
	{"vaijunto", []string{"joao", "carlos", "ana"}, []string{"maria", "pedro", "lucia"},
		[]string{"vaijunto:car-1", "vaijunto:car-2"}},
	{"rotacapixaba", []string{"rafael", "beatriz", "gustavo"}, []string{"paulo", "clara", "renata"},
		[]string{"rotacapixaba:car-1", "rotacapixaba:car-2"}},
	{"rotafluminense", []string{"marcos", "fernanda", "roberto"}, []string{"tiago", "sofia", "julia"},
		[]string{"rotafluminense:car-1", "rotafluminense:car-2"}},
}

func diretorioDaSemente(empresa string) string {
	return filepath.Join("..", "..", "dados", empresa)
}

// TestSementes_CarregamSemErroComOPrefixoDaPropriaEmpresa confere o que o
// boot exige (D27): toda carona da semente leva o prefixo da empresa dona.
func TestSementes_CarregamSemErroComOPrefixoDaPropriaEmpresa(t *testing.T) {
	for _, emp := range empresasDoConsorcio {
		t.Run(emp.nome, func(t *testing.T) {
			dir := diretorioDaSemente(emp.nome)
			estado, err := lerEstado(emp.nome, filepath.Join(dir, "usuarios.json"), filepath.Join(dir, "caronas.json"))
			if err != nil {
				t.Fatalf("CarregarEstado(%s): %v", emp.nome, err)
			}
			if len(estado.caronas) != len(emp.caronasEsperas) {
				t.Errorf("%s tem %d caronas, want %d", emp.nome, len(estado.caronas), len(emp.caronasEsperas))
			}
			for _, id := range emp.caronasEsperas {
				if _, ok := estado.caronas[id]; !ok {
					t.Errorf("%s não tem a carona %s", emp.nome, id)
				}
			}
			for id := range estado.caronas {
				if dona, err := EmpresaDoID(id); err != nil || dona != emp.nome {
					t.Errorf("carona %q na semente de %s: empresa %q, err %v", id, emp.nome, dona, err)
				}
			}
		})
	}
}

// TestSementes_UsuariosDaSecao12_1 confere nomes, perfis e senhas da tabela:
// 3 motoristas (1234), 3 passageiros (abcd) e teste01..teste50 (teste), com
// nome completo inventado em todos.
func TestSementes_UsuariosDaSecao12_1(t *testing.T) {
	for _, emp := range empresasDoConsorcio {
		t.Run(emp.nome, func(t *testing.T) {
			usuarios, err := lerUsuarios(filepath.Join(diretorioDaSemente(emp.nome), "usuarios.json"))
			if err != nil {
				t.Fatal(err)
			}
			if want := 6 + 50; len(usuarios) != want {
				t.Errorf("%d usuários, want %d", len(usuarios), want)
			}
			confere := func(nome, perfil, senha string) {
				u, ok := usuarios[nome]
				if !ok {
					t.Errorf("falta o usuário %q", nome)
					return
				}
				if u.Perfil != perfil || u.Senha != senha {
					t.Errorf("%s = (%s, %q), want (%s, %q)", nome, u.Perfil, u.Senha, perfil, senha)
				}
			}
			for _, m := range emp.motoristas {
				confere(m, "MOTORISTA", "1234")
			}
			for _, p := range emp.passageiros {
				confere(p, "PASSAGEIRO", "abcd")
			}
			for i := 1; i <= 50; i++ {
				confere("teste"+twoDigits(i), "PASSAGEIRO", "teste")
			}
			for nome, u := range usuarios {
				// "Nome completo": ao menos nome e sobrenome.
				if len(strings.Fields(u.Nome)) < 2 {
					t.Errorf("%s: nome %q não é completo", nome, u.Nome)
				}
			}
		})
	}
}

func twoDigits(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

// TestSementes_CidadesNoCatalogoEInvarianteI6 confere cada carona contra o
// catálogo (D33) e contra as regras da publicação (I6), sem depender de que o
// carregador as aplique.
func TestSementes_CidadesNoCatalogoEInvarianteI6(t *testing.T) {
	for _, emp := range empresasDoConsorcio {
		caronas, err := lerCaronas(emp.nome, filepath.Join(diretorioDaSemente(emp.nome), "caronas.json"))
		if err != nil {
			t.Fatal(err)
		}
		for id, c := range caronas {
			for _, cidade := range c.Rota {
				if !CidadeConhecida(cidade) {
					t.Errorf("%s: cidade %q fora do catálogo", id, cidade)
				}
			}
			if err := validarCarona(c.Rota, c.Horarios, c.Assentos, c.PrecoTrecho); err != nil {
				t.Errorf("%s viola a I6: %v", id, err)
			}
		}
	}
}

// TestSementes_DatasFusoEPrecos confere o que a seção 12.2 fixa: tudo em 05 e
// 06/11/2026, com deslocamento -03:00 explícito, e os preços em centavos.
func TestSementes_DatasFusoEPrecos(t *testing.T) {
	precos := map[string][]int{
		"vaijunto:car-1":       {2500, 7000},
		"vaijunto:car-2":       {3000},
		"rotacapixaba:car-1":   {18000, 3500},
		"rotacapixaba:car-2":   {3500, 1500},
		"rotafluminense:car-1": {9000, 3000},
		"rotafluminense:car-2": {8500, 1500},
	}
	assentos := map[string]int{
		"vaijunto:car-1": 3, "vaijunto:car-2": 2,
		"rotacapixaba:car-1": 3, "rotacapixaba:car-2": 1,
		"rotafluminense:car-1": 2, "rotafluminense:car-2": 1,
	}
	inicio := time.Date(2026, 11, 5, 0, 0, 0, 0, time.FixedZone("-03", -3*3600))
	fim := inicio.Add(48 * time.Hour)

	for _, emp := range empresasDoConsorcio {
		caronas, err := lerCaronas(emp.nome, filepath.Join(diretorioDaSemente(emp.nome), "caronas.json"))
		if err != nil {
			t.Fatal(err)
		}
		for id, c := range caronas {
			if want := assentos[id]; c.Assentos != want {
				t.Errorf("%s: %d assentos, want %d", id, c.Assentos, want)
			}
			if want := precos[id]; !igual(c.PrecoTrecho, want) {
				t.Errorf("%s: preços %v, want %v", id, c.PrecoTrecho, want)
			}
			for _, h := range c.Horarios {
				if _, desloc := h.Zone(); desloc != -3*3600 {
					t.Errorf("%s: horário %s sem deslocamento -03:00", id, h)
				}
				if h.Before(inicio) || !h.Before(fim) {
					t.Errorf("%s: horário %s fora de 05 e 06/11/2026", id, h)
				}
			}
		}
	}
}

func igual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSementes_FolgasDeBaldeacaoDaSecao12_2 confere a cadeia de pernas que a
// busca federada vai montar: cada conexão respeita a margem e o teto de espera,
// e a carona de fernanda NÃO serve de conexão depois de marcos.
func TestSementes_FolgasDeBaldeacaoDaSecao12_2(t *testing.T) {
	carregar := func(empresa string) map[string]*Carona {
		c, err := lerCaronas(empresa, filepath.Join(diretorioDaSemente(empresa), "caronas.json"))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	vj, rc, rf := carregar("vaijunto"), carregar("rotacapixaba"), carregar("rotafluminense")

	// instante devolve o horário da parada de uma carona.
	instante := func(c *Carona, cidade string) time.Time {
		for i, p := range c.Rota {
			if p == cidade {
				return c.Horarios[i]
			}
		}
		t.Fatalf("%s não passa por %s", c.ID, cidade)
		return time.Time{}
	}
	conexao := func(de *Carona, noDe string, para *Carona, noPara string, folga time.Duration) {
		t.Helper()
		got := instante(para, noPara).Sub(instante(de, noDe))
		if got != folga {
			t.Errorf("%s → %s em %s: folga %v, want %v", de.ID, para.ID, noPara, got, folga)
		}
		if got < MARGEM_BALDEACAO || got > ESPERA_MAXIMA_BALDEACAO {
			t.Errorf("%s → %s em %s: folga %v fora de [margem, teto]", de.ID, para.ID, noPara, got)
		}
	}
	conexao(vj["vaijunto:car-1"], "Vitória da Conquista", rc["rotacapixaba:car-1"], "Vitória da Conquista", 90*time.Minute)
	conexao(rc["rotacapixaba:car-1"], "Cachoeiro de Itapemirim", rf["rotafluminense:car-1"], "Cachoeiro de Itapemirim", 90*time.Minute)
	conexao(rc["rotacapixaba:car-2"], "Cachoeiro de Itapemirim", rf["rotafluminense:car-2"], "Cachoeiro de Itapemirim", 60*time.Minute)

	// A perna de fernanda chega ao Rio depois que marcos já passou por lá, então
	// não serve de continuação para Petrópolis (seção 12.2).
	if !instante(rf["rotafluminense:car-2"], "Rio de Janeiro").After(instante(rf["rotafluminense:car-1"], "Rio de Janeiro")) {
		t.Error("fernanda deveria chegar ao Rio depois de marcos")
	}
}
