package dominio

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// logQueFalha simula disco cheio ou fsync com erro.
type logQueFalha struct{}

func (logQueFalha) Anexar([]byte) error { return errors.New("disco cheio") }

// publicarDeTeste publica uma carona simples de joao e devolve o estado.
func publicarDeTeste(t *testing.T, e *Estado) Carona {
	t.Helper()
	c, err := e.PublicarCarona("joao", []string{"Salvador", "Feira de Santana", "Jequié"},
		[]time.Time{as(6, 0), as(7, 30), as(10, 45)}, 3, []int{1500, 2500}, agoraDaPublicacao())
	if err != nil {
		t.Fatalf("PublicarCarona: %v", err)
	}
	return c
}

// reiniciar simula o boot: estado novo, só com o que está no log.
func reiniciar(t *testing.T, antes *Estado) *Estado {
	t.Helper()
	depois := estadoDeTeste()
	if err := depois.Reaplicar(antes.log.(*LogMemoria).Registros()); err != nil {
		t.Fatalf("Reaplicar: %v", err)
	}
	return depois
}

// A gravação acontece antes de a operação retornar (regra 3).
func TestPublicarCarona_GravaNoLogAntesDeRetornar(t *testing.T) {
	e := estadoDeTeste()
	c := publicarDeTeste(t, e)

	registros := e.log.(*LogMemoria).Registros()
	if len(registros) != 1 {
		t.Fatalf("registros = %d, want 1", len(registros))
	}
	var env struct {
		Seq     uint64 `json:"seq"`
		Lamport uint64 `json:"lamport"`
		Tipo    string `json:"tipo"`
		Dados   struct {
			Carona string `json:"carona"`
		} `json:"dados"`
	}
	if err := json.Unmarshal(registros[0], &env); err != nil {
		t.Fatalf("registro inválido: %v", err)
	}
	if env.Seq != 1 || env.Lamport != 0 || env.Tipo != "CARONA_PUBLICADA" || env.Dados.Carona != c.ID {
		t.Errorf("envelope = %+v, want seq 1, lamport 0, CARONA_PUBLICADA, carona %s", env, c.ID)
	}
}

// Write-ahead: se o log falha, a operação falha e o estado não muda. A ordem
// "grava, depois aplica" é o que impede o servidor de lembrar de algo que o
// disco não guardou.
func TestPublicarCarona_FalhaDoLogNaoAlteraEstado(t *testing.T) {
	e := estadoDeTeste()
	e.UsarLog(logQueFalha{})

	_, err := e.PublicarCarona("joao", []string{"Salvador", "Feira de Santana"},
		[]time.Time{as(6, 0), as(7, 30)}, 3, []int{1500}, agoraDaPublicacao())
	if !errors.Is(err, ErrFalhaNoLog) {
		t.Fatalf("erro = %v, want ErrFalhaNoLog", err)
	}
	if got := e.CaronasDoMotorista("joao", true); len(got) != 0 {
		t.Errorf("carona entrou no estado apesar da falha do log: %v", got)
	}
}

func TestCancelarCarona_FalhaDoLogNaoAlteraEstado(t *testing.T) {
	e := estadoDeTeste()
	c := publicarDeTeste(t, e)
	e.UsarLog(logQueFalha{})

	if _, err := e.CancelarCarona(c.ID, "joao", agoraDaPublicacao()); !errors.Is(err, ErrFalhaNoLog) {
		t.Fatalf("erro = %v, want ErrFalhaNoLog", err)
	}
	if got := e.CaronasDoMotorista("joao", false); len(got) != 1 {
		t.Errorf("carona deveria continuar de pé, got %v", got)
	}
}

// Publicar e reabrir: a carona volta com o mesmo ID e os mesmos campos.
func TestReaplicar_PublicadaVolta(t *testing.T) {
	e := estadoDeTeste()
	c := publicarDeTeste(t, e)

	depois := reiniciar(t, e)
	volta, _, err := depois.DetalharCarona(c.ID, "joao")
	if err != nil {
		t.Fatalf("carona %s não voltou: %v", c.ID, err)
	}
	if volta.ID != c.ID || volta.Assentos != 3 || len(volta.Rota) != 3 || volta.Cancelada {
		t.Errorf("carona restaurada = %+v, want %+v", volta, c)
	}
	for i := range c.Horarios {
		if !volta.Horarios[i].Equal(c.Horarios[i]) {
			t.Errorf("horário %d = %v, want %v", i, volta.Horarios[i], c.Horarios[i])
		}
	}
	for i := range c.Livres {
		if volta.Livres[i] != 3 || volta.PrecoTrecho[i] != c.PrecoTrecho[i] {
			t.Errorf("trecho %d: livres %d, preço %d", i, volta.Livres[i], volta.PrecoTrecho[i])
		}
	}
}

// Cancelar e reabrir: a carona continua cancelada.
func TestReaplicar_CanceladaContinuaCancelada(t *testing.T) {
	e := estadoDeTeste()
	c := publicarDeTeste(t, e)
	if _, err := e.CancelarCarona(c.ID, "joao", agoraDaPublicacao()); err != nil {
		t.Fatalf("CancelarCarona: %v", err)
	}

	depois := reiniciar(t, e)
	volta, _, err := depois.DetalharCarona(c.ID, "joao")
	if err != nil {
		t.Fatalf("DetalharCarona: %v", err)
	}
	if !volta.Cancelada {
		t.Error("carona voltou de pé depois do replay")
	}
}

// D37: o replay não revalida. Uma carona cuja partida já passou volta mesmo
// assim, porque foi aceita quando a partida ainda estava no futuro.
func TestReaplicar_NaoRevalida(t *testing.T) {
	linha := `{"seq":1,"lamport":0,"tipo":"CARONA_PUBLICADA","dados":{"carona":"vaijunto:car-aa11bb22","motorista":"joao","rota":["Salvador","Feira de Santana"],"horarios":["2020-01-01T06:00:00-03:00","2020-01-01T07:30:00-03:00"],"assentos":2,"precos_centavos":[1500]}}`
	e := estadoDeTeste()
	if err := e.Reaplicar([][]byte{[]byte(linha)}); err != nil {
		t.Fatalf("Reaplicar recusou carona de partida passada: %v", err)
	}
	if _, _, err := e.DetalharCarona("vaijunto:car-aa11bb22", "joao"); err != nil {
		t.Errorf("carona não voltou: %v", err)
	}
}

// O replay usa o ID gravado e não consome nem gera ID novo.
func TestReaplicar_UsaOIDDoRegistro(t *testing.T) {
	linha := `{"seq":7,"lamport":0,"tipo":"CARONA_PUBLICADA","dados":{"carona":"vaijunto:car-cafe0001","motorista":"joao","rota":["Salvador","Feira de Santana"],"horarios":["2026-10-01T06:00:00-03:00","2026-10-01T07:30:00-03:00"],"assentos":2,"precos_centavos":[1500]}}`
	e := estadoDeTeste()
	if err := e.Reaplicar([][]byte{[]byte(linha)}); err != nil {
		t.Fatalf("Reaplicar: %v", err)
	}
	lista := e.CaronasDoMotorista("joao", true)
	if len(lista) != 1 || lista[0].ID != "vaijunto:car-cafe0001" {
		t.Errorf("caronas = %+v", lista)
	}
	// O seq é restaurado: o próximo registro continua a numeração.
	e.UsarLog(NovaLogMemoria())
	publicarDeTeste(t, e)
	var env struct{ Seq uint64 }
	json.Unmarshal(e.log.(*LogMemoria).Registros()[0], &env)
	if env.Seq != 8 {
		t.Errorf("seq depois do replay = %d, want 8", env.Seq)
	}
}

// Registro que o domínio não entende é corrupção: o boot recusa.
func TestReaplicar_RecusaRegistroIncoerente(t *testing.T) {
	casos := map[string]string{
		"tipo desconhecido":   `{"seq":1,"lamport":0,"tipo":"INVENTADO","dados":{}}`,
		"dados ilegíveis":     `{"seq":1,"lamport":0,"tipo":"CARONA_PUBLICADA","dados":"texto"}`,
		"cancela inexistente": `{"seq":1,"lamport":0,"tipo":"CARONA_CANCELADA","dados":{"carona":"vaijunto:car-00000000"}}`,
		"envelope ilegível":   `[1,2,3]`,
	}
	for nome, linha := range casos {
		t.Run(nome, func(t *testing.T) {
			err := estadoDeTeste().Reaplicar([][]byte{[]byte(linha)})
			if err == nil {
				t.Fatal("Reaplicar aceitou registro incoerente")
			}
			if !strings.Contains(err.Error(), "registro 1") {
				t.Errorf("mensagem sem o número do registro: %v", err)
			}
		})
	}
}

func TestReaplicar_RecusaIDDuplicado(t *testing.T) {
	linha := `{"seq":1,"lamport":0,"tipo":"CARONA_PUBLICADA","dados":{"carona":"vaijunto:car-aa11bb22","motorista":"joao","rota":["Salvador","Feira de Santana"],"horarios":["2026-10-01T06:00:00-03:00","2026-10-01T07:30:00-03:00"],"assentos":2,"precos_centavos":[1500]}}`
	if err := estadoDeTeste().Reaplicar([][]byte{[]byte(linha), []byte(linha)}); err == nil {
		t.Fatal("Reaplicar aceitou o mesmo ID duas vezes")
	}
}
