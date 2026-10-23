package federacao

import (
	"context"
	"errors"
	"testing"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/transacao"
)

// Testes da rede em memória (PROJETO_P2.md, seção 5.4).
//
// O que importa afirmar aqui é que ela é um substituto **fiel**: o mesmo pedido
// produz o mesmo voto pelos dois caminhos, e as falhas injetadas aparecem com os
// mesmos erros que a rede de verdade produz (D30). Uma rede em memória que
// respondesse diferente faria os testes de coordenação das fases 5 e 7
// afirmarem coisas sobre um protocolo que não é o nosso.

// hubDeTeste monta o hub com uma empresa dona de uma carona de três assentos.
func hubDeTeste(t *testing.T) (*RedeEmMemoria, *dominio.Estado) {
	t.Helper()
	estado := parComUmaCarona(t, "vaijunto")
	hub := NovaRedeEmMemoria()
	hub.Registrar("vaijunto", transacao.NovoParticipante(estado))
	return hub, estado
}

// pedidoDeTeste é o pedido bem formado que os casos reaproveitam.
func pedidoDeTeste(txID string, itens ...dominio.ItemReserva) dominio.PedidoPreparacao {
	return dominio.PedidoPreparacao{
		TxID:          txID,
		Coordenador:   "rotacapixaba",
		Participantes: []string{"rotacapixaba", "vaijunto"},
		Carimbo:       dominio.Carimbo{Lamport: 41, Empresa: "rotacapixaba"},
		Passageiro:    dominio.Passageiro{Ref: "rotacapixaba:maria", Nome: "Maria Souza"},
		Itens:         itens,
	}
}

// O voto que sai da rede em memória é o mesmo que sai do handler HTTP, inclusive
// no motivo do NÃO — que é o que o coordenador lê para decidir.
func TestRedeEmMemoria_MesmoVotoDoCaminhoHTTP(t *testing.T) {
	hub, estado := hubDeTeste(t)
	rede := hub.De("rotacapixaba")
	ctx := context.Background()

	voto, err := rede.Preparar(ctx, "vaijunto", pedidoDeTeste(txDeTeste, dominio.ItemReserva{CaronaID: "vaijunto:car-1", De: 0, Ate: 2}))
	if err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	if voto.Voto != transacao.VotoSim || len(voto.Pernas) != 1 {
		t.Fatalf("voto = %+v, want SIM com 1 perna", voto)
	}
	if voto.Pernas[0].PrecoCentavos != 21500 {
		t.Errorf("preço da perna = %d, want 21500", voto.Pernas[0].PrecoCentavos)
	}

	// O mesmo pedido pelo handler HTTP responde igual.
	porHTTP := votoDaResposta(t, pedirTransacao(configDeTransacoes(estado), "rotacapixaba", txDeTeste, "preparar",
		corpoPreparar(itemDeFio("vaijunto:car-1", 0, 2))))
	if porHTTP.Voto != string(voto.Voto) || len(porHTTP.Pernas) != len(voto.Pernas) {
		t.Errorf("HTTP respondeu %+v e a rede em memória %+v", porHTTP, voto)
	}
}

// Motivo de voto NÃO: a tradução é a mesma tabela da borda, e por isso o
// coordenador não precisa saber por qual rede falou.
func TestRedeEmMemoria_VotoNaoTrazMotivoETrecho(t *testing.T) {
	hub, _ := hubDeTeste(t)
	rede := hub.De("rotacapixaba")
	ctx := context.Background()
	item := dominio.ItemReserva{CaronaID: "vaijunto:car-1", De: 0, Ate: 1}

	for i, tx := range []string{"rotacapixaba:res-1", "rotacapixaba:res-2", "rotacapixaba:res-3"} {
		if _, err := rede.Preparar(ctx, "vaijunto", pedidoDeTeste(tx, item)); err != nil {
			t.Fatalf("Preparar %d: %v", i, err)
		}
	}

	voto, err := rede.Preparar(ctx, "vaijunto", pedidoDeTeste("rotacapixaba:res-4", item))
	if err != nil {
		t.Fatalf("Preparar sem assento: %v", err)
	}
	if voto.Voto != transacao.VotoNao || voto.Motivo != motivoSemAssento {
		t.Fatalf("voto = %+v, want NAO/SEM_ASSENTO", voto)
	}
	if voto.CaronaID != "vaijunto:car-1" || voto.Trecho != 0 {
		t.Errorf("voto = %+v, want carona vaijunto:car-1 e trecho 0", voto)
	}
}

// Confirmar e abortar atravessam a rede e devolvem a situação do participante.
func TestRedeEmMemoria_ConfirmarEAbortar(t *testing.T) {
	hub, _ := hubDeTeste(t)
	rede := hub.De("rotacapixaba")
	ctx := context.Background()
	item := dominio.ItemReserva{CaronaID: "vaijunto:car-1", De: 0, Ate: 1}

	if _, err := rede.Preparar(ctx, "vaijunto", pedidoDeTeste("rotacapixaba:res-1", item)); err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	if situacao, err := rede.Confirmar(ctx, "vaijunto", "rotacapixaba:res-1"); err != nil || situacao != dominio.SituacaoConfirmado {
		t.Errorf("Confirmar = %q, %v; want CONFIRMADO, nil", situacao, err)
	}
	// Abortar transação desconhecida grava o veto e responde ABORTADO (D38).
	if situacao, err := rede.Abortar(ctx, "vaijunto", "rotacapixaba:res-9"); err != nil || situacao != dominio.SituacaoAbortado {
		t.Errorf("Abortar = %q, %v; want ABORTADO, nil", situacao, err)
	}
}

// A violação de protocolo chega a quem chama como *ErroHTTP com status 409, o
// mesmo formato do cliente HTTP: é por ele que a política da seção 1.3 sabe
// parar de repetir.
func TestRedeEmMemoria_ViolacaoChegaComo409(t *testing.T) {
	hub, _ := hubDeTeste(t)
	rede := hub.De("rotacapixaba")

	_, err := rede.Confirmar(context.Background(), "vaijunto", "rotacapixaba:res-desconhecida")

	var erroHTTP *ErroHTTP
	if !errors.As(err, &erroHTTP) {
		t.Fatalf("err = %v, want *ErroHTTP", err)
	}
	if erroHTTP.Status != 409 || erroHTTP.Codigo != codigoViolacaoProtocolo {
		t.Errorf("erro = %+v, want 409 VIOLACAO_PROTOCOLO", erroHTTP)
	}
}

// Papel inválido é recusado também na rede em memória, para que nenhum teste
// passe por um caminho que o servidor de verdade recusaria (seção 1.2).
func TestRedeEmMemoria_RecusaQuemNaoEhCoordenador(t *testing.T) {
	hub, _ := hubDeTeste(t)

	_, err := hub.De("rotafluminense").Confirmar(context.Background(), "vaijunto", "rotacapixaba:res-1")

	var erroHTTP *ErroHTTP
	if !errors.As(err, &erroHTTP) || erroHTTP.Status != 403 {
		t.Fatalf("err = %v, want *ErroHTTP 403", err)
	}
}

// Consultar é a exceção da tabela de papéis (seção 1.2 da API): aceita de
// qualquer empresa do consórcio, e não só do coordenador.
//
// A rede em memória precisa ter a mesma abertura do servidor de verdade, ou a
// terminação cooperativa ficaria intestável exatamente no caminho que ela usa
// — um participante em dúvida pergunta a outro participante, e nenhum dos dois
// é o coordenador.
func TestRedeEmMemoria_ConsultarAceitoDeQualquerEmpresa(t *testing.T) {
	hub, estado := hubDeTeste(t)

	// rotafluminense pergunta a vaijunto sobre uma transação de rotacapixaba:
	// três empresas, nenhuma delas o coordenador do lado de quem chama.
	resposta, err := hub.De("rotafluminense").Consultar(context.Background(), "vaijunto", txDeTeste)
	if err != nil {
		t.Fatalf("Consultar: %v", err)
	}
	if resposta.Papel != transacao.PapelParticipante || resposta.Situacao != transacao.SituacaoDesconhecida {
		t.Fatalf("resposta = %+v, want PARTICIPANTE/DESCONHECIDA", resposta)
	}

	// E a consulta vetou, como no caminho HTTP: é o efeito colateral que faz
	// dela um POST (D38).
	pt, existe := estado.Participacao(txDeTeste)
	if !existe || pt.Situacao != dominio.SituacaoAbortado {
		t.Errorf("veto = %+v (existe=%t), want ABORTADO", pt, existe)
	}
}

// A mesma consulta, agora sobre uma transação que o destino conhece, responde
// a situação guardada — igual ao que o handler HTTP devolve.
func TestRedeEmMemoria_ConsultarDevolveASituacaoConhecida(t *testing.T) {
	hub, _ := hubDeTeste(t)
	rede := hub.De("rotacapixaba")
	ctx := context.Background()

	if _, err := rede.Preparar(ctx, "vaijunto", pedidoDeTeste(txDeTeste, dominio.ItemReserva{CaronaID: "vaijunto:car-1", De: 0, Ate: 2})); err != nil {
		t.Fatalf("Preparar: %v", err)
	}

	resposta, err := rede.Consultar(ctx, "vaijunto", txDeTeste)
	if err != nil {
		t.Fatalf("Consultar: %v", err)
	}
	if resposta.Papel != transacao.PapelParticipante || resposta.Situacao != string(dominio.SituacaoPreparado) {
		t.Errorf("resposta = %+v, want PARTICIPANTE/PREPARADO", resposta)
	}
}

// Empresa não registrada é ErrParDesconhecido, como uma empresa fora de
// --pares: erro de configuração nossa, que nenhuma repetição conserta (D20).
func TestRedeEmMemoria_ParDesconhecido(t *testing.T) {
	hub, _ := hubDeTeste(t)

	_, err := hub.De("rotacapixaba").Confirmar(context.Background(), "rotafluminense", "rotacapixaba:res-1")
	if !errors.Is(err, ErrParDesconhecido) {
		t.Errorf("err = %v, want ErrParDesconhecido", err)
	}
}

// Atraso acima do prazo de quem chama vira ErrParIndisponivel, e o participante
// nunca é chamado: é o caso que a D30 trata como falha detectada por timeout.
func TestRedeEmMemoria_AtrasoEstouraOPrazoSemTocarOEstado(t *testing.T) {
	hub, estado := hubDeTeste(t)
	hub.Atrasar("vaijunto", 2*time.Second)

	ctx, cancelar := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelar()

	inicio := time.Now()
	_, err := hub.De("rotacapixaba").Preparar(ctx, "vaijunto",
		pedidoDeTeste(txDeTeste, dominio.ItemReserva{CaronaID: "vaijunto:car-1", De: 0, Ate: 1}))

	if !errors.Is(err, ErrParIndisponivel) {
		t.Fatalf("err = %v, want ErrParIndisponivel", err)
	}
	// A espera respeita o prazo em vez de dormir o atraso inteiro: é isso que
	// permite aos testes rodarem em milissegundos.
	if decorrido := time.Since(inicio); decorrido > time.Second {
		t.Errorf("a chamada esperou %v, want até o prazo do ctx", decorrido)
	}
	if n := len(estado.Participacoes()); n != 0 {
		t.Errorf("o participante foi chamado apesar do prazo estourado: %d participações", n)
	}
}

// Atraso abaixo do prazo é entregue normalmente.
func TestRedeEmMemoria_AtrasoDentroDoPrazoEntrega(t *testing.T) {
	hub, _ := hubDeTeste(t)
	hub.Atrasar("vaijunto", 5*time.Millisecond)

	ctx, cancelar := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelar()

	voto, err := hub.De("rotacapixaba").Preparar(ctx, "vaijunto",
		pedidoDeTeste(txDeTeste, dominio.ItemReserva{CaronaID: "vaijunto:car-1", De: 0, Ate: 1}))
	if err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	if voto.Voto != transacao.VotoSim {
		t.Errorf("voto = %q, want SIM", voto.Voto)
	}
}

// Perda de mensagem: a chamada falha sem chegar ao participante, e a seguinte
// passa. É o caso que a idempotência da D28 cobre — repetir é seguro, porque a
// perda pode ter sido só da resposta.
func TestRedeEmMemoria_PerdaDeMensagemAtingeSoAsPrimeiras(t *testing.T) {
	hub, estado := hubDeTeste(t)
	hub.Perder("rotacapixaba", "vaijunto", OperacaoPreparar, 1)
	rede := hub.De("rotacapixaba")
	ctx := context.Background()
	pedido := pedidoDeTeste(txDeTeste, dominio.ItemReserva{CaronaID: "vaijunto:car-1", De: 0, Ate: 1})

	if _, err := rede.Preparar(ctx, "vaijunto", pedido); !errors.Is(err, ErrParIndisponivel) {
		t.Fatalf("primeira chamada: err = %v, want ErrParIndisponivel", err)
	}
	if n := len(estado.Participacoes()); n != 0 {
		t.Errorf("a mensagem perdida chegou ao participante")
	}

	if _, err := rede.Preparar(ctx, "vaijunto", pedido); err != nil {
		t.Fatalf("segunda chamada: %v", err)
	}
	if n := len(estado.Participacoes()); n != 1 {
		t.Errorf("participações = %d, want 1", n)
	}

	// A perda é por operação: o confirmar não foi atingido.
	if _, err := rede.Confirmar(ctx, "vaijunto", txDeTeste); err != nil {
		t.Errorf("Confirmar: %v", err)
	}
}

// Partição: para quem chama é indistinguível de um par fora do ar, nos dois
// sentidos, até o Reconectar.
func TestRedeEmMemoria_ParticaoEReconexao(t *testing.T) {
	hub, _ := hubDeTeste(t)
	hub.Registrar("rotacapixaba", transacao.NovoParticipante(parComUmaCarona(t, "rotacapixaba")))
	ctx := context.Background()

	hub.Particionar("rotacapixaba", "vaijunto")
	if _, err := hub.De("rotacapixaba").Abortar(ctx, "vaijunto", "rotacapixaba:res-1"); !errors.Is(err, ErrParIndisponivel) {
		t.Errorf("ida: err = %v, want ErrParIndisponivel", err)
	}
	if _, err := hub.De("vaijunto").Abortar(ctx, "rotacapixaba", "vaijunto:res-1"); !errors.Is(err, ErrParIndisponivel) {
		t.Errorf("volta: err = %v, want ErrParIndisponivel", err)
	}

	hub.Reconectar("rotacapixaba", "vaijunto")
	if _, err := hub.De("rotacapixaba").Abortar(ctx, "vaijunto", "rotacapixaba:res-1"); err != nil {
		t.Errorf("após reconectar: %v", err)
	}
}
