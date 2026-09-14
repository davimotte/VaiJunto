package cliente

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Formatação para exibição. Nada aqui altera o que vai para o socket: o
// protocolo continua trafegando centavos inteiros (D11) e RFC 3339. A
// conversão para reais e para horário legível é responsabilidade do cliente,
// e acontece só na última linha antes da tela.

// FormatarCentavos escreve um valor em centavos como reais.
//
// A divisão é inteira, e não por float: 11500 vira "R$ 115,00" por
// 11500/100 e 11500%100, sem que o valor passe em momento algum por um tipo
// que arredonda (D11).
func FormatarCentavos(centavos int) string {
	sinal := ""
	if centavos < 0 {
		sinal = "-"
		centavos = -centavos
	}
	return fmt.Sprintf("R$ %s%d,%02d", sinal, centavos/100, centavos%100)
}

// Formatos de exibição, em convenção brasileira (dia/mês/ano, hora de 24 h).
const (
	formatoInstante = "02/01/2006 15:04"
	formatoDiaHora  = "02/01 15:04"
	formatoHora     = "15:04"
)

// FormatarInstante escreve um instante completo, com data e hora.
//
// Não há conversão de fuso: o servidor envia RFC 3339 com o deslocamento que
// o motorista publicou, encoding/json preserva esse deslocamento ao
// desserializar, e exibir o instante como ele veio é o que faz o horário na
// tela do passageiro ser o mesmo horário que o motorista publicou —
// independentemente do fuso da máquina onde o cliente roda, que numa imagem
// Alpine costuma ser UTC.
func FormatarInstante(t time.Time) string { return t.Format(formatoInstante) }

// FormatarDiaHora escreve dia, mês e hora, sem o ano. Serve às listagens em
// que o ano já apareceu no cabeçalho.
func FormatarDiaHora(t time.Time) string { return t.Format(formatoDiaHora) }

// FormatarHoraRelativa escreve só a hora quando t cai no mesmo dia de
// referencia, e o dia junto quando não cai.
//
// Existe por causa de D13: a janela de baldeação vai até 12 h e o filtro de
// data da busca vale só para a primeira perna, então uma conexão legítima pode
// atravessar a meia-noite. Exibir apenas "06:00" numa perna do dia seguinte
// faria o itinerário parecer voltar no tempo.
func FormatarHoraRelativa(t, referencia time.Time) string {
	if mesmoDia(t, referencia) {
		return t.Format(formatoHora)
	}
	return t.Format(formatoDiaHora)
}

// mesmoDia compara as datas civis de dois instantes. A comparação é feita no
// fuso de a para os dois lados: instantes vindos do servidor carregam o mesmo
// deslocamento, e é a data no fuso das cidades que interessa ao passageiro.
func mesmoDia(a, b time.Time) bool {
	b = b.In(a.Location())
	anoA, mesA, diaA := a.Date()
	anoB, mesB, diaB := b.Date()
	return anoA == anoB && mesA == mesB && diaA == diaB
}

// FormatarIntervalo escreve partida e chegada de um trecho, com o dia
// aparecendo apenas quando difere do dia de referencia.
func FormatarIntervalo(partida, chegada, referencia time.Time) string {
	return FormatarHoraRelativa(partida, referencia) + " → " + FormatarHoraRelativa(chegada, referencia)
}

// Preencher completa s com espaços à direita até largura colunas.
//
// Conta runas, e não bytes, porque %-*s do fmt conta bytes: "Jequié" tem 6
// runas e 7 bytes, e toda cidade com acento desalinharia as colunas seguintes
// da listagem — que é justamente o que o alinhamento existe para evitar numa
// tela de apresentação. String mais longa que a largura é devolvida inteira:
// truncar um nome de cidade seria pior que empurrar a linha.
func Preencher(s string, largura int) string {
	faltam := largura - utf8.RuneCountInString(s)
	if faltam <= 0 {
		return s
	}
	return s + strings.Repeat(" ", faltam)
}

// Plural escolhe entre a forma singular e a plural de acordo com n, e devolve
// a contagem junto: Plural(1, "reserva", "reservas") == "1 reserva".
func Plural(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// espacamentoColunas é o respiro entre uma coluna e a seguinte.
const espacamentoColunas = 2

// Tabela acumula as linhas de uma listagem e só as imprime no final, quando
// já sabe a largura real de cada coluna.
//
// Larguras fixas escolhidas na mão não servem aqui: precisariam caber o pior
// caso — "Vitória da Conquista → Feira de Santana" na rota, e a data junto da
// hora na baldeação que atravessa a meia-noite —, e aí toda listagem comum
// sairia com um vão enorme entre as colunas. Medir o conteúdo faz a tabela
// ficar estreita quando os dados são estreitos, sem deixar de caber quando
// não são.
type Tabela struct {
	linhas []linhaTabela
}

type linhaTabela struct {
	celulas []string
	// solta marca a linha que atravessa a tabela inteira, como o cabeçalho de
	// um itinerário: ela é impressa como veio e não entra no cálculo das
	// larguras.
	solta bool
}

// Linha acrescenta uma linha alinhada em colunas.
func (t *Tabela) Linha(celulas ...string) {
	t.linhas = append(t.linhas, linhaTabela{celulas: celulas})
}

// LinhaSolta acrescenta uma linha que não participa do alinhamento.
func (t *Tabela) LinhaSolta(formato string, args ...any) {
	t.linhas = append(t.linhas, linhaTabela{celulas: []string{fmt.Sprintf(formato, args...)}, solta: true})
}

// Escrever imprime a tabela no terminal.
func (t *Tabela) Escrever(term *Terminal) {
	larguras := t.larguras()
	for _, linha := range t.linhas {
		if linha.solta {
			term.Imprimir("%s\n", linha.celulas[0])
			continue
		}
		var b strings.Builder
		for i, celula := range linha.celulas {
			// A última coluna não recebe preenchimento: espaço no fim da
			// linha não alinha nada e polui o terminal.
			if i == len(linha.celulas)-1 {
				b.WriteString(celula)
				continue
			}
			b.WriteString(Preencher(celula, larguras[i]+espacamentoColunas))
		}
		term.Imprimir("%s\n", b.String())
	}
}

// larguras devolve a maior largura de cada coluna, em runas.
func (t *Tabela) larguras() []int {
	var larguras []int
	for _, linha := range t.linhas {
		if linha.solta {
			continue
		}
		for i, celula := range linha.celulas {
			for len(larguras) <= i {
				larguras = append(larguras, 0)
			}
			if largura := utf8.RuneCountInString(celula); largura > larguras[i] {
				larguras[i] = largura
			}
		}
	}
	return larguras
}
