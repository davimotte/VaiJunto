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
	return PedidoPreparacao{
		TxID:          tx,
		Coordenador:   "rotacapixaba",
		Participantes: []string{"rotacapixaba", "vaijunto"},
		Carimbo:       Carimbo{Lamport: 41, Empresa: "rotacapixaba"},
		Passageiro:    Passageiro{Ref: "rotacapixaba:maria", Nome: "Maria Souza"},
		Itens:         itens,
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

// Passo 3 com disputa **no-wait** (fase 4): trecho esgotado é NÃO na hora, com
// o trecho identificado para o diagnóstico e o registro de operações. O
// wait-die, que responderia AGUARDE para a transação mais velha, é da fase 7.
func TestPreparar_SemAssentoNaoGravaNada(t *testing.T) {
	e := estadoDoCenario(t)

	// car-4 tem um assento só: o primeiro PREPARAR o leva.
	prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-4", 0, 1))
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
	prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-4", 0, 1))

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

// --- Portadores e ocupação (seções 4.3 e 11.1) ---

// Quem está em espera num trecho é derivado das participações em PREPARADO, e
// não guardado em paralelo na carona (seção 4.3). Como os assentos são
// contadores (D25), um trecho pode ter vários portadores ao mesmo tempo — é
// disso que o wait-die da fase 7 precisa.
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
