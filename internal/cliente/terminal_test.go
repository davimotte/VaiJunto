package cliente

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// terminalDeTeste monta um Terminal sobre uma entrada roteirizada e devolve
// também o buffer de saída, para que o teste confira o que foi impresso.
func terminalDeTeste(entrada string) (*Terminal, *bytes.Buffer) {
	var saida bytes.Buffer
	return NovoTerminal(strings.NewReader(entrada), &saida), &saida
}

// TestLerInteiro_RepeteAteReceberValorValido é a propriedade que D15 exige do
// menu: entrada inválida não encerra o processo nem fecha a conexão, apenas
// repete a pergunta. Importa na apresentação, que tem arguição no meio.
func TestLerInteiro_RepeteAteReceberValorValido(t *testing.T) {
	casos := []struct {
		nome    string
		entrada string
	}{
		{"texto no lugar de número", "abc\n2\n"},
		{"acima da faixa", "9\n2\n"},
		{"abaixo da faixa", "0\n2\n"},
		{"linha vazia", "\n2\n"},
		{"número com lixo junto", "2x\n2\n"},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			term, saida := terminalDeTeste(caso.entrada)

			valor, err := term.LerInteiro("Escolha: ", 1, 3)
			if err != nil {
				t.Fatalf("LerInteiro: %v", err)
			}
			if valor != 2 {
				t.Errorf("valor = %d, want 2", valor)
			}
			// A recusa precisa dizer o que se espera, e não só que houve erro.
			if !strings.Contains(saida.String(), "1 e 3") {
				t.Errorf("saída = %q, esperava a faixa aceita", saida.String())
			}
		})
	}
}

// TestLerOpcao_NumeraAsOpcoesEDevolveOIndice: as opções são apresentadas de 1
// a n, e o retorno é base zero para indexar a lista que veio do servidor sem
// que nenhum identificador dela apareça na tela.
func TestLerOpcao_NumeraAsOpcoesEDevolveOIndice(t *testing.T) {
	term, saida := terminalDeTeste("3\n")

	indice, err := term.LerOpcao("Origem:", []string{"Salvador", "Feira de Santana", "Jequié"})
	if err != nil {
		t.Fatalf("LerOpcao: %v", err)
	}
	if indice != 2 {
		t.Errorf("índice = %d, want 2", indice)
	}
	if !strings.Contains(saida.String(), "3) Jequié") {
		t.Errorf("saída = %q, esperava as opções numeradas", saida.String())
	}
}

// TestLerEscolhaOuVoltar cobre a saída por 0, que existe em toda lista para
// que desistir de uma operação nunca exija sair do cliente.
func TestLerEscolhaOuVoltar(t *testing.T) {
	casos := []struct {
		nome     string
		entrada  string
		esperado int
	}{
		{"zero volta ao menu", "0\n", VoltarAoMenu},
		{"primeiro item", "1\n", 0},
		{"último item", "4\n", 3},
		{"inválido antes do válido", "7\n2\n", 1},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			term, _ := terminalDeTeste(caso.entrada)

			escolha, err := term.LerEscolhaOuVoltar("Reservar qual? (0 para voltar): ", 4)
			if err != nil {
				t.Fatalf("LerEscolhaOuVoltar: %v", err)
			}
			if escolha != caso.esperado {
				t.Errorf("escolha = %d, want %d", escolha, caso.esperado)
			}
		})
	}
}

func TestLerSimNao(t *testing.T) {
	casos := []struct {
		nome     string
		entrada  string
		padrao   bool
		esperado bool
	}{
		{"s", "s\n", false, true},
		{"sim", "sim\n", false, true},
		{"n", "n\n", true, false},
		{"não com acento", "não\n", true, false},
		{"maiúscula", "S\n", false, true},
		{"vazio usa o padrão falso", "\n", false, false},
		{"vazio usa o padrão verdadeiro", "\n", true, true},
		{"repete até entender", "talvez\ns\n", false, true},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			term, _ := terminalDeTeste(caso.entrada)

			resposta, err := term.LerSimNao("Confirma?", caso.padrao)
			if err != nil {
				t.Fatalf("LerSimNao: %v", err)
			}
			if resposta != caso.esperado {
				t.Errorf("resposta = %v, want %v", resposta, caso.esperado)
			}
		})
	}
}

func TestLerDataISO_RecusaDataInexistente(t *testing.T) {
	term, saida := terminalDeTeste("2026-13-40\n2026-02-30\n15/09/2026\n2026-09-15\n")

	data, err := term.LerDataISO("Data: ")
	if err != nil {
		t.Fatalf("LerDataISO: %v", err)
	}
	if data != "2026-09-15" {
		t.Errorf("data = %q, want %q", data, "2026-09-15")
	}
	if !strings.Contains(saida.String(), "AAAA-MM-DD") {
		t.Errorf("saída = %q, esperava o formato exigido", saida.String())
	}
}

// TestLerInstante_HoraInvalidaNaoReperguntaAData: a data já aceita não pode
// ser pedida de novo por causa de um erro na hora — redigitar um dado já
// validado é o atrito que faz o operador errar outra vez.
func TestLerInstante_HoraInvalidaNaoReperguntaAData(t *testing.T) {
	term, saida := terminalDeTeste("2026-09-15\n25:00\n8h\n08:00\n")
	fuso := time.FixedZone("-03", -3*60*60)

	instante, err := term.LerInstante("Data: ", "Hora: ", fuso)
	if err != nil {
		t.Fatalf("LerInstante: %v", err)
	}

	esperado := time.Date(2026, 9, 15, 8, 0, 0, 0, fuso)
	if !instante.Equal(esperado) {
		t.Errorf("instante = %s, want %s", instante, esperado)
	}
	if got := strings.Count(saida.String(), "Data: "); got != 1 {
		t.Errorf("a data foi pedida %d vezes, want 1", got)
	}
	if got := strings.Count(saida.String(), "Hora: "); got != 3 {
		t.Errorf("a hora foi pedida %d vezes, want 3", got)
	}
}

// TestLerInstante_UsaOFusoInformado: o instante montado carrega o fuso
// informado, e não o da máquina. Dentro do contêiner Alpine o fuso local é UTC,
// e uma partida digitada como 08:00 sairia três horas adiantada.
func TestLerInstante_UsaOFusoInformado(t *testing.T) {
	term, _ := terminalDeTeste("2026-09-15\n08:00\n")
	fuso := time.FixedZone("-03", -3*60*60)

	instante, err := term.LerInstante("Data: ", "Hora: ", fuso)
	if err != nil {
		t.Fatalf("LerInstante: %v", err)
	}
	if _, deslocamento := instante.Zone(); deslocamento != -3*60*60 {
		t.Errorf("deslocamento = %d s, want %d s", deslocamento, -3*60*60)
	}
	if got := instante.Format(time.RFC3339); got != "2026-09-15T08:00:00-03:00" {
		t.Errorf("instante = %s", got)
	}
}

// TestLerInstanteComDataPadrao cobre a data opcional das paradas seguintes à
// primeira: Enter vazio usa a data padrão, uma data digitada a substitui, e
// uma data inválida continua sendo recusada — a resposta vazia é a única que
// ganhou significado novo.
func TestLerInstanteComDataPadrao(t *testing.T) {
	fuso := time.FixedZone("-03", -3*60*60)
	padrao := time.Date(2026, 9, 20, 8, 0, 0, 0, fuso)

	casos := []struct {
		nome     string
		entrada  string
		esperado time.Time
		recusas  int
	}{
		{"Enter usa a data padrão", "\n10:15\n", time.Date(2026, 9, 20, 10, 15, 0, 0, fuso), 0},
		{"data digitada substitui a padrão", "2026-09-21\n01:30\n", time.Date(2026, 9, 21, 1, 30, 0, 0, fuso), 0},
		{"data inválida é recusada e Enter ainda vale", "20/09\n\n10:15\n", time.Date(2026, 9, 20, 10, 15, 0, 0, fuso), 1},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			term, saida := terminalDeTeste(caso.entrada)
			instante, err := term.LerInstanteComDataPadrao("Data: ", "Hora: ", padrao, fuso)
			if err != nil {
				t.Fatalf("LerInstanteComDataPadrao: %v", err)
			}
			if !instante.Equal(caso.esperado) {
				t.Errorf("instante = %v, want %v", instante, caso.esperado)
			}
			if n := strings.Count(saida.String(), "Data inválida"); n != caso.recusas {
				t.Errorf("%d recusas de data, want %d:\n%s", n, caso.recusas, saida.String())
			}
		})
	}
}

// TestLerInstanteComDataPadrao_DataPadraoNoFusoInformado: a data que o Enter
// repete é a do dia civil no fuso das cidades, e não no fuso em que o instante
// padrão chegou. 02:00 UTC de 21/09 ainda é 23:00 de 20/09 na Bahia.
func TestLerInstanteComDataPadrao_DataPadraoNoFusoInformado(t *testing.T) {
	fuso := time.FixedZone("-03", -3*60*60)
	padrao := time.Date(2026, 9, 21, 2, 0, 0, 0, time.UTC)

	term, _ := terminalDeTeste("\n23:30\n")
	instante, err := term.LerInstanteComDataPadrao("Data: ", "Hora: ", padrao, fuso)
	if err != nil {
		t.Fatalf("LerInstanteComDataPadrao: %v", err)
	}
	if got := instante.Format(time.RFC3339); got != "2026-09-20T23:30:00-03:00" {
		t.Errorf("instante = %s, want 2026-09-20T23:30:00-03:00", got)
	}
}

func TestAnalisarCentavos(t *testing.T) {
	validos := []struct {
		texto    string
		esperado int
	}{
		{"45", 4500},
		{"45,90", 4590},
		{"45,9", 4590}, // "45,9" é noventa centavos, não nove
		{"45.90", 4590},
		{"R$ 45,90", 4590},
		{"R$45,90", 4590},
		{"  45,90  ", 4590},
		{"0", 0},
		{"0,05", 5},
		{",50", 50},
		{"115,00", 11500},
		{"45,", 4500},
	}
	for _, caso := range validos {
		t.Run(caso.texto, func(t *testing.T) {
			centavos, err := analisarCentavos(caso.texto)
			if err != nil {
				t.Fatalf("analisarCentavos(%q): %v", caso.texto, err)
			}
			if centavos != caso.esperado {
				t.Errorf("analisarCentavos(%q) = %d, want %d", caso.texto, centavos, caso.esperado)
			}
		})
	}

	invalidos := []string{"", "abc", "-5", "45,900", "4,5,6", "45,9x", "R$", "1e2"}
	for _, texto := range invalidos {
		t.Run("inválido: "+texto, func(t *testing.T) {
			if centavos, err := analisarCentavos(texto); err == nil {
				t.Errorf("analisarCentavos(%q) = %d, esperava erro", texto, centavos)
			}
		})
	}
}

func TestLerCentavos_RepeteAteReceberValorValido(t *testing.T) {
	term, saida := terminalDeTeste("abc\n-5\n45,90\n")

	centavos, err := term.LerCentavos("Preço: R$ ")
	if err != nil {
		t.Fatalf("LerCentavos: %v", err)
	}
	if centavos != 4590 {
		t.Errorf("centavos = %d, want 4590", centavos)
	}
	if !strings.Contains(saida.String(), "45,90") {
		t.Errorf("saída = %q, esperava o formato de exemplo", saida.String())
	}
}

// TestLeituras_FimDeEntrada garante que toda leitura tem saída pelo EOF. Sem
// isso, um cliente com a entrada fechada — stdin redirecionado que acabou, ou
// contêiner sem -it — giraria para sempre repetindo a pergunta.
func TestLeituras_FimDeEntrada(t *testing.T) {
	leituras := map[string]func(*Terminal) error{
		"LerTexto": func(term *Terminal) error {
			_, err := term.LerTexto("? ")
			return err
		},
		"LerInteiro": func(term *Terminal) error {
			_, err := term.LerInteiro("? ", 1, 3)
			return err
		},
		"LerOpcao": func(term *Terminal) error {
			_, err := term.LerOpcao("?", []string{"a", "b"})
			return err
		},
		"LerEscolhaOuVoltar": func(term *Terminal) error {
			_, err := term.LerEscolhaOuVoltar("? ", 2)
			return err
		},
		"LerSimNao": func(term *Terminal) error {
			_, err := term.LerSimNao("?", false)
			return err
		},
		"LerCentavos": func(term *Terminal) error {
			_, err := term.LerCentavos("? ")
			return err
		},
		"LerDataISO": func(term *Terminal) error {
			_, err := term.LerDataISO("? ")
			return err
		},
		"LerInstante": func(term *Terminal) error {
			_, err := term.LerInstante("? ", "? ", time.UTC)
			return err
		},
	}

	for nome, ler := range leituras {
		t.Run(nome, func(t *testing.T) {
			term, _ := terminalDeTeste("")
			if err := ler(term); !errors.Is(err, ErrEntradaEncerrada) {
				t.Errorf("%s = %v, want ErrEntradaEncerrada", nome, err)
			}
		})
	}
}

// TestLerInstante_FimDeEntradaNaHora cobre o EOF depois da data já aceita,
// que é o caminho que o laço da hora poderia deixar preso.
func TestLerInstante_FimDeEntradaNaHora(t *testing.T) {
	term, _ := terminalDeTeste("2026-09-15\n")

	if _, err := term.LerInstante("Data: ", "Hora: ", time.UTC); !errors.Is(err, ErrEntradaEncerrada) {
		t.Errorf("LerInstante = %v, want ErrEntradaEncerrada", err)
	}
}
