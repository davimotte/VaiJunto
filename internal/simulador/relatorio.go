package simulador

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// cabecalhoLatencias e cabecalhoCodigos são as colunas dos dois CSVs da seção
// 11.4.
//
// As sete primeiras colunas de latência são as do CSV do P1 (PROJETO.md, seção
// 8.3), com os mesmos nomes e as mesmas casas decimais, e as colunas novas são
// `cenario` e `operacao`. É o que permite abrir as duas gerações de CSV na
// mesma planilha: a linha de operação `total` do P2 é diretamente comparável à
// linha única de um ponto do P1.
var (
	cabecalhoLatencias = []string{"rotulo", "cenario", "passageiros", "operacao", "duracao_s",
		"requisicoes", "vazao_req_s", "media_ms", "p50_ms", "p95_ms", "p99_ms"}
	cabecalhoCodigos = []string{"rotulo", "cenario", "passageiros", "operacao", "codigo", "quantidade"}
)

// ms formata uma duração em milissegundos com três casas, como no P1.
func ms(d time.Duration) string {
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 3, 64)
}

// imprimirTabela mostra a curva no terminal, em dois blocos: latência por
// operação e contagem de respostas por código.
//
// São dois blocos porque são duas perguntas diferentes. O primeiro responde
// quanto custou; o segundo, o que aconteceu — e sob contenção o segundo é o
// que explica o primeiro, porque uma recusa é mais rápida que uma confirmação.
func imprimirTabela(saida io.Writer, r Resultado) {
	if saida == nil {
		return
	}

	var tabela strings.Builder
	fmt.Fprintf(&tabela, "\ncenário %s, rótulo %s, semente %d\n", r.Cenario, r.Rotulo, r.Semente)

	fmt.Fprintf(&tabela, "\n%11s %13s %12s %14s %11s %9s %9s %9s\n",
		"passageiros", "operação", "requisições", "vazão (req/s)",
		"média (ms)", "p50 (ms)", "p95 (ms)", "p99 (ms)")
	for _, ponto := range r.Pontos {
		for _, resumo := range ponto.resumos() {
			fmt.Fprintf(&tabela, "%11d %13s %12d %14.1f %11s %9s %9s %9s\n",
				ponto.Passageiros, resumo.Operacao, resumo.Requisicoes,
				vazao(resumo.Requisicoes, ponto.Decorrido),
				ms(resumo.Media), ms(resumo.P50), ms(resumo.P95), ms(resumo.P99))
		}
	}

	fmt.Fprintf(&tabela, "\n%11s %13s %29s %12s\n", "passageiros", "operação", "código", "quantidade")
	for _, ponto := range r.Pontos {
		for _, resumo := range ponto.Operacoes {
			for _, codigo := range codigosOrdenados(resumo.PorCodigo) {
				fmt.Fprintf(&tabela, "%11d %13s %29s %12d\n",
					ponto.Passageiros, resumo.Operacao, codigo, resumo.PorCodigo[codigo])
			}
		}
	}

	fmt.Fprint(saida, tabela.String())
}

// gravarCSV escreve os dois arquivos e devolve os caminhos.
func gravarCSV(cfg Config, r Resultado) ([]string, error) {
	if err := os.MkdirAll(cfg.Saida, 0o755); err != nil {
		return nil, fmt.Errorf("criar %s: %w", cfg.Saida, err)
	}

	instante := time.Now().Format("20060102-150405")
	base := filepath.Join(cfg.Saida, nomeBase(r.Cenario, r.Rotulo, instante))
	latencias := base + ".csv"
	codigos := base + "-codigos.csv"

	if err := escrever(latencias, cabecalhoLatencias, linhasDeLatencia(r)); err != nil {
		return nil, err
	}
	if err := escrever(codigos, cabecalhoCodigos, linhasDeCodigo(r)); err != nil {
		return nil, err
	}
	return []string{latencias, codigos}, nil
}

// nomeBase monta simulador-<cenario>[-<rotulo>]-<instante>. O rótulo só entra
// quando diz algo além do cenário, que é o seu valor padrão.
func nomeBase(cenario, rotulo, instante string) string {
	partes := []string{"simulador", cenario}
	if rotulo != "" && rotulo != cenario {
		partes = append(partes, rotulo)
	}
	return strings.Join(append(partes, instante), "-")
}

func linhasDeLatencia(r Resultado) [][]string {
	var linhas [][]string
	for _, ponto := range r.Pontos {
		duracao := strconv.FormatFloat(ponto.Decorrido.Seconds(), 'f', 3, 64)
		for _, resumo := range ponto.resumos() {
			linhas = append(linhas, []string{
				r.Rotulo,
				r.Cenario,
				strconv.Itoa(ponto.Passageiros),
				resumo.Operacao,
				duracao,
				strconv.Itoa(resumo.Requisicoes),
				strconv.FormatFloat(vazao(resumo.Requisicoes, ponto.Decorrido), 'f', 1, 64),
				ms(resumo.Media), ms(resumo.P50), ms(resumo.P95), ms(resumo.P99),
			})
		}
	}
	return linhas
}

// linhasDeCodigo emite só as quatro operações medidas, e não o total: todo
// código do total aparece em alguma delas, e a linha repetida só confundiria
// quem soma a coluna.
func linhasDeCodigo(r Resultado) [][]string {
	var linhas [][]string
	for _, ponto := range r.Pontos {
		for _, resumo := range ponto.Operacoes {
			for _, codigo := range codigosOrdenados(resumo.PorCodigo) {
				linhas = append(linhas, []string{
					r.Rotulo,
					r.Cenario,
					strconv.Itoa(ponto.Passageiros),
					resumo.Operacao,
					codigo,
					strconv.Itoa(resumo.PorCodigo[codigo]),
				})
			}
		}
	}
	return linhas
}

// codigosOrdenados devolve os códigos em ordem alfabética, para que duas
// rodadas iguais produzam arquivos iguais: a iteração de um mapa em Go é
// deliberadamente aleatória.
func codigosOrdenados(porCodigo map[string]int) []string {
	codigos := make([]string, 0, len(porCodigo))
	for codigo := range porCodigo {
		codigos = append(codigos, codigo)
	}
	sort.Strings(codigos)
	return codigos
}

func escrever(caminho string, cabecalho []string, linhas [][]string) error {
	arquivo, err := os.Create(caminho)
	if err != nil {
		return fmt.Errorf("criar %s: %w", caminho, err)
	}
	defer func() { _ = arquivo.Close() }()

	escritor := csv.NewWriter(arquivo)
	if err := escritor.WriteAll(append([][]string{cabecalho}, linhas...)); err != nil {
		return fmt.Errorf("gravar %s: %w", caminho, err)
	}
	return arquivo.Close()
}
