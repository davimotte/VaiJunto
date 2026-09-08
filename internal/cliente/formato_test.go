package cliente

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// colunaDe devolve em qual coluna da tela agulha começa dentro de linha.
//
// strings.Index não serve: ele conta bytes, e "→" ocupa três bytes em uma
// coluna. Usá-lo aqui faria o teste acusar desalinhamento numa tabela
// perfeitamente alinhada — exatamente o engano que Preencher evita no código.
func colunaDe(linha, agulha string) int {
	byteInicial := strings.Index(linha, agulha)
	if byteInicial < 0 {
		return -1
	}
	return utf8.RuneCountInString(linha[:byteInicial])
}

// fusoDoCorredorFixo é o deslocamento das cidades do corredor, usado nos
// testes para montar instantes sem depender do tzdata da máquina.
var fusoDoCorredorFixo = time.FixedZone("-03", -3*60*60)

func TestFormatarCentavos(t *testing.T) {
	casos := []struct {
		centavos int
		esperado string
	}{
		{11500, "R$ 115,00"},
		{4500, "R$ 45,00"},
		{4590, "R$ 45,90"},
		{0, "R$ 0,00"},
		{5, "R$ 0,05"},
		{50, "R$ 0,50"},
		{99, "R$ 0,99"},
		{100, "R$ 1,00"},
		{123456789, "R$ 1234567,89"},
	}

	for _, caso := range casos {
		if got := FormatarCentavos(caso.centavos); got != caso.esperado {
			t.Errorf("FormatarCentavos(%d) = %q, want %q", caso.centavos, got, caso.esperado)
		}
	}
}

// TestFormatarCentavos_SomaSemArredondamento confere que a soma de preços
// exibida bate com a soma feita em centavos. É a razão de D11: em float,
// 0,10 + 0,20 não dá exatamente 0,30, e o preço total do itinerário sairia
// com um centavo de diferença.
func TestFormatarCentavos_SomaSemArredondamento(t *testing.T) {
	precos := []int{10, 20, 3010, 4590}
	total := 0
	for _, preco := range precos {
		total += preco
	}
	if got := FormatarCentavos(total); got != "R$ 76,30" {
		t.Errorf("total = %q, want %q", got, "R$ 76,30")
	}
}

func TestFormatarInstante(t *testing.T) {
	instante := time.Date(2026, 9, 15, 6, 0, 0, 0, fusoDoCorredorFixo)
	if got := FormatarInstante(instante); got != "15/09/2026 06:00" {
		t.Errorf("FormatarInstante = %q", got)
	}
	if got := FormatarDiaHora(instante); got != "15/09 06:00" {
		t.Errorf("FormatarDiaHora = %q", got)
	}
}

// TestFormatarInstante_NaoConverteOFuso: o instante é exibido como o servidor
// mandou. Converter para o fuso da máquina faria o horário na tela do
// passageiro diferir do que o motorista publicou, e num contêiner Alpine
// (fuso local UTC) a diferença seria de três horas.
func TestFormatarInstante_NaoConverteOFuso(t *testing.T) {
	instante := time.Date(2026, 9, 15, 6, 0, 0, 0, fusoDoCorredorFixo)

	// O mesmo instante visto de outro fuso: a formatação precisa continuar
	// mostrando a hora do fuso que veio junto do valor.
	if got := FormatarInstante(instante.In(time.UTC).In(fusoDoCorredorFixo)); got != "15/09/2026 06:00" {
		t.Errorf("FormatarInstante = %q, want %q", got, "15/09/2026 06:00")
	}
}

// TestFormatarHoraRelativa cobre a baldeação que atravessa a meia-noite,
// permitida por D13: a janela de conexão vai até 12 h e o filtro de data da
// busca vale só para a primeira perna. Exibir só "06:00" numa perna do dia
// seguinte faria o itinerário parecer voltar no tempo.
func TestFormatarHoraRelativa(t *testing.T) {
	referencia := time.Date(2026, 9, 15, 22, 0, 0, 0, fusoDoCorredorFixo)

	casos := []struct {
		nome     string
		instante time.Time
		esperado string
	}{
		{
			nome:     "mesmo dia sai só com a hora",
			instante: time.Date(2026, 9, 15, 23, 30, 0, 0, fusoDoCorredorFixo),
			esperado: "23:30",
		},
		{
			nome:     "dia seguinte sai com a data",
			instante: time.Date(2026, 9, 16, 1, 0, 0, 0, fusoDoCorredorFixo),
			esperado: "16/09 01:00",
		},
		{
			nome:     "meia-noite em ponto já é o dia seguinte",
			instante: time.Date(2026, 9, 16, 0, 0, 0, 0, fusoDoCorredorFixo),
			esperado: "16/09 00:00",
		},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			if got := FormatarHoraRelativa(caso.instante, referencia); got != caso.esperado {
				t.Errorf("FormatarHoraRelativa = %q, want %q", got, caso.esperado)
			}
		})
	}
}

func TestFormatarIntervalo_BaldeacaoNoturna(t *testing.T) {
	referencia := time.Date(2026, 9, 15, 22, 0, 0, 0, fusoDoCorredorFixo)
	partida := time.Date(2026, 9, 15, 23, 0, 0, 0, fusoDoCorredorFixo)
	chegada := time.Date(2026, 9, 16, 1, 30, 0, 0, fusoDoCorredorFixo)

	if got := FormatarIntervalo(partida, chegada, referencia); got != "23:00 → 16/09 01:30" {
		t.Errorf("FormatarIntervalo = %q", got)
	}
}

// TestPreencher_ContaRunasENaoBytes: as cidades do corredor têm acento, e
// alinhar por bytes torceria toda coluna à direita de "Jequié".
func TestPreencher_ContaRunasENaoBytes(t *testing.T) {
	comAcento := Preencher("Jequié", 10)
	semAcento := Preencher("Jequie", 10)

	if len([]rune(comAcento)) != 10 {
		t.Errorf("Preencher(\"Jequié\", 10) tem %d runas, want 10", len([]rune(comAcento)))
	}
	if len([]rune(comAcento)) != len([]rune(semAcento)) {
		t.Errorf("acento mudou a largura: %q vs %q", comAcento, semAcento)
	}
	// Texto maior que a largura sai inteiro: truncar nome de cidade seria pior
	// que empurrar a linha.
	if got := Preencher("Vitória da Conquista", 5); got != "Vitória da Conquista" {
		t.Errorf("Preencher truncou: %q", got)
	}
}

func TestPlural(t *testing.T) {
	casos := []struct {
		n        int
		esperado string
	}{
		{0, "0 baldeações"},
		{1, "1 baldeação"},
		{2, "2 baldeações"},
	}
	for _, caso := range casos {
		if got := Plural(caso.n, "baldeação", "baldeações"); got != caso.esperado {
			t.Errorf("Plural(%d) = %q, want %q", caso.n, got, caso.esperado)
		}
	}
}

// TestTabela_AlinhaPelaLarguraDoConteudo: a largura de cada coluna sai do
// conteúdo, e não de uma constante escolhida na mão. Constante precisaria
// caber o pior caso do corredor e deixaria um vão em toda listagem comum.
func TestTabela_AlinhaPelaLarguraDoConteudo(t *testing.T) {
	var tabela Tabela
	tabela.Linha("Salvador → Jequié", "06:00 → 11:00", "R$ 75,00")
	tabela.Linha("Jequié → Vitória da Conquista", "12:30 → 15:00", "R$ 40,00")

	var saida bytes.Buffer
	tabela.Escrever(NovoTerminal(strings.NewReader(""), &saida))

	linhas := strings.Split(strings.TrimRight(saida.String(), "\n"), "\n")
	if len(linhas) != 2 {
		t.Fatalf("linhas = %d, want 2", len(linhas))
	}

	// A segunda coluna começa na mesma posição nas duas linhas.
	primeira := colunaDe(linhas[0], "06:00")
	segunda := colunaDe(linhas[1], "12:30")
	if primeira != segunda {
		t.Errorf("colunas desalinhadas: %d vs %d\n%s", primeira, segunda, saida.String())
	}
	// A largura vem do conteúdo mais largo, e não de um pior caso hipotético:
	// "Jequié → Vitória da Conquista" tem 29 runas, mais o espaçamento.
	if primeira != 29+espacamentoColunas {
		t.Errorf("coluna começa em %d, want %d", primeira, 29+espacamentoColunas)
	}
	// A última coluna não leva preenchimento à direita.
	for i, linha := range linhas {
		if strings.HasSuffix(linha, " ") {
			t.Errorf("linha %d termina com espaço: %q", i, linha)
		}
	}
}

// TestTabela_LinhaSoltaNaoEntraNoAlinhamento: o cabeçalho de um itinerário
// atravessa a tabela inteira e não pode esticar as colunas das pernas.
func TestTabela_LinhaSoltaNaoEntraNoAlinhamento(t *testing.T) {
	var tabela Tabela
	tabela.LinhaSolta("[1] %s — cabeçalho bem mais longo que qualquer coluna da tabela", "R$ 115,00")
	tabela.Linha("Salvador → Jequié", "R$ 75,00")

	var saida bytes.Buffer
	tabela.Escrever(NovoTerminal(strings.NewReader(""), &saida))

	linhas := strings.Split(strings.TrimRight(saida.String(), "\n"), "\n")
	if len(linhas) != 2 {
		t.Fatalf("linhas = %d, want 2", len(linhas))
	}
	if !strings.HasPrefix(linhas[0], "[1] R$ 115,00 — cabeçalho") {
		t.Errorf("linha solta = %q", linhas[0])
	}
	if got := colunaDe(linhas[1], "R$ 75,00"); got != utf8.RuneCountInString("Salvador → Jequié")+espacamentoColunas {
		t.Errorf("a linha solta esticou a coluna: preço começa em %d", got)
	}
}
