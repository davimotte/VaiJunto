package dominio

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Testes do participante do 2PC (PROJETO_P2.md, seção 7.2; API_CONSORCIO.md,
// seções 4.3 a 4.5).
//
// Rodam sobre o estado, e não por HTTP, por dois motivos: `agora` é parâmetro,
// o que permite fixar o instante do prazo de dúvida, e o pacote é o mesmo, o
// que permite montar à mão situações que esta fase ainda não produz — como
// CANCELADO, que só nasce com os cancelamentos da fase 8, mas já tem linha nas
// tabelas da API.
//
// O coordenador dos pedidos é sempre `rotacapixaba`, e o dono é `vaijunto`: é a
// forma normal de um participante ser chamado, e deixa visível que nada aqui
// depende de o coordenador ser a própria empresa.

const txDeTeste = "rotacapixaba:res-3f0b11c4"

// pedido monta um PedidoPreparacao bem formado com os itens dados.
func pedido(tx string, itens ...ItemReserva) PedidoPreparacao {
	return pedidoComCarimbo(tx, Carimbo{Lamport: 41, Empresa: "rotacapixaba"}, itens...)
}

// pedidoComCarimbo é o mesmo pedido com o carimbo escolhido, para os testes do
// wait-die (seção 8.2), em que a precedência é justamente o que se afirma.
func pedidoComCarimbo(tx string, carimbo Carimbo, itens ...ItemReserva) PedidoPreparacao {
	return PedidoPreparacao{
		TxID:          tx,
		Coordenador:   "rotacapixaba",
		Participantes: []string{"rotacapixaba", "vaijunto"},
		Carimbo:       carimbo,
		Passageiro:    Passageiro{Ref: "rotacapixaba:maria", Nome: "Maria Souza"},
		Itens:         itens,
	}
}

// esgotarComConfirmado consome os assentos de um trecho com uma participação
// **CONFIRMADO**, isto é, sem deixar portador nenhum em PREPARADO.
//
// Faz falta desde o wait-die: um trecho esgotado por uma transação em
// PREPARADO não é mais SEM_ASSENTO, é disputa (seção 8.2). Quem quer o trecho
// cheio de verdade — assento vendido, que não volta — precisa confirmar.
func esgotarComConfirmado(t *testing.T, e *Estado, tx string, itens ...ItemReserva) {
	t.Helper()
	prepararNoCenario(t, e, tx, itens...)
	if _, err := e.Confirmar(tx); err != nil {
		t.Fatalf("Confirmar(%s): %v", tx, err)
	}
}

// aoPreparar é o instante em que os PREPARAR dos testes acontecem.
func aoPreparar() time.Time { return as(0, 0) }

// prepararNoCenario é o atalho dos PREPARAR bem-sucedidos, que aparecem como
// preparação em quase todos os testes de confirmar e abortar.
func prepararNoCenario(t *testing.T, e *Estado, tx string, itens ...ItemReserva) []Perna {
	t.Helper()
	pernas, err := e.Preparar(pedido(tx, itens...), aoPreparar())
	if err != nil {
		t.Fatalf("Preparar(%s, %v): %v", tx, itens, err)
	}
	return pernas
}

// cancelarParticipacaoNoTeste põe uma participação em CANCELADO como o
// cancelamento da fase 8 fará: a situação muda **e os assentos voltam**, que é
// o efeito do registro PERNAS_CANCELADAS (seção 4.8). Montar o estado à mão é o
// que permite cobrir agora as linhas CANCELADO das tabelas das seções 4.3 a 4.5,
// que esta fase ainda não produz.
func cancelarParticipacaoNoTeste(t *testing.T, e *Estado, txID string) {
	t.Helper()
	pt, existe := e.participacoes[txID]
	if !existe {
		t.Fatalf("participação %s não existe", txID)
	}
	devolverItens(e, pt.Itens)
	pt.Situacao = SituacaoCancelado
}

// conferirIP21 verifica a invariante mestra em todos os trechos de todas as
// caronas: Livres + confirmados + em espera == Assentos, com Livres >= 0
// (seção 11.1).
//
// É chamada depois de cada operação dos testes, e não só no fim: a invariante
// vale **sempre**, e conferi-la apenas no estado final deixaria passar uma
// violação transitória visível a qualquer outra goroutine.
func conferirIP21(t *testing.T, e *Estado) {
	t.Helper()
	for _, o := range e.Ocupacao() {
		if o.Livres < 0 {
			t.Errorf("I-P2-1: %s trecho %d com Livres = %d, negativo", o.CaronaID, o.Trecho, o.Livres)
		}
		if soma := o.Livres + o.Confirmados + o.EmEspera; soma != o.Assentos {
			t.Errorf("I-P2-1: %s trecho %d: livres %d + confirmados %d + em espera %d = %d, want %d",
				o.CaronaID, o.Trecho, o.Livres, o.Confirmados, o.EmEspera, soma, o.Assentos)
		}
	}
}

// ocupacaoDe devolve a conta de um trecho específico.
func ocupacaoDe(t *testing.T, e *Estado, caronaID string, trecho int) OcupacaoTrecho {
	t.Helper()
	for _, o := range e.Ocupacao() {
		if o.CaronaID == caronaID && o.Trecho == trecho {
			return o
		}
	}
	t.Fatalf("Ocupacao: trecho %d de %s não existe", trecho, caronaID)
	return OcupacaoTrecho{}
}

// tiposNoLog lista os tipos dos registros gravados, na ordem.
func tiposNoLog(t *testing.T, e *Estado) []string {
	t.Helper()
	var tipos []string
	for i, bruto := range e.log.(*LogMemoria).Registros() {
		var env struct {
			Tipo string `json:"tipo"`
		}
		if err := json.Unmarshal(bruto, &env); err != nil {
			t.Fatalf("registro %d ilegível: %v", i+1, err)
		}
		tipos = append(tipos, env.Tipo)
	}
	return tipos
}

// --- Carimbo (D24, seção 8.1) ---

// A ordem é total: decide pelo número e, em empate, pela empresa em ordem
// alfabética. Carimbo menor é transação mais velha. É isso que permite a dois
// donos diferentes decidirem a mesma disputa do mesmo jeito (seção 8.4).
func TestCarimbo_OrdemTotalPorLamportEDepoisEmpresa(t *testing.T) {
	a := Carimbo{Lamport: 41, Empresa: "rotacapixaba"}
	b := Carimbo{Lamport: 42, Empresa: "aaa"}
	c := Carimbo{Lamport: 41, Empresa: "vaijunto"}

	if !a.MaisVelhoQue(b) {
		t.Errorf("(41,rotacapixaba) devia ser mais velho que (42,aaa)")
	}
	if b.MaisVelhoQue(a) {
		t.Errorf("(42,aaa) não pode ser mais velho que (41,rotacapixaba)")
	}
	if !a.MaisVelhoQue(c) || c.MaisVelhoQue(a) {
		t.Errorf("empate em 41 devia desempatar por empresa: rotacapixaba antes de vaijunto")
	}
	if a.MaisVelhoQue(a) {
		t.Errorf("um carimbo não é mais velho que ele mesmo")
	}
}

// --- PREPARAR (seção 7.2, passos 1 a 4; seção 4.3 da API) ---

// O caminho feliz: o assento é descontado no PREPARAR, a participação nasce
// PREPARADO, o registro vai ao log ANTES de o voto SIM poder sair (D22, regra
// 3) e as pernas devolvidas trazem os dados autoritativos da seção 3.3.
func TestPreparar_DescontaAssentoGravaEDevolvePernas(t *testing.T) {
	e := estadoDoCenario(t)

	pernas := prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 2))

	if tipos := tiposNoLog(t, e); len(tipos) != 1 || tipos[0] != "PREPARADO" {
		t.Fatalf("registros no log = %v, want [PREPARADO]", tipos)
	}

	// car-1 tem 3 assentos e dois trechos; os dois foram pedidos.
	for _, trecho := range []int{0, 1} {
		o := ocupacaoDe(t, e, "vaijunto:car-1", trecho)
		if o.Livres != 2 || o.EmEspera != 1 || o.Confirmados != 0 {
			t.Errorf("trecho %d: livres %d, em espera %d, confirmados %d; want 2, 1, 0",
				trecho, o.Livres, o.EmEspera, o.Confirmados)
		}
	}
	conferirIP21(t, e)

	pt, existe := e.Participacao(txDeTeste)
	if !existe {
		t.Fatalf("participação de %s não existe", txDeTeste)
	}
	if pt.Situacao != SituacaoPreparado {
		t.Errorf("situação = %q, want PREPARADO", pt.Situacao)
	}
	if pt.Coordenador != "rotacapixaba" || pt.Carimbo != (Carimbo{41, "rotacapixaba"}) {
		t.Errorf("participação = %+v, want coordenador rotacapixaba e carimbo (41,rotacapixaba)", pt)
	}
	// D32: do passageiro, só a referência opaca e o nome de exibição.
	if pt.Passageiro != (Passageiro{Ref: "rotacapixaba:maria", Nome: "Maria Souza"}) {
		t.Errorf("passageiro guardado = %+v", pt.Passageiro)
	}
	// O prazo de dúvida é armado agora, mesmo que só a fase 6A o leia.
	if !pt.PreparadaEm.Equal(aoPreparar()) {
		t.Errorf("PreparadaEm = %v, want %v", pt.PreparadaEm, aoPreparar())
	}

	if len(pernas) != 1 {
		t.Fatalf("pernas = %d, want 1", len(pernas))
	}
	p := pernas[0]
	// Cidades traz todas as paradas de De a Ate, inclusive a intermediária: é
	// com elas que o coordenador verifica cidade repetida (D13, D34).
	if got, want := strings.Join(p.Cidades, "|"), "Salvador|Feira de Santana|Jequié"; got != want {
		t.Errorf("cidades = %q, want %q", got, want)
	}
	if p.Empresa != "vaijunto" || p.MotoristaNome != "João Silva" {
		t.Errorf("perna = %+v, want empresa vaijunto e motorista João Silva", p)
	}
	if p.PrecoCentavos != 2500+3500 {
		t.Errorf("preço = %d, want 6000", p.PrecoCentavos)
	}
	if !p.Partida.Equal(as(6, 0)) || !p.Chegada.Equal(as(10, 45)) {
		t.Errorf("partida %v e chegada %v, want 06:00 e 10:45", p.Partida, p.Chegada)
	}
}

// Idempotência pelo txID (D28): o mesmo PREPARAR recebido duas vezes produz a
// mesma resposta e desconta o assento uma vez só. É o que torna seguro o
// coordenador repetir depois de uma resposta perdida.
func TestPreparar_RepetidoNaoDescontaDeNovo(t *testing.T) {
	e := estadoDoCenario(t)

	primeiro := prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))
	segundo := prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))

	if fmt.Sprint(primeiro) != fmt.Sprint(segundo) {
		t.Errorf("a repetição devolveu pernas diferentes:\n%v\n%v", primeiro, segundo)
	}
	if o := ocupacaoDe(t, e, "vaijunto:car-1", 0); o.Livres != 2 || o.EmEspera != 1 {
		t.Errorf("livres %d e em espera %d após repetir; want 2 e 1", o.Livres, o.EmEspera)
	}
	if tipos := tiposNoLog(t, e); len(tipos) != 1 {
		t.Errorf("registros = %v, want só um PREPARADO", tipos)
	}
	conferirIP21(t, e)
}

// As quatro linhas de repetição da seção 4.3: PREPARADO, CONFIRMADO e
// CANCELADO respondem SIM com as mesmas pernas; ABORTADO responde NÃO.
func TestPreparar_RepeticaoPorSituacao(t *testing.T) {
	for _, caso := range []struct {
		situacao Situacao
		querErro error
	}{
		{SituacaoPreparado, nil},
		{SituacaoConfirmado, nil},
		{SituacaoCancelado, nil},
		{SituacaoAbortado, ErrTransacaoAbortada},
	} {
		t.Run(string(caso.situacao), func(t *testing.T) {
			e := estadoDoCenario(t)
			prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))
			// Cada situação é alcançada pela operação que a produz, para que o
			// estado do teste seja um estado que o protocolo de fato cria. A
			// exceção é CANCELADO, que só nasce na fase 8.
			switch caso.situacao {
			case SituacaoPreparado:
				// Já está.
			case SituacaoConfirmado:
				if _, err := e.Confirmar(txDeTeste); err != nil {
					t.Fatalf("Confirmar: %v", err)
				}
			case SituacaoAbortado:
				if _, err := e.Abortar(txDeTeste); err != nil {
					t.Fatalf("Abortar: %v", err)
				}
			case SituacaoCancelado:
				cancelarParticipacaoNoTeste(t, e, txDeTeste)
			}

			pernas, err := e.Preparar(pedido(txDeTeste, item("vaijunto:car-1", 0, 1)), aoPreparar())
			if !errors.Is(err, caso.querErro) {
				t.Fatalf("Preparar em %s: err = %v, want %v", caso.situacao, err, caso.querErro)
			}
			if caso.querErro == nil && len(pernas) != 1 {
				t.Errorf("Preparar em %s devolveu %d pernas, want 1", caso.situacao, len(pernas))
			}
			if caso.querErro != nil && pernas != nil {
				t.Errorf("Preparar em %s devolveu pernas junto com o NÃO: %v", caso.situacao, pernas)
			}
			conferirIP21(t, e)
		})
	}
}

// Primeira linha da tabela da seção 8.2: trecho esgotado **só por assentos
// confirmados** é NÃO na hora, com o trecho identificado para o diagnóstico e
// o registro de operações. Não há portador nenhum, logo não há o que esperar.
func TestPreparar_SemAssentoNaoGravaNada(t *testing.T) {
	e := estadoDoCenario(t)

	// car-4 tem um assento só, e aqui ele é vendido: confirmado não volta.
	esgotarComConfirmado(t, e, txDeTeste, item("vaijunto:car-4", 0, 1))
	conferirIP21(t, e)

	antes := len(tiposNoLog(t, e))
	_, err := e.Preparar(pedido("rotacapixaba:res-00000002", item("vaijunto:car-4", 0, 1)), aoPreparar())

	var detalhe *ErroSemAssento
	if !errors.As(err, &detalhe) {
		t.Fatalf("Preparar sem assento: err = %v, want *ErroSemAssento", err)
	}
	if detalhe.CaronaID != "vaijunto:car-4" || detalhe.IndiceTrecho != 0 {
		t.Errorf("detalhe = %+v, want car-4 trecho 0", detalhe)
	}
	if !errors.Is(err, ErrSemAssento) {
		t.Errorf("err não embrulha ErrSemAssento: %v", err)
	}

	// O NÃO não é gravado (seção 7.2): a repetição é uma nova avaliação.
	if depois := len(tiposNoLog(t, e)); depois != antes {
		t.Errorf("o voto NÃO gravou %d registro(s)", depois-antes)
	}
	if _, existe := e.Participacao("rotacapixaba:res-00000002"); existe {
		t.Errorf("o voto NÃO criou participação")
	}
	conferirIP21(t, e)
}

// Um PREPARAR com dois itens, um deles sem assento, não desconta nada do
// outro: os passos 1 a 3 só leem, e o passo 4 só começa depois de toda a
// validação passar (seção 7.2). Sem isso haveria estado parcial a desfazer.
func TestPreparar_RecusaNaoDescontaOsOutrosItens(t *testing.T) {
	e := estadoDoCenario(t)
	esgotarComConfirmado(t, e, txDeTeste, item("vaijunto:car-4", 0, 1))

	_, err := e.Preparar(pedido("rotacapixaba:res-00000002",
		item("vaijunto:car-1", 0, 1), item("vaijunto:car-4", 0, 1)), aoPreparar())
	if !errors.Is(err, ErrSemAssento) {
		t.Fatalf("err = %v, want ErrSemAssento", err)
	}

	if o := ocupacaoDe(t, e, "vaijunto:car-1", 0); o.Livres != 3 {
		t.Errorf("car-1 trecho 0 com livres = %d, want 3: a recusa descontou um item", o.Livres)
	}
	conferirIP21(t, e)
}

// Cada linha de recusa do passo 2 (D34) e da forma do pedido (seção 4.3).
// Nenhuma delas grava nada.
func TestPreparar_Recusas(t *testing.T) {
	cancelada := func(e *Estado) { e.caronas["vaijunto:car-3"].Cancelada = true }

	for _, caso := range []struct {
		nome     string
		antes    func(*Estado)
		p        PedidoPreparacao
		querErro error
	}{
		{
			nome:     "carona de outra empresa",
			p:        pedido(txDeTeste, item("rotafluminense:car-1", 0, 1)),
			querErro: ErrItemDeOutraEmpresa,
		},
		{
			nome:     "identificador sem prefixo de empresa",
			p:        pedido(txDeTeste, item("car-1", 0, 1)),
			querErro: ErrItemDeOutraEmpresa,
		},
		{
			nome:     "carona inexistente nesta empresa",
			p:        pedido(txDeTeste, item("vaijunto:car-99", 0, 1)),
			querErro: ErrCaronaNaoEncontrada,
		},
		{
			nome:     "carona cancelada",
			antes:    cancelada,
			p:        pedido(txDeTeste, item("vaijunto:car-3", 0, 1)),
			querErro: ErrCaronaCancelada,
		},
		{
			nome:     "índices fora da rota",
			p:        pedido(txDeTeste, item("vaijunto:car-1", 0, 9)),
			querErro: ErrItemInvalido,
		},
		{
			nome:     "de maior ou igual a ate",
			p:        pedido(txDeTeste, item("vaijunto:car-1", 1, 1)),
			querErro: ErrItemInvalido,
		},
		{
			nome:     "mesma carona duas vezes",
			p:        pedido(txDeTeste, item("vaijunto:car-1", 0, 1), item("vaijunto:car-1", 1, 2)),
			querErro: ErrItinerarioInvalido,
		},
		{
			nome:     "sem itens",
			p:        pedido(txDeTeste),
			querErro: ErrPedidoInvalido,
		},
		{
			nome: "coordenador divergente do prefixo do txID",
			p: func() PedidoPreparacao {
				p := pedido(txDeTeste, item("vaijunto:car-1", 0, 1))
				p.Coordenador = "rotafluminense"
				return p
			}(),
			querErro: ErrPedidoInvalido,
		},
		{
			nome: "participantes sem esta empresa",
			p: func() PedidoPreparacao {
				p := pedido(txDeTeste, item("vaijunto:car-1", 0, 1))
				p.Participantes = []string{"rotacapixaba"}
				return p
			}(),
			querErro: ErrPedidoInvalido,
		},
		{
			nome: "carimbo sem empresa",
			p: func() PedidoPreparacao {
				p := pedido(txDeTeste, item("vaijunto:car-1", 0, 1))
				p.Carimbo.Empresa = ""
				return p
			}(),
			querErro: ErrPedidoInvalido,
		},
		{
			nome: "referência de passageiro sem prefixo",
			p: func() PedidoPreparacao {
				p := pedido(txDeTeste, item("vaijunto:car-1", 0, 1))
				p.Passageiro.Ref = "maria"
				return p
			}(),
			querErro: ErrPedidoInvalido,
		},
		{
			nome: "passageiro sem nome de exibição",
			p: func() PedidoPreparacao {
				p := pedido(txDeTeste, item("vaijunto:car-1", 0, 1))
				p.Passageiro.Nome = ""
				return p
			}(),
			querErro: ErrPedidoInvalido,
		},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			e := estadoDoCenario(t)
			if caso.antes != nil {
				caso.antes(e)
			}

			if _, err := e.Preparar(caso.p, aoPreparar()); !errors.Is(err, caso.querErro) {
				t.Fatalf("err = %v, want %v", err, caso.querErro)
			}
			if tipos := tiposNoLog(t, e); len(tipos) != 0 {
				t.Errorf("a recusa gravou %v", tipos)
			}
			if _, existe := e.Participacao(caso.p.TxID); existe {
				t.Errorf("a recusa criou participação")
			}
			conferirIP21(t, e)
		})
	}
}

// D22, regra 3: sem o registro em disco nada é aplicado, e o voto SIM não sai.
// Um servidor que descontasse o assento e caísse antes do fsync voltaria sem
// saber que prometeu aquele assento a alguém.
func TestPreparar_FalhaNoLogNaoAplicaNada(t *testing.T) {
	e := estadoDoCenario(t)
	e.UsarLog(logQueFalha{})

	if _, err := e.Preparar(pedido(txDeTeste, item("vaijunto:car-1", 0, 1)), aoPreparar()); !errors.Is(err, ErrFalhaNoLog) {
		t.Fatalf("err = %v, want ErrFalhaNoLog", err)
	}
	if o := ocupacaoDe(t, e, "vaijunto:car-1", 0); o.Livres != 3 || o.EmEspera != 0 {
		t.Errorf("livres %d e em espera %d após falha de log; want 3 e 0", o.Livres, o.EmEspera)
	}
	if _, existe := e.Participacao(txDeTeste); existe {
		t.Errorf("a falha de log deixou participação no estado")
	}
	conferirIP21(t, e)
}

// --- CONFIRMAR (seção 4.4 da API) ---

// A linha PREPARADO → CONFIRMADO: grava, muda a situação e **mantém** os
// assentos descontados — o em espera virou confirmado, e a conta de I-P2-1 não
// se altera.
func TestConfirmar_MantemOsAssentosDescontados(t *testing.T) {
	e := estadoDoCenario(t)
	prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))

	situacao, err := e.Confirmar(txDeTeste)
	if err != nil {
		t.Fatalf("Confirmar: %v", err)
	}
	if situacao != SituacaoConfirmado {
		t.Errorf("situação = %q, want CONFIRMADO", situacao)
	}
	if tipos := tiposNoLog(t, e); strings.Join(tipos, ",") != "PREPARADO,CONFIRMADO" {
		t.Errorf("registros = %v, want [PREPARADO CONFIRMADO]", tipos)
	}

	o := ocupacaoDe(t, e, "vaijunto:car-1", 0)
	if o.Livres != 2 || o.Confirmados != 1 || o.EmEspera != 0 {
		t.Errorf("livres %d, confirmados %d, em espera %d; want 2, 1, 0", o.Livres, o.Confirmados, o.EmEspera)
	}
	conferirIP21(t, e)
}

// As quatro linhas da tabela da seção 4.4, inclusive as duas que a seção 7.4
// declara impossíveis e que por isso respondem violação de protocolo.
func TestConfirmar_PorSituacao(t *testing.T) {
	for _, caso := range []struct {
		nome        string
		montar      func(*testing.T, *Estado)
		querSituaca Situacao
		querErro    error
	}{
		{
			nome: "preparado",
			montar: func(t *testing.T, e *Estado) {
				prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))
			},
			querSituaca: SituacaoConfirmado,
		},
		{
			nome: "confirmado, repetição",
			montar: func(t *testing.T, e *Estado) {
				prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))
				if _, err := e.Confirmar(txDeTeste); err != nil {
					t.Fatalf("Confirmar: %v", err)
				}
			},
			querSituaca: SituacaoConfirmado,
		},
		{
			nome: "cancelado, o cancelamento chegou antes",
			montar: func(t *testing.T, e *Estado) {
				prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))
				cancelarParticipacaoNoTeste(t, e, txDeTeste)
			},
			querSituaca: SituacaoCancelado,
		},
		{
			nome: "abortado, impossível pela seção 7.4",
			montar: func(t *testing.T, e *Estado) {
				prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))
				if _, err := e.Abortar(txDeTeste); err != nil {
					t.Fatalf("Abortar: %v", err)
				}
			},
			querErro: ErrViolacaoProtocolo,
		},
		{
			nome:     "desconhecida, impossível pela seção 7.4",
			montar:   func(*testing.T, *Estado) {},
			querErro: ErrViolacaoProtocolo,
		},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			e := estadoDoCenario(t)
			caso.montar(t, e)
			antes := len(tiposNoLog(t, e))

			situacao, err := e.Confirmar(txDeTeste)
			if !errors.Is(err, caso.querErro) {
				t.Fatalf("err = %v, want %v", err, caso.querErro)
			}
			if caso.querErro == nil && situacao != caso.querSituaca {
				t.Errorf("situação = %q, want %q", situacao, caso.querSituaca)
			}
			// Nenhuma das linhas sem efeito grava, inclusive as de violação.
			if caso.querSituaca != SituacaoConfirmado || caso.nome != "preparado" {
				if depois := len(tiposNoLog(t, e)); depois != antes {
					t.Errorf("gravou %d registro(s) numa linha sem efeito", depois-antes)
				}
			}
			conferirIP21(t, e)
		})
	}
}

// --- ABORTAR (seção 4.5 da API) ---

// A linha PREPARADO → ABORTADO: devolve os assentos em espera. O assento volta
// por abort, nunca por prazo de quem votou SIM (seção 7.5).
func TestAbortar_DevolveOsAssentosEmEspera(t *testing.T) {
	e := estadoDoCenario(t)
	prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 2))

	situacao, err := e.Abortar(txDeTeste)
	if err != nil {
		t.Fatalf("Abortar: %v", err)
	}
	if situacao != SituacaoAbortado {
		t.Errorf("situação = %q, want ABORTADO", situacao)
	}
	for _, trecho := range []int{0, 1} {
		o := ocupacaoDe(t, e, "vaijunto:car-1", trecho)
		if o.Livres != 3 || o.EmEspera != 0 {
			t.Errorf("trecho %d: livres %d e em espera %d após abort; want 3 e 0", trecho, o.Livres, o.EmEspera)
		}
	}
	conferirIP21(t, e)
}

// As cinco linhas da tabela da seção 4.5, incluindo o veto da D38 e as duas
// transições impossíveis.
func TestAbortar_PorSituacao(t *testing.T) {
	for _, caso := range []struct {
		nome     string
		montar   func(*testing.T, *Estado)
		querErro error
	}{
		{
			nome: "preparado",
			montar: func(t *testing.T, e *Estado) {
				prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))
			},
		},
		{
			nome:   "desconhecida, grava o veto",
			montar: func(*testing.T, *Estado) {},
		},
		{
			nome: "abortado, repetição",
			montar: func(t *testing.T, e *Estado) {
				if _, err := e.Abortar(txDeTeste); err != nil {
					t.Fatalf("Abortar: %v", err)
				}
			},
		},
		{
			nome: "confirmado, impossível pela seção 7.4",
			montar: func(t *testing.T, e *Estado) {
				prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))
				if _, err := e.Confirmar(txDeTeste); err != nil {
					t.Fatalf("Confirmar: %v", err)
				}
			},
			querErro: ErrViolacaoProtocolo,
		},
		{
			nome: "cancelado, impossível pela seção 7.4",
			montar: func(t *testing.T, e *Estado) {
				prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))
				cancelarParticipacaoNoTeste(t, e, txDeTeste)
			},
			querErro: ErrViolacaoProtocolo,
		},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			e := estadoDoCenario(t)
			caso.montar(t, e)

			situacao, err := e.Abortar(txDeTeste)
			if !errors.Is(err, caso.querErro) {
				t.Fatalf("err = %v, want %v", err, caso.querErro)
			}
			if caso.querErro == nil && situacao != SituacaoAbortado {
				t.Errorf("situação = %q, want ABORTADO", situacao)
			}
			conferirIP21(t, e)
		})
	}
}

// O veto (D38) é uma participação ABORTADO sem itens, gravada ANTES da
// resposta. É ele que torna seguro abortar sem falar com o coordenador: um
// PREPARAR que chegue atrasado encontra a transação abortada e recebe NÃO, e
// portanto o coordenador nunca obterá unanimidade.
func TestAbortar_VetoRecusaPrepararAtrasado(t *testing.T) {
	e := estadoDoCenario(t)

	if _, err := e.Abortar(txDeTeste); err != nil {
		t.Fatalf("Abortar desconhecida: %v", err)
	}
	if tipos := tiposNoLog(t, e); len(tipos) != 1 || tipos[0] != "ABORTADO" {
		t.Fatalf("registros = %v, want [ABORTADO]", tipos)
	}

	pt, existe := e.Participacao(txDeTeste)
	if !existe || pt.Situacao != SituacaoAbortado || len(pt.Itens) != 0 {
		t.Fatalf("veto = %+v (existe=%t), want ABORTADO sem itens", pt, existe)
	}

	_, err := e.Preparar(pedido(txDeTeste, item("vaijunto:car-1", 0, 1)), aoPreparar())
	if !errors.Is(err, ErrTransacaoAbortada) {
		t.Fatalf("PREPARAR atrasado: err = %v, want ErrTransacaoAbortada", err)
	}
	if o := ocupacaoDe(t, e, "vaijunto:car-1", 0); o.Livres != 3 {
		t.Errorf("o PREPARAR recusado pelo veto descontou assento: livres = %d", o.Livres)
	}
	conferirIP21(t, e)
}

// --- Consulta e recuperação (D23, D38; seções 9.3 e 9.4; seção 4.6 da API) ---

// A tabela do coordenador da seção 4.6, pelo lado do domínio. As três linhas,
// e a afirmação que sustenta o abort presumido: responder ABORTADA **não grava
// nada**, então a resposta não compromete o servidor e pode sair sem fsync.
func TestDecisaoDaTransacao_TabelaDoCoordenador(t *testing.T) {
	for _, caso := range []struct {
		nome    string
		montar  func(*testing.T, *Estado) string
		querDec Decisao
		querLog []string
	}{
		{
			// Linha 1: DECISAO_CONFIRMAR no log.
			nome: "decisao de confirmar gravada",
			montar: func(t *testing.T, e *Estado) string {
				return reservarNoCenario(t, e, "maria", item("vaijunto:car-1", 0, 1)).ID
			},
			querDec: DecisaoConfirmada,
			querLog: []string{"PREPARADO", "DECISAO_CONFIRMAR", "CONFIRMADO"},
		},
		{
			// Linha 1, variante da nota da seção 4.6: a pergunta é sobre a
			// DECISÃO, e um cancelamento posterior não a desfaz.
			nome: "reserva confirmada e depois cancelada",
			montar: func(t *testing.T, e *Estado) string {
				r := reservarNoCenario(t, e, "maria", item("vaijunto:car-1", 0, 1))
				desativarReservaNoTeste(t, e, r.ID)
				return r.ID
			},
			querDec: DecisaoConfirmada,
			querLog: []string{"PREPARADO", "DECISAO_CONFIRMAR", "CONFIRMADO"},
		},
		{
			// Linha 2: a transação está na memória, ainda votando.
			nome: "transacao em votacao",
			montar: func(t *testing.T, e *Estado) string {
				e.MarcarTransacaoEmVotacao("vaijunto:res-0000abcd")
				return "vaijunto:res-0000abcd"
			},
			querDec: DecisaoEmAndamento,
		},
		{
			// Linha 3: nem uma coisa nem outra. É o abort presumido (D23), e é
			// o caminho de volta que a seção 7 do API_CONSORCIO.md demonstra
			// com curl, sem derrubar servidor nenhum.
			nome:    "desconhecida, abort presumido",
			montar:  func(*testing.T, *Estado) string { return "vaijunto:res-0000abcd" },
			querDec: DecisaoAbortada,
		},
		{
			// A marca sai no fim da tentativa, e depois dela a transação volta
			// a ser desconhecida: é o que acontece quando o coordenador aborta.
			nome: "votacao encerrada sem decisao",
			montar: func(t *testing.T, e *Estado) string {
				e.MarcarTransacaoEmVotacao("vaijunto:res-0000abcd")
				e.LiberarTransacaoEmVotacao("vaijunto:res-0000abcd")
				return "vaijunto:res-0000abcd"
			},
			querDec: DecisaoAbortada,
		},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			e := estadoDoCenario(t)
			txID := caso.montar(t, e)
			antes := tiposNoLog(t, e)

			if dec := e.DecisaoDaTransacao(txID); dec != caso.querDec {
				t.Errorf("decisão = %q, want %q", dec, caso.querDec)
			}
			// A consulta ao coordenador é somente leitura em TODAS as linhas,
			// inclusive na do abort presumido: o coordenador não grava nada
			// antes de decidir (D23; D37, regra 1).
			depois := tiposNoLog(t, e)
			if strings.Join(antes, ",") != strings.Join(depois, ",") {
				t.Errorf("a consulta gravou: log era %v, virou %v", antes, depois)
			}
			if caso.querLog != nil && strings.Join(depois, ",") != strings.Join(caso.querLog, ",") {
				t.Errorf("registros = %v, want %v", depois, caso.querLog)
			}
			conferirIP21(t, e)
		})
	}
}

// A tabela do participante da seção 4.6: quem conhece a transação responde a
// própria situação, e quem não conhece grava o veto e responde DESCONHECIDA.
func TestConsultarParticipacao_TabelaDoParticipante(t *testing.T) {
	for _, caso := range []struct {
		nome        string
		montar      func(*testing.T, *Estado)
		querSituaca Situacao
		querConheci bool
	}{
		{
			nome: "preparado",
			montar: func(t *testing.T, e *Estado) {
				prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))
			},
			querSituaca: SituacaoPreparado,
			querConheci: true,
		},
		{
			nome: "confirmado",
			montar: func(t *testing.T, e *Estado) {
				prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))
				if _, err := e.Confirmar(txDeTeste); err != nil {
					t.Fatalf("Confirmar: %v", err)
				}
			},
			querSituaca: SituacaoConfirmado,
			querConheci: true,
		},
		{
			nome: "abortado",
			montar: func(t *testing.T, e *Estado) {
				prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))
				if _, err := e.Abortar(txDeTeste); err != nil {
					t.Fatalf("Abortar: %v", err)
				}
			},
			querSituaca: SituacaoAbortado,
			querConheci: true,
		},
		{
			// Um veto já gravado é uma participação como outra qualquer: a
			// consulta seguinte responde ABORTADO e não grava de novo.
			nome: "abortado por veto anterior",
			montar: func(t *testing.T, e *Estado) {
				if _, err := e.Abortar(txDeTeste); err != nil {
					t.Fatalf("Abortar: %v", err)
				}
			},
			querSituaca: SituacaoAbortado,
			querConheci: true,
		},
		{
			nome: "cancelado",
			montar: func(t *testing.T, e *Estado) {
				prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))
				if _, err := e.Confirmar(txDeTeste); err != nil {
					t.Fatalf("Confirmar: %v", err)
				}
				cancelarParticipacaoNoTeste(t, e, txDeTeste)
			},
			querSituaca: SituacaoCancelado,
			querConheci: true,
		},
		{
			nome:        "desconhecida, grava o veto",
			montar:      func(*testing.T, *Estado) {},
			querConheci: false,
		},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			e := estadoDoCenario(t)
			caso.montar(t, e)

			situacao, conhecida, err := e.ConsultarParticipacao(txDeTeste)
			if err != nil {
				t.Fatalf("ConsultarParticipacao: %v", err)
			}
			if conhecida != caso.querConheci {
				t.Fatalf("conhecida = %t, want %t", conhecida, caso.querConheci)
			}
			if conhecida && situacao != caso.querSituaca {
				t.Errorf("situação = %q, want %q", situacao, caso.querSituaca)
			}
			conferirIP21(t, e)
		})
	}
}

// O coração da D38: consultar uma transação desconhecida **grava o veto antes
// de responder**, e o veto recusa um PREPARAR atrasado.
//
// Sem ele, a terminação não seria segura: quem perguntou abortaria com base
// numa resposta que não impede o coordenador de obter unanimidade depois, e o
// mesmo itinerário poderia acabar confirmado num dono e abortado no outro. É
// este teste que fecha o passo 3 do argumento da seção 7.4.
func TestConsultarParticipacao_DesconhecidaGravaVetoERecusaPrepararAtrasado(t *testing.T) {
	e := estadoDoCenario(t)

	situacao, conhecida, err := e.ConsultarParticipacao(txDeTeste)
	if err != nil || conhecida || situacao != "" {
		t.Fatalf("ConsultarParticipacao = (%q, %t, %v), want (vazia, false, nil)", situacao, conhecida, err)
	}

	// O veto está no log ANTES de a resposta DESCONHECIDA poder sair: quando a
	// função retorna, o fsync já aconteceu (D22, regra 3).
	if tipos := tiposNoLog(t, e); len(tipos) != 1 || tipos[0] != "ABORTADO" {
		t.Fatalf("registros = %v, want [ABORTADO]", tipos)
	}
	pt, existe := e.Participacao(txDeTeste)
	if !existe || pt.Situacao != SituacaoAbortado || len(pt.Itens) != 0 {
		t.Fatalf("veto = %+v (existe=%t), want ABORTADO sem itens", pt, existe)
	}

	_, err = e.Preparar(pedido(txDeTeste, item("vaijunto:car-1", 0, 1)), aoPreparar())
	if !errors.Is(err, ErrTransacaoAbortada) {
		t.Fatalf("PREPARAR atrasado: err = %v, want ErrTransacaoAbortada", err)
	}
	if o := ocupacaoDe(t, e, "vaijunto:car-1", 0); o.Livres != 3 {
		t.Errorf("o PREPARAR recusado pelo veto descontou assento: livres = %d", o.Livres)
	}
	conferirIP21(t, e)
}

// O prazo de dúvida (D41) separa quem acabou de votar SIM de quem está em
// PREPARADO tempo demais. Só o segundo entra na terminação cooperativa.
func TestParticipacoesEmDuvida_SoAsQueVenceramOPrazo(t *testing.T) {
	e := estadoDoCenario(t)
	prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))

	const prazo = 10 * time.Second
	if emDuvida := e.ParticipacoesEmDuvida(aoPreparar().Add(prazo-time.Second), prazo); len(emDuvida) != 0 {
		t.Errorf("antes do prazo: %d em dúvida, want 0", len(emDuvida))
	}
	emDuvida := e.ParticipacoesEmDuvida(aoPreparar().Add(prazo), prazo)
	if len(emDuvida) != 1 || emDuvida[0].TxID != txDeTeste {
		t.Fatalf("no prazo: %+v, want só %s", emDuvida, txDeTeste)
	}

	// Decidida não está mais em dúvida, por mais tempo que passe: só PREPARADO
	// tem assento em espera a devolver.
	if _, err := e.Confirmar(txDeTeste); err != nil {
		t.Fatalf("Confirmar: %v", err)
	}
	if emDuvida := e.ParticipacoesEmDuvida(aoPreparar().Add(time.Hour), prazo); len(emDuvida) != 0 {
		t.Errorf("depois de CONFIRMADO: %d em dúvida, want 0", len(emDuvida))
	}
}

// Primeira tarefa da seção 9.3. Como PreparadaEm não vai ao log (seção 4.4), o
// replay devolve a participação com o instante zerado, e ela ficaria invisível
// para o varredor da terminação — isto é, o assento nunca voltaria, que é
// exatamente o que a RD04 proíbe. O boot rearma o prazo.
func TestArmarPrazosDeDuvida_RecuperaOInstanteQueOLogNaoGuarda(t *testing.T) {
	antes := estadoDoCenario(t)
	prepararNoCenario(t, antes, txDeTeste, item("vaijunto:car-1", 0, 1))

	depois := estadoDoCenario(t)
	if _, err := depois.Reaplicar(antes.log.(*LogMemoria).Registros()); err != nil {
		t.Fatalf("Reaplicar: %v", err)
	}

	// Antes de armar, o varredor não a enxerga, por mais tempo que passe.
	if emDuvida := depois.ParticipacoesEmDuvida(as(23, 0), 10*time.Second); len(emDuvida) != 0 {
		t.Fatalf("participação do replay já em dúvida sem ter sido armada: %+v", emDuvida)
	}

	if n := depois.ArmarPrazosDeDuvida(as(1, 0)); n != 1 {
		t.Fatalf("ArmarPrazosDeDuvida = %d, want 1", n)
	}
	emDuvida := depois.ParticipacoesEmDuvida(as(1, 0).Add(10*time.Second), 10*time.Second)
	if len(emDuvida) != 1 || emDuvida[0].TxID != txDeTeste {
		t.Fatalf("após armar: %+v, want só %s", emDuvida, txDeTeste)
	}

	// Idempotente: uma segunda chamada não reinicia o prazo de quem já foi
	// armada, e com isso nenhuma participação escapa da terminação.
	if n := depois.ArmarPrazosDeDuvida(as(2, 0)); n != 0 {
		t.Errorf("ArmarPrazosDeDuvida repetida = %d, want 0", n)
	}
}

// Segunda tarefa da seção 9.3: o boot precisa achar as reservas cuja fase 2
// não terminou, que são as que o CONFIRMAR ainda tem de alcançar (passo 4 do
// argumento de atomicidade da seção 7.4).
func TestReservasNaoConcluidas_SaoAsQueAFase2NaoTerminou(t *testing.T) {
	e := estadoDoCenario(t)
	pendente := reservarNoCenario(t, e, "maria", item("vaijunto:car-1", 0, 1))
	concluida := reservarNoCenario(t, e, "joao", item("vaijunto:car-3", 0, 1))
	if err := e.ConcluirReserva(concluida.ID); err != nil {
		t.Fatalf("ConcluirReserva: %v", err)
	}

	naoConcluidas := e.ReservasNaoConcluidas()
	if len(naoConcluidas) != 1 || naoConcluidas[0].ID != pendente.ID {
		t.Fatalf("ReservasNaoConcluidas = %+v, want só %s", naoConcluidas, pendente.ID)
	}

	// Cancelada e não concluída continua na lista: o cancelamento da D39
	// pressupõe participações CONFIRMADO para cancelar (seção 7.3), então o
	// CONFIRMAR ainda precisa chegar.
	desativarReservaNoTeste(t, e, pendente.ID)
	if n := len(e.ReservasNaoConcluidas()); n != 1 {
		t.Errorf("reserva inativa e não concluída sumiu da lista: %d, want 1", n)
	}
}

// --- Portadores e ocupação (seções 4.3 e 11.1) ---

// Quem está em espera num trecho é derivado das participações em PREPARADO, e
// não guardado em paralelo na carona (seção 4.3). Como os assentos são
// contadores (D25), um trecho pode ter vários portadores ao mesmo tempo — e é
// contra **todos** eles que o wait-die compara o carimbo (seção 8.2).
func TestPortadores_DerivamDasParticipacoesPreparadas(t *testing.T) {
	e := estadoDoCenario(t)
	prepararNoCenario(t, e, "rotacapixaba:res-00000001", item("vaijunto:car-1", 0, 1))
	prepararNoCenario(t, e, "rotacapixaba:res-00000002", item("vaijunto:car-1", 0, 2))
	// Esta cobre só o segundo trecho: não é portadora do trecho 0.
	prepararNoCenario(t, e, "rotacapixaba:res-00000003", item("vaijunto:car-1", 1, 2))

	if n := len(portadores(e, "vaijunto:car-1", 0)); n != 2 {
		t.Errorf("portadores do trecho 0 = %d, want 2", n)
	}
	if n := len(portadores(e, "vaijunto:car-1", 1)); n != 2 {
		t.Errorf("portadores do trecho 1 = %d, want 2", n)
	}

	// Abortar uma delas a tira da lista, sem que nada mais mude.
	if _, err := e.Abortar("rotacapixaba:res-00000001"); err != nil {
		t.Fatalf("Abortar: %v", err)
	}
	lista := portadores(e, "vaijunto:car-1", 0)
	if len(lista) != 1 || lista[0].TxID != "rotacapixaba:res-00000002" {
		t.Errorf("portadores após o abort = %v, want só res-00000002", lista)
	}
	conferirIP21(t, e)
}

// A conta de I-P2-1 soma as participações CONFIRMADO e as reservas ativas do
// P1, que ainda descontam assentos até a fase 5 (seção 14).
func TestOcupacao_ContaConfirmadosEmEsperaEReservasDoP1(t *testing.T) {
	e := estadoDoCenario(t)

	reservarNoCenario(t, e, "maria", item("vaijunto:car-1", 0, 1))
	prepararNoCenario(t, e, "rotacapixaba:res-00000001", item("vaijunto:car-1", 0, 1))
	prepararNoCenario(t, e, "rotacapixaba:res-00000002", item("vaijunto:car-1", 0, 1))
	if _, err := e.Confirmar("rotacapixaba:res-00000002"); err != nil {
		t.Fatalf("Confirmar: %v", err)
	}

	o := ocupacaoDe(t, e, "vaijunto:car-1", 0)
	if o.Livres != 0 || o.Confirmados != 2 || o.EmEspera != 1 || o.Assentos != 3 {
		t.Errorf("ocupação = %+v, want livres 0, confirmados 2, em espera 1, assentos 3", o)
	}
	conferirIP21(t, e)
}

// --- Replay (D37, seção 9.2) ---

// O boot restaura participações e contadores: o estado reconstruído do log é
// indistinguível do que caiu.
func TestReplay_RestauraParticipacoesEContadores(t *testing.T) {
	antes := estadoDeTeste()
	c := publicarDeTeste(t, antes)
	prepararNoCenario(t, antes, "rotacapixaba:res-00000001", item(c.ID, 0, 2))
	prepararNoCenario(t, antes, "rotacapixaba:res-00000002", item(c.ID, 0, 1))
	if _, err := antes.Confirmar("rotacapixaba:res-00000001"); err != nil {
		t.Fatalf("Confirmar: %v", err)
	}

	depois := reiniciar(t, antes)

	if fmt.Sprint(depois.Ocupacao()) != fmt.Sprint(antes.Ocupacao()) {
		t.Errorf("ocupação após o reinício:\n%v\nwant\n%v", depois.Ocupacao(), antes.Ocupacao())
	}
	conferirIP21(t, depois)

	for _, tx := range []string{"rotacapixaba:res-00000001", "rotacapixaba:res-00000002"} {
		pt, existe := depois.Participacao(tx)
		if !existe {
			t.Fatalf("participação %s não voltou", tx)
		}
		original, _ := antes.Participacao(tx)
		// PreparadaEm não vai ao log: o prazo de dúvida recomeça no boot
		// (seção 4.4). O resto tem de voltar igual.
		original.PreparadaEm = time.Time{}
		if fmt.Sprint(pt) != fmt.Sprint(original) {
			t.Errorf("participação %s = %+v, want %+v", tx, pt, original)
		}
		if !pt.PreparadaEm.IsZero() {
			t.Errorf("participação %s voltou com PreparadaEm = %v, want zero", tx, pt.PreparadaEm)
		}
	}
}

// Abort gravado antes da queda devolve os assentos no replay, e o veto volta
// como participação abortada sem itens.
func TestReplay_AbortDevolveAssentosEVetoVolta(t *testing.T) {
	antes := estadoDeTeste()
	c := publicarDeTeste(t, antes)
	prepararNoCenario(t, antes, "rotacapixaba:res-00000001", item(c.ID, 0, 1))
	if _, err := antes.Abortar("rotacapixaba:res-00000001"); err != nil {
		t.Fatalf("Abortar: %v", err)
	}
	if _, err := antes.Abortar("rotacapixaba:res-00000009"); err != nil {
		t.Fatalf("Abortar desconhecida: %v", err)
	}

	depois := reiniciar(t, antes)

	if o := ocupacaoDe(t, depois, c.ID, 0); o.Livres != 3 || o.EmEspera != 0 {
		t.Errorf("livres %d e em espera %d após o replay do abort; want 3 e 0", o.Livres, o.EmEspera)
	}
	veto, existe := depois.Participacao("rotacapixaba:res-00000009")
	if !existe || veto.Situacao != SituacaoAbortado || len(veto.Itens) != 0 {
		t.Errorf("veto após o replay = %+v (existe=%t), want ABORTADO sem itens", veto, existe)
	}
	conferirIP21(t, depois)
}

// "Sem revalidar" não é "sem checar" (D37): um registro que o domínio não
// entende é corrupção, e o boot é recusado com o número do registro em vez de
// subir com estado inventado.
func TestReplay_RecusaRegistrosIncoerentes(t *testing.T) {
	envelope := func(seq int, tipo, dados string) []byte {
		return fmt.Appendf(nil, `{"seq":%d,"lamport":1,"tipo":%q,"dados":%s}`, seq, tipo, dados)
	}
	publicada := envelope(1, "CARONA_PUBLICADA", `{"carona":"vaijunto:car-x","motorista":"joao",
		"rota":["Salvador","Jequié"],"horarios":["2026-10-01T06:00:00-03:00","2026-10-01T10:00:00-03:00"],
		"assentos":1,"precos_centavos":[1000]}`)
	preparado := func(seq int, tx, carona string, de, ate int) []byte {
		return envelope(seq, "PREPARADO", fmt.Sprintf(`{"tx":%q,"coordenador":"rotacapixaba",
			"participantes":["rotacapixaba","vaijunto"],"itens":[{"carona":%q,"de":%d,"ate":%d}],
			"carimbo":{"lamport":41,"empresa":"rotacapixaba"},
			"passageiro":{"ref":"rotacapixaba:maria","nome":"Maria Souza"}}`, tx, carona, de, ate))
	}
	soTx := func(seq int, tipo, tx string) []byte {
		return envelope(seq, tipo, fmt.Sprintf(`{"tx":%q}`, tx))
	}

	for _, caso := range []struct {
		nome      string
		registros [][]byte
		quer      string
	}{
		{
			nome:      "preparado de carona inexistente",
			registros: [][]byte{preparado(1, "rotacapixaba:res-1", "vaijunto:car-x", 0, 1)},
			quer:      "não existe",
		},
		{
			nome:      "preparado com trecho fora da rota",
			registros: [][]byte{publicada, preparado(2, "rotacapixaba:res-1", "vaijunto:car-x", 0, 5)},
			quer:      "fora da rota",
		},
		{
			nome: "preparado repetido para a mesma transação",
			registros: [][]byte{publicada,
				preparado(2, "rotacapixaba:res-1", "vaijunto:car-x", 0, 1),
				preparado(3, "rotacapixaba:res-1", "vaijunto:car-x", 0, 1)},
			quer: "já tem participação",
		},
		{
			nome: "preparado sem assento para descontar",
			registros: [][]byte{publicada,
				preparado(2, "rotacapixaba:res-1", "vaijunto:car-x", 0, 1),
				preparado(3, "rotacapixaba:res-2", "vaijunto:car-x", 0, 1)},
			quer: "sem assento para descontar",
		},
		{
			nome:      "preparado sem itens",
			registros: [][]byte{envelope(1, "PREPARADO", `{"tx":"rotacapixaba:res-1","itens":[]}`)},
			quer:      "sem itens",
		},
		{
			nome:      "confirmado de transação desconhecida",
			registros: [][]byte{soTx(1, "CONFIRMADO", "rotacapixaba:res-1")},
			quer:      "não está preparada",
		},
		{
			nome: "confirmado repetido",
			registros: [][]byte{publicada,
				preparado(2, "rotacapixaba:res-1", "vaijunto:car-x", 0, 1),
				soTx(3, "CONFIRMADO", "rotacapixaba:res-1"),
				soTx(4, "CONFIRMADO", "rotacapixaba:res-1")},
			quer: "não está preparada",
		},
		{
			nome: "abortado de transação confirmada",
			registros: [][]byte{publicada,
				preparado(2, "rotacapixaba:res-1", "vaijunto:car-x", 0, 1),
				soTx(3, "CONFIRMADO", "rotacapixaba:res-1"),
				soTx(4, "ABORTADO", "rotacapixaba:res-1")},
			quer: "está em CONFIRMADO",
		},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			e := estadoDeTeste()
			_, err := e.Reaplicar(caso.registros)
			if err == nil {
				t.Fatalf("Reaplicar aceitou log incoerente")
			}
			if !strings.Contains(err.Error(), caso.quer) {
				t.Errorf("err = %q, want conter %q", err, caso.quer)
			}
		})
	}
}

// --- Wait-die: a tabela da seção 8.2 linha a linha ---
//
// Os testes do coordenador e da disputa entre empresas estão em testes/; aqui
// o alvo é a decisão em si, com o estado montado à mão. É onde cada linha da
// tabela aparece isolada, sem rede e sem coordenador.
//
// car-2 tem dois assentos no seu único trecho, o que permite montar um trecho
// esgotado com **dois** portadores de carimbos diferentes — o caso em que a
// exigência de ser mais velha que *todos* decide.

// carimboDe é o atalho para um carimbo da rotacapixaba, que é a coordenadora
// dos pedidos destes testes.
func carimboDe(lamport uint64) Carimbo {
	return Carimbo{Lamport: lamport, Empresa: "rotacapixaba"}
}

// esgotarCar2 põe os dois assentos de car-2 em espera, com os carimbos dados,
// e devolve o estado.
func esgotarCar2(t *testing.T, lamports ...uint64) *Estado {
	t.Helper()
	e := estadoDoCenario(t)
	for i, l := range lamports {
		tx := fmt.Sprintf("rotacapixaba:res-portador%d", i)
		if _, err := e.Preparar(pedidoComCarimbo(tx, carimboDe(l), item("vaijunto:car-2", 0, 1)), aoPreparar()); err != nil {
			t.Fatalf("pôr %s em espera: %v", tx, err)
		}
	}
	if o := ocupacaoDe(t, e, "vaijunto:car-2", 0); o.Livres != 0 {
		t.Fatalf("car-2 ficou com %d livres, want 0", o.Livres)
	}
	return e
}

// Segunda linha da tabela: mais velha que **todos** os portadores → AGUARDE,
// com o trecho informado (seção 4.3 da API) e sem gravar nada.
func TestWaitDie_MaisVelhaQueTodosEspera(t *testing.T) {
	e := esgotarCar2(t, 20, 30)
	antes := len(tiposNoLog(t, e))

	_, err := e.Preparar(pedidoComCarimbo(txDeTeste, carimboDe(10), item("vaijunto:car-2", 0, 1)), aoPreparar())

	var detalhe *ErroAguarde
	if !errors.As(err, &detalhe) {
		t.Fatalf("err = %v, want *ErroAguarde", err)
	}
	if detalhe.CaronaID != "vaijunto:car-2" || detalhe.IndiceTrecho != 0 {
		t.Errorf("detalhe = %+v, want car-2 trecho 0", detalhe)
	}
	if depois := len(tiposNoLog(t, e)); depois != antes {
		t.Errorf("o AGUARDE gravou %d registro(s); ele não grava nada (seção 8.2)", depois-antes)
	}
	if _, existe := e.Participacao(txDeTeste); existe {
		t.Errorf("o AGUARDE criou participação")
	}
	conferirIP21(t, e)
}

// Terceira linha: basta **um** portador mais velho para a transação desistir,
// ainda que ela seja mais velha que o outro.
//
// É o que a seção 8.2 quer dizer com "mais velha que todos": exigir menos
// permitiria que T esperasse por alguém mais velho, e um ciclo de espera
// voltaria a ser possível (seção 8.5).
func TestWaitDie_UmPortadorMaisVelhoBastaParaDesistir(t *testing.T) {
	e := esgotarCar2(t, 10, 30)
	antes := len(tiposNoLog(t, e))

	// 20 é mais velha que o portador de 30, e mais nova que o de 10.
	_, err := e.Preparar(pedidoComCarimbo(txDeTeste, carimboDe(20), item("vaijunto:car-2", 0, 1)), aoPreparar())

	var detalhe *ErroPrecedencia
	if !errors.As(err, &detalhe) {
		t.Fatalf("err = %v, want *ErroPrecedencia", err)
	}
	if detalhe.CaronaID != "vaijunto:car-2" || detalhe.IndiceTrecho != 0 {
		t.Errorf("detalhe = %+v, want car-2 trecho 0", detalhe)
	}
	if depois := len(tiposNoLog(t, e)); depois != antes {
		t.Errorf("a PRECEDENCIA gravou %d registro(s)", depois-antes)
	}
	conferirIP21(t, e)
}

// Carimbo **igual** ao do portador conta como espera.
//
// O único jeito de isso acontecer é a D36: a nova tentativa de uma reserva
// reusa o carimbo com txID novo, e o ABORTAR da tentativa anterior vai em
// segundo plano, então ela pode encontrar a si mesma ainda em PREPARADO. Se o
// empate desse PRECEDENCIA, a reserva desistiria de si mesma e queimaria as
// três tentativas; esperando, ela leva o assento assim que o abort chega.
//
// Para duas transações diferentes o empate é impossível: (lamport, empresa) é
// ordem total.
func TestWaitDie_CarimboIgualAoPortadorEspera(t *testing.T) {
	e := esgotarCar2(t, 20, 20)

	_, err := e.Preparar(pedidoComCarimbo(txDeTeste, carimboDe(20), item("vaijunto:car-2", 0, 1)), aoPreparar())
	if !errors.Is(err, ErrAguarde) {
		t.Fatalf("err = %v, want ErrAguarde", err)
	}
	conferirIP21(t, e)
}

// Primeira linha ganha das outras duas: um PREPARAR com dois trechos
// esgotados, um sem portadores e outro com portador mais novo, é SEM_ASSENTO.
//
// A ordem das regras importa. Esperar faria o coordenador gastar o prazo de
// votação por um assento que não vai vagar, porque o trecho sem portadores
// está cheio de assentos **vendidos**, e venda não volta (regra 4).
func TestWaitDie_TrechoVendidoGanhaDoTrechoEmDisputa(t *testing.T) {
	e := estadoDoCenario(t)

	// car-4 tem um assento, e ele é vendido: trecho esgotado sem portadores.
	esgotarComConfirmado(t, e, "rotacapixaba:res-vendida", item("vaijunto:car-4", 0, 1))
	// car-2 tem dois, e os dois ficam em espera com transações mais novas:
	// sozinho, este trecho daria AGUARDE.
	for i, l := range []uint64{90, 91} {
		tx := fmt.Sprintf("rotacapixaba:res-portador%d", i)
		if _, err := e.Preparar(pedidoComCarimbo(tx, carimboDe(l), item("vaijunto:car-2", 0, 1)), aoPreparar()); err != nil {
			t.Fatalf("pôr %s em espera: %v", tx, err)
		}
	}

	_, err := e.Preparar(pedidoComCarimbo(txDeTeste, carimboDe(1),
		item("vaijunto:car-2", 0, 1), item("vaijunto:car-4", 0, 1)), aoPreparar())

	var detalhe *ErroSemAssento
	if !errors.As(err, &detalhe) {
		t.Fatalf("err = %v, want *ErroSemAssento (o trecho vendido decide)", err)
	}
	if detalhe.CaronaID != "vaijunto:car-4" {
		t.Errorf("detalhe = %+v, want o trecho vendido de car-4", detalhe)
	}
	conferirIP21(t, e)
}

// Um trecho com assento livre não entra na disputa: o PREPARAR de um
// itinerário em que só um dos trechos está esgotado é decidido por esse
// trecho, e o outro não vira nem espera nem recusa.
func TestWaitDie_SoOsTrechosEsgotadosEntramNaDecisao(t *testing.T) {
	e := estadoDoCenario(t)
	if _, err := e.Preparar(pedidoComCarimbo("rotacapixaba:res-portador0", carimboDe(90),
		item("vaijunto:car-2", 0, 1)), aoPreparar()); err != nil {
		t.Fatalf("pôr o portador em espera: %v", err)
	}

	// car-2 tem dois assentos e só um portador: ainda há um livre, então
	// nenhum trecho está esgotado e o voto é SIM.
	if _, err := e.Preparar(pedidoComCarimbo(txDeTeste, carimboDe(1),
		item("vaijunto:car-1", 0, 1), item("vaijunto:car-2", 0, 1)), aoPreparar()); err != nil {
		t.Fatalf("Preparar com assento livre: %v", err)
	}

	// Agora car-2 esgotou, e o trecho livre de car-1 não muda a decisão.
	_, err := e.Preparar(pedidoComCarimbo("rotacapixaba:res-00000009", carimboDe(99),
		item("vaijunto:car-1", 0, 1), item("vaijunto:car-2", 0, 1)), aoPreparar())
	if !errors.Is(err, ErrPrecedencia) {
		t.Fatalf("err = %v, want ErrPrecedencia", err)
	}
	conferirIP21(t, e)
}
