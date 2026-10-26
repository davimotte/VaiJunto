package simulador

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"vaijunto/internal/protocolo"
)

// resultadoDeTeste monta um resultado com dois pontos, para exercitar as
// linhas do CSV sem precisar de servidor.
func resultadoDeTeste() Resultado {
	ponto := func(passageiros int) Ponto {
		c := &coletor{}
		c.registrar(OpBuscar, protocolo.Resposta{Status: protocolo.StatusOK}, 2*time.Millisecond)
		c.registrar(OpReservar, protocolo.Resposta{Status: protocolo.StatusOK}, 30*time.Millisecond)
		c.registrar(OpReservar, protocolo.Resposta{
			Status: protocolo.StatusErro,
			Codigo: protocolo.CodigoSemAssento,
		}, 5*time.Millisecond)
		c.registrar(OpCancelar, protocolo.Resposta{Status: protocolo.StatusOK}, 8*time.Millisecond)
		return resumir(passageiros, map[string]int{"vaijunto": passageiros}, 2*time.Second, []*coletor{c})
	}
	return Resultado{
		Cenario: CenarioFederado,
		Rotulo:  "rede",
		Semente: 1,
		Pontos:  []Ponto{ponto(1), ponto(10)},
	}
}

// TestGravarCSVTemCabecalhoEUmaLinhaPorOperacao fixa o formato que o relatório
// e a planilha consomem, incluindo a linha `total`, que é a diretamente
// comparável com o CSV do P1.
func TestGravarCSVTemCabecalhoEUmaLinhaPorOperacao(t *testing.T) {
	cfg := configDeTeste(CenarioFederado)
	cfg.Saida = t.TempDir()
	resultado := resultadoDeTeste()

	arquivos, err := gravarCSV(cfg, resultado)
	if err != nil {
		t.Fatalf("gravarCSV: %v", err)
	}
	if len(arquivos) != 2 {
		t.Fatalf("gravou %d arquivo(s), queria 2", len(arquivos))
	}

	linhas := lerCSV(t, arquivos[0])
	esperadas := 1 + len(resultado.Pontos)*(len(operacoesMedidas)+1)
	if len(linhas) != esperadas {
		t.Fatalf("CSV de latências com %d linhas, queria %d", len(linhas), esperadas)
	}
	conferirCabecalho(t, linhas[0], cabecalhoLatencias)

	// A linha do total de um ponto soma as requisições das operações dele.
	soma, total := 0, -1
	for _, linha := range linhas[1 : len(operacoesMedidas)+2] {
		if linha[3] == OpTotal {
			total = atoi(t, linha[5])
			continue
		}
		soma += atoi(t, linha[5])
	}
	if total != soma {
		t.Errorf("linha total com %d requisições, e as operações somam %d", total, soma)
	}

	codigos := lerCSV(t, arquivos[1])
	conferirCabecalho(t, codigos[0], cabecalhoCodigos)
	// A linha `total` não aparece no CSV de códigos: todo código dela já está
	// em alguma operação, e repeti-la confundiria quem soma a coluna.
	for _, linha := range codigos[1:] {
		if linha[3] == OpTotal {
			t.Errorf("CSV de códigos trouxe a operação %s", OpTotal)
		}
	}
	if !temLinha(codigos, protocolo.CodigoSemAssento) || !temLinha(codigos, protocolo.StatusOK) {
		t.Errorf("CSV de códigos sem %s ou sem %s: %v", protocolo.CodigoSemAssento, protocolo.StatusOK, codigos)
	}
}

// TestNomeBaseOmiteORotuloRedundante: sem isso, a rodada padrão sairia como
// simulador-federado-federado-<instante>.
func TestNomeBaseOmiteORotuloRedundante(t *testing.T) {
	if nome := nomeBase("federado", "federado", "20261026-120000"); nome != "simulador-federado-20261026-120000" {
		t.Errorf("nomeBase com rótulo igual ao cenário = %q", nome)
	}
	if nome := nomeBase("local", "rede", "20261026-120000"); nome != "simulador-local-rede-20261026-120000" {
		t.Errorf("nomeBase com rótulo próprio = %q", nome)
	}
}

func TestImprimirTabelaMostraOsDoisBlocos(t *testing.T) {
	var saida strings.Builder
	imprimirTabela(&saida, resultadoDeTeste())
	texto := saida.String()

	for _, esperado := range []string{"p95 (ms)", "código", OpTotal, OpReservar, protocolo.CodigoSemAssento, "cenário federado"} {
		if !strings.Contains(texto, esperado) {
			t.Errorf("a tabela não mostra %q:\n%s", esperado, texto)
		}
	}

	// Registro nil não imprime e não quebra: é como o teste em processo roda.
	imprimirTabela(nil, resultadoDeTeste())
}

func lerCSV(t *testing.T, caminho string) [][]string {
	t.Helper()
	arquivo, err := os.Open(caminho)
	if err != nil {
		t.Fatalf("abrir %s: %v", caminho, err)
	}
	defer func() { _ = arquivo.Close() }()

	linhas, err := csv.NewReader(arquivo).ReadAll()
	if err != nil {
		t.Fatalf("ler %s: %v", caminho, err)
	}
	if len(linhas) == 0 {
		t.Fatalf("%s está vazio", filepath.Base(caminho))
	}
	return linhas
}

func conferirCabecalho(t *testing.T, obtido, esperado []string) {
	t.Helper()
	if len(obtido) != len(esperado) {
		t.Fatalf("cabeçalho com %d colunas, queria %d: %v", len(obtido), len(esperado), obtido)
	}
	for i := range esperado {
		if obtido[i] != esperado[i] {
			t.Errorf("coluna %d é %q, queria %q", i, obtido[i], esperado[i])
		}
	}
}

func temLinha(linhas [][]string, codigo string) bool {
	for _, linha := range linhas[1:] {
		if linha[4] == codigo {
			return true
		}
	}
	return false
}

func atoi(t *testing.T, texto string) int {
	t.Helper()
	n, err := strconv.Atoi(texto)
	if err != nil {
		t.Fatalf("converter %q: %v", texto, err)
	}
	return n
}
