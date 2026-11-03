package transacao

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"vaijunto/internal/dominio"
)

// A exceção do 409 na política de repetição da seção 1.3 do API_CONSORCIO.md:
// "409 encerra a repetição. Ele indica violação de protocolo, que nenhuma
// repetição corrige. Quem recebe registra a violação no log e para."
//
// Os dois laços que repetem "sem limite" (D41) são a fase 2 do coordenador e o
// outbox do entregador. Nos dois, um 409 que não parasse significaria uma
// goroutine batendo de 2 em 2 s, para sempre, num pedido que o outro lado já
// provou incoerente — e, no caso do outbox, um aviso que nunca sai da lista,
// deixando a I-P2-3 ("outbox vazio após a quiescência") impossível de
// satisfazer.
//
// Os testes entram pelos pontos EXPORTADOS das duas camadas — Recuperar() —, e
// não pelos laços diretamente: é por ali que o boot os aciona (seção 9.3), e um
// teste que chamasse confirmarTodos à mão deixaria de cobrir o caminho real.

// erro409 é o que um par devolve quando responde 409 VIOLACAO_PROTOCOLO. Imita
// federacao.ErroHTTP só no que importa aqui: o método que ehViolacao consulta.
//
// É um tipo local em vez do original porque internal/transacao não importa
// internal/federacao — é a federação que importa esta camada (seção 5.3) —, e
// é justamente por isso que o coordenador decide pela interface erroComStatus,
// e não pelo tipo concreto.
type erro409 struct{}

func (erro409) Error() string   { return "par respondeu 409 VIOLACAO_PROTOCOLO" }
func (erro409) StatusHTTP() int { return 409 }

// redeQueViola conta as chamadas recebidas e responde sempre 409. Implementa
// Rede inteira porque a interface é o contrato da seção 5.4; só Confirmar e
// Cancelar são exercitados.
type redeQueViola struct {
	mu       sync.Mutex
	chamadas map[string]int
}

func novaRedeQueViola() *redeQueViola {
	return &redeQueViola{chamadas: make(map[string]int)}
}

func (r *redeQueViola) contar(operacao string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chamadas[operacao]++
}

func (r *redeQueViola) quantas(operacao string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.chamadas[operacao]
}

func (r *redeQueViola) Preparar(context.Context, string, dominio.PedidoPreparacao) (RespostaPreparar, error) {
	r.contar("preparar")
	return RespostaPreparar{}, erro409{}
}

func (r *redeQueViola) Confirmar(context.Context, string, string) (dominio.Situacao, error) {
	r.contar("confirmar")
	return "", erro409{}
}

func (r *redeQueViola) Abortar(context.Context, string, string) (dominio.Situacao, error) {
	r.contar("abortar")
	return "", erro409{}
}

func (r *redeQueViola) Consultar(context.Context, string, string) (RespostaConsulta, error) {
	r.contar("consultar")
	return RespostaConsulta{}, erro409{}
}

func (r *redeQueViola) Cancelar(context.Context, string, string) (dominio.Situacao, error) {
	r.contar("cancelar")
	return "", erro409{}
}

func (r *redeQueViola) AvisarCaronaCancelada(context.Context, string, string, string) (string, error) {
	r.contar("avisar-carona-cancelada")
	return "", erro409{}
}

var _ Rede = (*redeQueViola)(nil)

// prazoCurtoDeRepeticao é o intervalo entre repetições nestes testes. Precisa
// ser pequeno porque a afirmação é "não repetiu": o teste espera algumas
// janelas e confere que a contagem não andou.
const prazoCurtoDeRepeticao = 20 * time.Millisecond

// estadoComReservaConfirmada monta um Estado com uma reserva confirmada e não
// concluída, que é exatamente o que o boot encontra quando o coordenador caiu
// entre o DECISAO_CONFIRMAR e o CONFIRMAR (TF2, seção 9.3).
//
// A perna é de outra empresa, e a lista de participantes também: assim a fase 2
// só tem um destino, o remoto, e o teste mede uma chamada de rede por rodada,
// sem o participante local no meio.
func estadoComReservaConfirmada(t *testing.T, empresa, dona string) (*dominio.Estado, string) {
	t.Helper()

	estado := dominio.NovoEstado(empresa)
	txID := empresa + ":res-00000001"
	partida := time.Now().Add(48 * time.Hour)
	pernas := []dominio.Perna{{
		CaronaID:      dona + ":car-1",
		Empresa:       dona,
		MotoristaNome: "Rafael Moreira",
		De:            0,
		Ate:           1,
		Cidades:       []string{"Vitória da Conquista", "Vitória"},
		Partida:       partida,
		Chegada:       partida.Add(2 * time.Hour),
		PrecoCentavos: 18000,
	}}
	itens := []dominio.ItemReserva{{CaronaID: dona + ":car-1", De: 0, Ate: 1}}

	if _, _, err := estado.ConfirmarReserva(txID, empresa+":maria", []string{dona}, itens, pernas, time.Now()); err != nil {
		t.Fatalf("ConfirmarReserva: %v", err)
	}
	return estado, txID
}

// TestFase2ParaNo409 é a exceção da seção 1.3 na fase 2: a retomada do boot
// manda um CONFIRMAR, recebe 409 e não insiste.
func TestFase2ParaNo409(t *testing.T) {
	estado, txID := estadoComReservaConfirmada(t, "vaijunto", "rotacapixaba")
	rede := novaRedeQueViola()

	coordenador := NovoCoordenador(ConfigCoordenador{
		Empresa:    "vaijunto",
		Estado:     estado,
		Relogio:    NovoRelogio(),
		Rede:       rede,
		Consorcio:  []string{"vaijunto", "rotacapixaba"},
		PrazoFase2: prazoCurtoDeRepeticao,
	})
	defer coordenador.Parar()

	if retomadas := coordenador.Recuperar(); retomadas != 1 {
		t.Fatalf("Recuperar devolveu %d reserva(s) a retomar, want 1", retomadas)
	}

	// Cinco janelas de repetição: se o 409 não encerrasse o laço, a contagem
	// passaria de 1 com folga.
	time.Sleep(5 * prazoCurtoDeRepeticao)

	if quantas := rede.quantas("confirmar"); quantas != 1 {
		t.Errorf("CONFIRMAR enviado %d vez(es) após um 409, want 1: a seção 1.3 da API encerra a repetição", quantas)
	}

	// A transação é concluída: a divergência que o 409 denuncia não se conserta
	// repetindo, e deixar a reserva pendente para sempre faria a goroutine da
	// fase 2 nunca terminar.
	if naoConcluidas := estado.ReservasNaoConcluidas(); len(naoConcluidas) != 0 {
		t.Errorf("%d reserva(s) ainda não concluída(s), want 0: a fase 2 precisa terminar", len(naoConcluidas))
	}
	if _, achou := estado.Reserva(txID); !achou {
		t.Errorf("a reserva %q desapareceu do estado", txID)
	}
}

// TestOutboxParaNo409 é a mesma exceção no outbox (D39): o aviso é entregue
// uma vez, volta 409, e não é repetido nem fica para sempre na lista.
func TestOutboxParaNo409(t *testing.T) {
	estado, txID := estadoComReservaConfirmada(t, "vaijunto", "rotacapixaba")
	rede := novaRedeQueViola()

	// O cancelamento pelo passageiro (seção 10.1) é o caminho que enfileira um
	// aviso CANCELAR_PERNAS para cada participante remoto.
	avisos, err := estado.CancelarReserva(txID, "vaijunto:maria", time.Now())
	if err != nil {
		t.Fatalf("CancelarReserva: %v", err)
	}
	if len(avisos) != 1 {
		t.Fatalf("%d aviso(s) enfileirado(s), want 1", len(avisos))
	}

	entregador := NovoEntregador(ConfigEntregador{
		Empresa:      "vaijunto",
		Estado:       estado,
		PrazoEntrega: prazoCurtoDeRepeticao,
	})
	entregador.UsarRede(rede)
	defer entregador.Parar()

	if retomados := entregador.Recuperar(); retomados != 1 {
		t.Fatalf("Recuperar devolveu %d aviso(s) a entregar, want 1", retomados)
	}

	time.Sleep(5 * prazoCurtoDeRepeticao)

	if quantas := rede.quantas("cancelar"); quantas != 1 {
		t.Errorf("CANCELAR enviado %d vez(es) após um 409, want 1: a seção 1.3 da API encerra a repetição", quantas)
	}
	if pendentes := estado.AvisosPendentes(); len(pendentes) != 0 {
		t.Errorf("%d aviso(s) ainda no outbox, want 0: a I-P2-3 exige outbox vazio após a quiescência", len(pendentes))
	}
}

// TestEhViolacaoReconheceAsDuasFormas documenta o que ehViolacao aceita: o
// status 409 de um par (erroComStatus) e o sentinela do domínio, que é como a
// violação chega quando o participante é local, sem rede no meio.
func TestEhViolacaoReconheceAsDuasFormas(t *testing.T) {
	casos := []struct {
		nome string
		err  error
		want bool
	}{
		{"409 de um par", erro409{}, true},
		{"409 embrulhado", fmt.Errorf("fase 2: %w", erro409{}), true},
		{"sentinela do domínio", fmt.Errorf("local: %w", dominio.ErrViolacaoProtocolo), true},
		{"par fora do ar", errors.New("par não respondeu"), false},
		{"nil", nil, false},
	}
	for _, caso := range casos {
		if achou := ehViolacao(caso.err); achou != caso.want {
			t.Errorf("%s: ehViolacao = %t, want %t", caso.nome, achou, caso.want)
		}
	}
}
