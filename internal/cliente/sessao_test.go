package cliente

import (
	"strings"
	"testing"
	"time"

	"vaijunto/internal/dominio"
)

// TestRotaDoCorredor confere que a rota derivada no cliente é a mesma que o
// servidor deriva. Ela só rotula as perguntas de preço e fixa quantos preços
// a requisição leva — mas se divergisse da do servidor, a publicação voltaria
// como ROTA_INVALIDA por contagem de preços.
func TestRotaDoCorredor(t *testing.T) {
	casos := []struct {
		nome     string
		origem   string
		destino  string
		esperado []string
	}{
		{
			nome:     "corredor inteiro",
			origem:   "Salvador",
			destino:  "Vitória da Conquista",
			esperado: []string{"Salvador", "Feira de Santana", "Jequié", "Vitória da Conquista"},
		},
		{
			nome:     "sentido inverso",
			origem:   "Vitória da Conquista",
			destino:  "Salvador",
			esperado: []string{"Vitória da Conquista", "Jequié", "Feira de Santana", "Salvador"},
		},
		{
			nome:     "cidades vizinhas",
			origem:   "Feira de Santana",
			destino:  "Jequié",
			esperado: []string{"Feira de Santana", "Jequié"},
		},
		{
			nome:     "trecho do meio no sentido inverso",
			origem:   "Jequié",
			destino:  "Feira de Santana",
			esperado: []string{"Jequié", "Feira de Santana"},
		},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			rota := RotaDoCorredor(caso.origem, caso.destino)
			if len(rota) != len(caso.esperado) {
				t.Fatalf("rota = %v, want %v", rota, caso.esperado)
			}
			for i, cidade := range caso.esperado {
				if rota[i] != cidade {
					t.Errorf("rota[%d] = %q, want %q", i, rota[i], cidade)
				}
			}
		})
	}
}

// TestRotaDoCorredor_BateComADerivacaoDoServidor é a razão de o cliente usar a
// tabela do domínio em vez de manter uma lista própria: as duas derivações
// precisam concordar em cidade e em contagem de trechos.
func TestRotaDoCorredor_BateComADerivacaoDoServidor(t *testing.T) {
	cidades := dominio.CidadesCorredor()

	for _, origem := range cidades {
		for _, destino := range cidades {
			if origem == destino {
				continue
			}
			doCliente := RotaDoCorredor(origem, destino)
			doServidor, _, err := dominio.DerivarRotaEHorarios(origem, destino,
				time.Date(2026, 9, 15, 8, 0, 0, 0, fusoDoCorredorFixo))
			if err != nil {
				t.Fatalf("%s → %s: %v", origem, destino, err)
			}

			if len(doCliente) != len(doServidor) {
				t.Fatalf("%s → %s: cliente derivou %d cidades, servidor derivou %d",
					origem, destino, len(doCliente), len(doServidor))
			}
			for i := range doServidor {
				if doCliente[i] != doServidor[i] {
					t.Errorf("%s → %s: cidade %d = %q no cliente e %q no servidor",
						origem, destino, i, doCliente[i], doServidor[i])
				}
			}
		}
	}
}

// TestRotaDoCorredor_CidadeForaDoCorredor: sem rota, o menu não tem preço a
// perguntar. As cidades do cliente vêm sempre do menu enumerado, então este é
// um caminho defensivo — que precisa devolver vazio, e não entrar em laço.
func TestRotaDoCorredor_CidadeForaDoCorredor(t *testing.T) {
	if rota := RotaDoCorredor("Ilhéus", "Salvador"); rota != nil {
		t.Errorf("rota = %v, want nil", rota)
	}
	if rota := RotaDoCorredor("Salvador", "Ilhéus"); rota != nil {
		t.Errorf("rota = %v, want nil", rota)
	}
}

// TestFusoDoCorredor confere que os instantes montados pelo cliente saem no
// deslocamento do corredor.
//
// Vale com ou sem tzdata na máquina: se LoadLocation falhar, o deslocamento
// fixo responde o mesmo -03:00. O que não pode acontecer é o cliente cair em
// UTC, que é o fuso local dentro do contêiner Alpine.
func TestFusoDoCorredor(t *testing.T) {
	fuso := FusoDoCorredor()

	instante := time.Date(2026, 9, 15, 8, 0, 0, 0, fuso)
	if _, deslocamento := instante.Zone(); deslocamento != -3*60*60 {
		t.Errorf("deslocamento em 15/09/2026 = %d s, want %d s", deslocamento, -3*60*60)
	}
	if got := instante.Format(time.RFC3339); got != "2026-09-15T08:00:00-03:00" {
		t.Errorf("instante = %s, want 2026-09-15T08:00:00-03:00", got)
	}

	// Em janeiro também: a Bahia não observa horário de verão, e um fuso que
	// mudasse de deslocamento faria a carona publicada em dezembro sair uma
	// hora fora.
	verao := time.Date(2027, 1, 15, 8, 0, 0, 0, fuso)
	if got := verao.Format(time.RFC3339); got != "2027-01-15T08:00:00-03:00" {
		t.Errorf("instante = %s, want 2027-01-15T08:00:00-03:00", got)
	}
}

// TestEscolherCidade confere que a cidade sai com a grafia canônica do
// corredor. A seção 3 do PROTOCOL.md compara cidade por igualdade exata e não
// normaliza grafia: é o menu enumerado que garante a string correta.
func TestEscolherCidade(t *testing.T) {
	term, saida := terminalDeTeste("4\n")

	cidade, err := EscolherCidade(term, "Destino:")
	if err != nil {
		t.Fatalf("EscolherCidade: %v", err)
	}
	if cidade != "Vitória da Conquista" {
		t.Errorf("cidade = %q, want %q", cidade, "Vitória da Conquista")
	}
	if _, ok := dominio.IndiceCidade(cidade); !ok {
		t.Errorf("cidade %q não é reconhecida pelo corredor", cidade)
	}

	// Todas as cidades do corredor precisam estar no menu, ou uma delas ficaria
	// inalcançável pelo cliente.
	for _, esperada := range dominio.CidadesCorredor() {
		if !strings.Contains(saida.String(), esperada) {
			t.Errorf("cidade %q ausente do menu:\n%s", esperada, saida.String())
		}
	}
}
