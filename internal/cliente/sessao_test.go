package cliente

import (
	"errors"
	"strings"
	"testing"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// Menu de cidades usado nos testes de ColetarParadas, na ordem de
// dominio.CidadesAtendidas: 1) Salvador, 2) Feira de Santana, 3) Jequié,
// 4) Vitória da Conquista e, a partir da terceira parada, 5) encerrar a rota.

// paradasEsperadas compara cidade e horário, parada a parada. Horário por
// Equal, e não por ==, pelo motivo de sempre (D11).
func paradasEsperadas(t *testing.T, obtidas []protocolo.Parada, cidades []string, horarios []time.Time) {
	t.Helper()
	if len(obtidas) != len(cidades) {
		t.Fatalf("paradas = %+v, want %d paradas (%v)", obtidas, len(cidades), cidades)
	}
	for i := range cidades {
		if obtidas[i].Cidade != cidades[i] || !obtidas[i].Horario.Equal(horarios[i]) {
			t.Errorf("parada %d = %s %v, want %s %v", i+1, obtidas[i].Cidade, obtidas[i].Horario, cidades[i], horarios[i])
		}
	}
}

// em devolve um instante de 20/09/2026 no fuso fixo dos testes.
func em(hora, minuto int) time.Time {
	return time.Date(2026, 9, 20, hora, minuto, 0, 0, fusoDasCidadesFixo)
}

// TestColetarParadas_MontaRotaNaOrdemInformada confere o caminho feliz de D09
// no menu: o motorista escolhe cada cidade e digita o horário de cada parada,
// e as paradas saem na ordem em que foram informadas — que não precisa ser a
// ordem do menu.
func TestColetarParadas_MontaRotaNaOrdemInformada(t *testing.T) {
	term, _ := terminalDeTeste(
		"3\n2026-09-20\n08:00\n" + // Jequié
			"1\n2026-09-20\n08:50\n" + // Salvador
			"4\n2026-09-20\n20:10\n" + // Vitória da Conquista
			"5\n") // encerrar

	paradas, err := ColetarParadas(term, fusoDasCidadesFixo)
	if err != nil {
		t.Fatalf("ColetarParadas: %v", err)
	}
	paradasEsperadas(t, paradas,
		[]string{"Jequié", "Salvador", "Vitória da Conquista"},
		[]time.Time{em(8, 0), em(8, 50), em(20, 10)})
}

// TestColetarParadas_CidadeRepetidaRepetePergunta confere D15 aplicado à regra
// de cidade repetida (D09): o menu recusa na hora, sem mandar ao servidor uma
// requisição que só poderia voltar como ROTA_INVALIDA, e sem sair do fluxo.
func TestColetarParadas_CidadeRepetidaRepetePergunta(t *testing.T) {
	term, saida := terminalDeTeste(
		"1\n2026-09-20\n08:00\n" + // Salvador
			"1\n" + // Salvador de novo: recusada
			"2\n2026-09-20\n10:00\n" + // Feira de Santana
			"5\n")

	paradas, err := ColetarParadas(term, fusoDasCidadesFixo)
	if err != nil {
		t.Fatalf("ColetarParadas: %v", err)
	}
	paradasEsperadas(t, paradas,
		[]string{"Salvador", "Feira de Santana"},
		[]time.Time{em(8, 0), em(10, 0)})
	if !strings.Contains(saida.String(), "Salvador já está na rota") {
		t.Errorf("a recusa não explicou o motivo:\n%s", saida.String())
	}
}

// TestColetarParadas_HorarioNaoPosteriorRepetePergunta confere a regra de
// horários estritamente crescentes (D09) no menu: horário anterior e horário
// igual ao da parada anterior são recusados, e só a pergunta do horário se
// repete — a cidade já escolhida fica.
func TestColetarParadas_HorarioNaoPosteriorRepetePergunta(t *testing.T) {
	term, saida := terminalDeTeste(
		"1\n2026-09-20\n08:00\n" + // Salvador
			"2\n" + // Feira de Santana
			"2026-09-20\n07:00\n" + // antes: recusado
			"2026-09-20\n08:00\n" + // igual: recusado
			"2026-09-20\n09:00\n" + // aceito
			"5\n")

	paradas, err := ColetarParadas(term, fusoDasCidadesFixo)
	if err != nil {
		t.Fatalf("ColetarParadas: %v", err)
	}
	paradasEsperadas(t, paradas,
		[]string{"Salvador", "Feira de Santana"},
		[]time.Time{em(8, 0), em(9, 0)})
	if n := strings.Count(saida.String(), "posterior ao da parada anterior"); n != 2 {
		t.Errorf("%d recusas de horário explicadas, want 2:\n%s", n, saida.String())
	}
}

// TestColetarParadas_EncerrarSoAPartirDeDuasParadas confere que a opção de
// encerrar a rota só aparece quando já há uma carona possível: com uma parada
// só, "5" está fora do menu e é recusado como qualquer número fora da faixa.
func TestColetarParadas_EncerrarSoAPartirDeDuasParadas(t *testing.T) {
	term, saida := terminalDeTeste(
		"1\n2026-09-20\n08:00\n" + // Salvador
			"5\n" + // ainda não há opção 5: recusado
			"2\n2026-09-20\n10:00\n" + // Feira de Santana
			"5\n") // agora sim, encerrar

	paradas, err := ColetarParadas(term, fusoDasCidadesFixo)
	if err != nil {
		t.Fatalf("ColetarParadas: %v", err)
	}
	if len(paradas) != 2 {
		t.Fatalf("paradas = %+v, want 2", paradas)
	}
	if n := strings.Count(saida.String(), "Encerrar a rota"); n != 1 {
		t.Errorf("opção de encerrar exibida %d vezes, want 1 (só no menu da terceira parada):\n%s", n, saida.String())
	}
}

// TestColetarParadas_TodasAsCidadesEncerraSozinho confere que, com todas as
// cidades já na rota, o menu não pergunta mais nada: não há cidade que possa
// entrar sem repetir. A entrada termina logo depois da quarta parada; se o
// menu perguntasse de novo, a leitura devolveria ErrEntradaEncerrada.
func TestColetarParadas_TodasAsCidadesEncerraSozinho(t *testing.T) {
	term, _ := terminalDeTeste(
		"1\n2026-09-20\n06:00\n" +
			"2\n2026-09-20\n08:00\n" +
			"3\n2026-09-20\n11:00\n" +
			"4\n2026-09-20\n13:30\n")

	paradas, err := ColetarParadas(term, fusoDasCidadesFixo)
	if err != nil {
		t.Fatalf("ColetarParadas: %v", err)
	}
	if len(paradas) != len(dominio.CidadesAtendidas()) {
		t.Fatalf("paradas = %+v, want uma por cidade", paradas)
	}
}

// TestColetarParadas_FimDaEntrada confere que EOF no meio da rota sobe como
// ErrEntradaEncerrada, a única saída de erro das leituras, para que o menu
// encerre a sessão em vez de publicar uma rota pela metade.
func TestColetarParadas_FimDaEntrada(t *testing.T) {
	term, _ := terminalDeTeste("1\n2026-09-20\n08:00\n")

	if _, err := ColetarParadas(term, fusoDasCidadesFixo); !errors.Is(err, ErrEntradaEncerrada) {
		t.Fatalf("err = %v, want ErrEntradaEncerrada", err)
	}
}

// TestFusoDasCidades confere que os instantes montados pelo cliente saem no
// deslocamento das cidades atendidas, todas na Bahia.
//
// Vale com ou sem tzdata na máquina: se LoadLocation falhar, o deslocamento
// fixo responde o mesmo -03:00. O que não pode acontecer é o cliente cair em
// UTC, que é o fuso local dentro do contêiner Alpine.
func TestFusoDasCidades(t *testing.T) {
	fuso := FusoDasCidades()

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

// TestEscolherCidade confere que a cidade sai com a grafia canônica das
// cidades atendidas. A seção 3 do PROTOCOL.md compara cidade por igualdade
// exata e não normaliza grafia: é o menu enumerado que garante a string
// correta.
func TestEscolherCidade(t *testing.T) {
	term, saida := terminalDeTeste("4\n")

	cidade, err := EscolherCidade(term, "Destino:")
	if err != nil {
		t.Fatalf("EscolherCidade: %v", err)
	}
	if cidade != "Vitória da Conquista" {
		t.Errorf("cidade = %q, want %q", cidade, "Vitória da Conquista")
	}
	if !dominio.CidadeConhecida(cidade) {
		t.Errorf("cidade %q não é reconhecida pelo domínio", cidade)
	}

	// Todas as cidades atendidas precisam estar no menu, ou uma delas ficaria
	// inalcançável pelo cliente.
	for _, esperada := range dominio.CidadesAtendidas() {
		if !strings.Contains(saida.String(), esperada) {
			t.Errorf("cidade %q ausente do menu:\n%s", esperada, saida.String())
		}
	}
}
