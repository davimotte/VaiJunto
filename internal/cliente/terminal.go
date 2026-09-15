package cliente

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Terminal é a interface de texto compartilhada pelos dois menus.
//
// A regra que orienta todo o arquivo vem de D15: entrada inválida não encerra
// o processo nem fecha a conexão. Toda função de leitura repete a pergunta
// até receber algo válido, e o único erro que qualquer uma delas devolve é
// ErrEntradaEncerrada. Isso importa numa apresentação de 20 minutos com
// arguição no meio: um dedo errado no teclado não pode custar a demonstração.
type Terminal struct {
	entrada *bufio.Scanner
	saida   io.Writer
}

// ErrEntradaEncerrada sinaliza fim da entrada (EOF, como num `Ctrl-D` ou numa
// execução com stdin redirecionado que acabou).
//
// É a única saída de erro das leituras, e é deliberadamente distinta de
// "entrada inválida": não há como repetir a pergunta a quem não está mais
// respondendo, e insistir seria um laço infinito. O CLI a trata como pedido
// de encerramento, faz LOGOUT e sai com status zero.
var ErrEntradaEncerrada = errors.New("cliente: entrada do terminal encerrada")

// NovoTerminal monta um Terminal sobre um par de fluxos. Receber os fluxos
// como parâmetro, em vez de usar os.Stdin direto, é o que torna o menu
// testável com um strings.Reader.
func NovoTerminal(entrada io.Reader, saida io.Writer) *Terminal {
	return &Terminal{entrada: bufio.NewScanner(entrada), saida: saida}
}

// TerminalPadrao monta o Terminal ligado ao terminal do processo.
func TerminalPadrao() *Terminal { return NovoTerminal(os.Stdin, os.Stdout) }

// Imprimir escreve na saída do terminal. O erro de escrita é ignorado: se a
// saída padrão morreu, a próxima leitura devolve ErrEntradaEncerrada e o
// cliente encerra por lá, sem que cada linha impressa precise ser conferida.
func (t *Terminal) Imprimir(formato string, args ...any) {
	fmt.Fprintf(t.saida, formato, args...)
}

// lerBruto lê uma linha crua, sem validar nada. É o único ponto que toca o
// scanner, e portanto o único que pode devolver ErrEntradaEncerrada.
func (t *Terminal) lerBruto(rotulo string) (string, error) {
	t.Imprimir("%s", rotulo)
	if !t.entrada.Scan() {
		t.Imprimir("\n")
		return "", ErrEntradaEncerrada
	}
	return strings.TrimSpace(t.entrada.Text()), nil
}

// recusar explica por que a resposta não serve, antes de repetir a pergunta.
// A mensagem diz o que se espera, e não apenas que houve erro.
func (t *Terminal) recusar(motivo string) {
	t.Imprimir("  %s\n", motivo)
}

// LerTexto pede um texto não vazio.
func (t *Terminal) LerTexto(rotulo string) (string, error) {
	for {
		texto, err := t.lerBruto(rotulo)
		if err != nil {
			return "", err
		}
		if texto != "" {
			return texto, nil
		}
		t.recusar("Digite algo.")
	}
}

// LerInteiro pede um inteiro dentro de [minimo, maximo].
func (t *Terminal) LerInteiro(rotulo string, minimo, maximo int) (int, error) {
	for {
		texto, err := t.lerBruto(rotulo)
		if err != nil {
			return 0, err
		}
		valor, err := strconv.Atoi(texto)
		if err != nil {
			t.recusar(fmt.Sprintf("Digite um número entre %d e %d.", minimo, maximo))
			continue
		}
		if valor < minimo || valor > maximo {
			t.recusar(fmt.Sprintf("Fora da faixa: o número precisa estar entre %d e %d.", minimo, maximo))
			continue
		}
		return valor, nil
	}
}

// LerOpcao mostra as opções numeradas de 1 a len(opcoes) e devolve o índice
// base zero da escolhida.
//
// Devolver o índice, e não o texto, é o que mantém o usuário longe de
// identificador: quem chama usa o índice para recuperar o item que o servidor
// mandou, sem que nada dele apareça na tela ou passe pelo teclado.
func (t *Terminal) LerOpcao(titulo string, opcoes []string) (int, error) {
	if titulo != "" {
		t.Imprimir("\n%s\n", titulo)
	}
	for i, opcao := range opcoes {
		t.Imprimir("%d) %s\n", i+1, opcao)
	}
	escolha, err := t.LerInteiro("Escolha: ", 1, len(opcoes))
	if err != nil {
		return 0, err
	}
	return escolha - 1, nil
}

// VoltarAoMenu é o retorno de LerEscolhaOuVoltar quando o usuário digita 0.
const VoltarAoMenu = -1

// LerEscolhaOuVoltar pede um número de 1 a total e devolve o índice base
// zero, ou VoltarAoMenu quando o usuário digita 0.
//
// A saída por 0 existe em toda lista para que desistir de uma operação seja
// sempre possível sem sair do cliente — e, portanto, sem derrubar a conexão.
func (t *Terminal) LerEscolhaOuVoltar(rotulo string, total int) (int, error) {
	escolha, err := t.LerInteiro(rotulo, 0, total)
	if err != nil {
		return 0, err
	}
	if escolha == 0 {
		return VoltarAoMenu, nil
	}
	return escolha - 1, nil
}

// LerSimNao pede uma confirmação, aceitando resposta vazia como padrao.
func (t *Terminal) LerSimNao(rotulo string, padrao bool) (bool, error) {
	sufixo := " (s/N): "
	if padrao {
		sufixo = " (S/n): "
	}
	for {
		texto, err := t.lerBruto(rotulo + sufixo)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(texto) {
		case "":
			return padrao, nil
		case "s", "sim":
			return true, nil
		case "n", "nao", "não":
			return false, nil
		}
		t.recusar("Responda s ou n.")
	}
}

// LerCentavos pede um valor em reais e devolve centavos.
//
// A conversão é feita sobre as duas metades do texto, e nunca por
// strconv.ParseFloat: converter "45,90" para float e multiplicar por 100 pode
// render 4589 por arredondamento binário, e o preço errado só apareceria na
// soma do itinerário (D11).
func (t *Terminal) LerCentavos(rotulo string) (int, error) {
	for {
		texto, err := t.lerBruto(rotulo)
		if err != nil {
			return 0, err
		}
		centavos, err := analisarCentavos(texto)
		if err != nil {
			t.recusar("Valor inválido. Use o formato 45 ou 45,90.")
			continue
		}
		return centavos, nil
	}
}

var errValorMonetario = errors.New("cliente: valor monetário inválido")

// analisarCentavos converte "45", "45,9", "45,90" ou "R$ 45,90" em centavos.
func analisarCentavos(texto string) (int, error) {
	limpo := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(texto), "R$"))
	limpo = strings.ReplaceAll(limpo, ".", ",")
	if limpo == "" {
		return 0, errValorMonetario
	}

	parteInteira, parteFracionaria, temSeparador := strings.Cut(limpo, ",")
	if parteInteira == "" {
		parteInteira = "0"
	}
	reais, err := strconv.Atoi(parteInteira)
	if err != nil || reais < 0 {
		return 0, errValorMonetario
	}

	centavos := 0
	if temSeparador && parteFracionaria != "" {
		// "45,9" é quarenta e cinco reais e noventa centavos, e não nove.
		if len(parteFracionaria) == 1 {
			parteFracionaria += "0"
		}
		if len(parteFracionaria) != 2 {
			return 0, errValorMonetario
		}
		centavos, err = strconv.Atoi(parteFracionaria)
		if err != nil || centavos < 0 {
			return 0, errValorMonetario
		}
	}
	return reais*100 + centavos, nil
}

// formatoDataISO é o formato "AAAA-MM-DD" que a busca exige (PROTOCOL.md,
// seção 3).
const formatoDataISO = "2006-01-02"

// LerDataISO pede uma data e devolve a string "AAAA-MM-DD" que vai no campo
// "data" da busca.
//
// A data é validada aqui com time.Parse, e não apenas repassada: "2026-02-30"
// chegaria ao servidor como CAMPO_INVALIDO depois de uma ida e volta na rede,
// enquanto o cliente pode repetir a pergunta na hora.
func (t *Terminal) LerDataISO(rotulo string) (string, error) {
	return t.lerDataISO(rotulo, "")
}

// lerDataISO é LerDataISO com uma data padrão opcional, já em "AAAA-MM-DD":
// com padrao preenchido, a resposta vazia a devolve; com padrao vazio, a
// resposta vazia é uma data inválida como qualquer outra.
func (t *Terminal) lerDataISO(rotulo, padrao string) (string, error) {
	for {
		texto, err := t.lerBruto(rotulo)
		if err != nil {
			return "", err
		}
		if texto == "" && padrao != "" {
			return padrao, nil
		}
		if _, err := time.Parse(formatoDataISO, texto); err != nil {
			t.recusar("Data inválida. Use o formato AAAA-MM-DD, por exemplo 2026-09-15.")
			continue
		}
		return texto, nil
	}
}

// LerInstante pede data e hora e monta o time.Time no fuso informado.
//
// O fuso é explícito e vem de quem chama (FusoDasCidades): um instante sem
// fuso definido seria interpretado em UTC dentro do contêiner Alpine, e a
// carona publicada para as 08:00 apareceria às 05:00 para todo mundo.
func (t *Terminal) LerInstante(rotuloData, rotuloHora string, fuso *time.Location) (time.Time, error) {
	data, err := t.LerDataISO(rotuloData)
	if err != nil {
		return time.Time{}, err
	}
	return t.lerHora(rotuloHora, data, fuso)
}

// LerInstanteComDataPadrao é LerInstante com a data opcional: Enter vazio usa
// o dia de padrao, e uma data digitada o substitui.
//
// O dia é o de padrao lido no fuso informado, e não no fuso em que o instante
// chegou: 02:00 UTC de 21/09 ainda é 20/09 na Bahia, e é essa a data que o
// motorista vê e confirma.
func (t *Terminal) LerInstanteComDataPadrao(rotuloData, rotuloHora string, padrao time.Time, fuso *time.Location) (time.Time, error) {
	data, err := t.lerDataISO(rotuloData, padrao.In(fuso).Format(formatoDataISO))
	if err != nil {
		return time.Time{}, err
	}
	return t.lerHora(rotuloHora, data, fuso)
}

// lerHora pede a hora do dia data e monta o instante no fuso informado.
func (t *Terminal) lerHora(rotuloHora, data string, fuso *time.Location) (time.Time, error) {
	// Só a hora é repetida quando a hora está errada. Reabrir a pergunta da
	// data obrigaria a redigitar um dado que já foi aceito, que é o tipo de
	// atrito que faz o operador errar de novo.
	for {
		hora, err := t.lerBruto(rotuloHora)
		if err != nil {
			return time.Time{}, err
		}
		// A data já passou por LerDataISO, então o que ParseInLocation ainda
		// pode recusar aqui é a hora.
		instante, err := time.ParseInLocation(formatoDataISO+" 15:04", data+" "+hora, fuso)
		if err != nil {
			t.recusar("Hora inválida. Use o formato HH:MM, por exemplo 08:00.")
			continue
		}
		return instante, nil
	}
}
