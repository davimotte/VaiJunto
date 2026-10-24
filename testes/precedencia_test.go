package testes

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/federacao"
	"vaijunto/internal/transacao"
)

// Cenários da precedência por wait-die (PROJETO_P2.md, seção 8; TR3 e TR4 da
// seção 11.3).
//
// Os dois usam a **rede em memória** (seção 5.4) em vez do cliente HTTP, e é
// isso que os torna provas e não observações: com o gancho de Interceptar, a
// ordem em que cada PREPARAR chega a cada dono é decidida pelo teste. A
// disputa cruzada da seção 8.3 depende justamente de uma ordem que não se pode
// pedir ao escalonador — T₁ chegando primeiro a um dono e T₂ primeiro ao
// outro —, e a demonstração ao vivo (D-3) não a garante.
//
// Aqui os coordenadores são chamados direto, sem passar pela porta TCP: o que
// se afirma é a coordenação, e um LOGIN no meio só acrescentaria ruído. A
// tradução de DISPUTA_PERDIDA para o código do PROTOCOL.md é afirmada em
// internal/servidor.

// prazoVotacaoNaDisputa é largo o bastante para a disputa inteira se resolver
// — e é exatamente por isso que serve de asserção: se o wait-die travasse, o
// teste levaria este prazo todo e falharia por tempo, não por valor.
const prazoVotacaoNaDisputa = 3 * time.Second

// prazoAguardeNaDisputa encurta a repetição da D41 para que o teste não passe
// o tempo dormindo entre tentativas.
const prazoAguardeNaDisputa = 5 * time.Millisecond

// esperarEmEspera bloqueia até o trecho 0 da carona única de uma empresa ter o
// número de assentos em espera pedido.
//
// É espera **observada**, e não um sleep: o teste precisa saber que um
// PREPARAR já foi aplicado sob o lock do dono antes de soltar o próximo, e é
// essa observação que torna a ordem de chegada determinística em vez de
// provável.
func esperarEmEspera(t *testing.T, e *empresa, quantos int) {
	t.Helper()
	limite := time.Now().Add(prazoDeQuiescencia)
	for time.Now().Before(limite) {
		if o := e.estado.Ocupacao()[0]; o.EmEspera == quantos {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s: o trecho não chegou a %d assento(s) em espera: %+v", e.nome, quantos, e.estado.Ocupacao()[0])
}

// reservarPeloCoordenador dispara um RESERVAR direto no coordenador de uma
// empresa e devolve um canal com o erro (nil quando confirmou).
func reservarPeloCoordenador(e *empresa, usuario string, itens ...dominio.ItemReserva) <-chan error {
	pronto := make(chan error, 1)
	go func() {
		passageiro := dominio.Passageiro{Ref: e.nome + ":" + usuario, Nome: usuario}
		_, _, err := e.coordenador.Reservar(context.Background(), passageiro, usuario, itens)
		pronto <- err
	}()
	return pronto
}

// TestTR3DisputaCruzadaAMaisVelhaConfirma é o TR3 da seção 11.3: a disputa
// cruzada da seção 8.3, com a rede em memória forçando a ordem do instante 1.
//
// B é a rotacapixaba e C é a rotafluminense, cada uma com um assento. T₁ e T₂
// pedem as duas pernas, e a ordem de chegada é **invertida** entre os dois
// donos: T₁ chega primeiro a B, T₂ chega primeiro a C. É a situação em que a
// ordem de chegada — a camada 1 — não resolve nada, porque ela diz coisas
// opostas em cada servidor.
//
//	| Instante | B (rotacapixaba)              | C (rotafluminense)          |
//	|----------|-------------------------------|-----------------------------|
//	| 1        | T₁ → SIM                      | T₂ → SIM                    |
//	| 2        | T₂, mais nova → PRECEDENCIA   | T₁, mais velha → AGUARDE    |
//	| 3        | —                             | T₂ aborta; o assento volta  |
//	| 4        | —                             | T₁ repetido → SIM; confirma |
//
// As duas são coordenadas pela **mesma** empresa, a vaijunto, que não é dona
// de nenhuma das duas pernas. Dois motivos:
//
//   - o que o cenário precisa cruzar é a ordem de chegada aos **donos**, e
//     quem coordena não entra na conta do wait-die. Se uma das duas fosse
//     coordenada por B ou por C, o PREPARAR dela para si mesma seria chamada
//     direta, sem passar pela rede (seção 7.1, passo 5), e o teste não teria
//     como ordená-lo;
//   - no mesmo coordenador, os dois carimbos saem do mesmo relógio, e T₁ é a
//     mais velha porque **nasceu antes** (AoCriarTransacao incrementa L). A
//     precedência é consequência do algoritmo, e não um valor que o teste
//     escolheu.
//
// Os gates contam as entregas por destino, e a contagem é inequívoca porque o
// teste sequencia as largadas: T₁ sai sozinha, então a 1ª entrega a cada dono
// é dela; T₂ só sai depois, e as suas são as 2ªs. As repetições posteriores
// passam livres.
func TestTR3DisputaCruzadaAMaisVelhaConfirma(t *testing.T) {
	hub := federacao.NovaRedeEmMemoria()

	var mu sync.Mutex
	entregas := map[string]int{}
	seguraT1EmC := make(chan struct{})
	seguraT2EmB := make(chan struct{})

	hub.Interceptar(func(c federacao.Chamada) {
		if c.Operacao != federacao.OperacaoPreparar {
			return
		}
		mu.Lock()
		entregas[c.Destino]++
		n := entregas[c.Destino]
		mu.Unlock()

		switch {
		case c.Destino == "rotafluminense" && n == 1:
			// A 1ª em C é a de T₁, e é a que fica presa: sem isto T₁ chegaria
			// primeiro aos dois donos e a disputa deixaria de ser cruzada.
			<-seguraT1EmC
		case c.Destino == "rotacapixaba" && n == 2:
			// A 2ª em B é a de T₂, e ela só pode chegar depois de T₂ já estar
			// com o assento de C — é o instante 2 da tabela.
			<-seguraT2EmB
		}
	})

	consorcio := subirConsorcioP2(t, opcoesConsorcio{
		caronas: caronasEncadeadas(map[string]int{
			"vaijunto":       1,
			"rotacapixaba":   1,
			"rotafluminense": 1,
		}),
		auditoria:     true,
		redeEmMemoria: hub,
		prazoVotacao:  prazoVotacaoNaDisputa,
		prazoAguarde:  prazoAguardeNaDisputa,
		prazoFase2:    prazoAguardeNaDisputa,
	})

	pernas := []dominio.ItemReserva{
		{CaronaID: "rotacapixaba:car-1", De: 0, Ate: 1},
		{CaronaID: "rotafluminense:car-1", De: 0, Ate: 1},
	}
	coordenadora := consorcio["vaijunto"]

	// Instante 1, metade de B: T₁ sai sozinha e leva o assento de B. O seu
	// PREPARAR para C fica preso no gancho.
	inicio := time.Now()
	t1 := reservarPeloCoordenador(coordenadora, "maria", pernas...)
	esperarEmEspera(t, consorcio["rotacapixaba"], 1)

	// Instante 1, metade de C: T₂ sai agora, nasce mais nova, e chega primeiro
	// a C. O seu PREPARAR para B fica preso.
	t2 := reservarPeloCoordenador(coordenadora, "pedro", pernas...)
	esperarEmEspera(t, consorcio["rotafluminense"], 1)

	// Instante 2 nos dois donos ao mesmo tempo: cada uma encontra a outra.
	// Em B, T₂ é a mais nova e desiste; em C, T₁ é a mais velha e espera. É a
	// espera de T₁ que os instantes 3 e 4 transformam em assento.
	close(seguraT1EmC)
	close(seguraT2EmB)

	if err := <-t1; err != nil {
		t.Fatalf("T₁, a mais velha, não confirmou: %v", err)
	}
	decorrido := time.Since(inicio)

	// Sem deadlock e sem esperar o fim do prazo: se as duas esperassem uma
	// pela outra, ou se T₁ ficasse pendurada até o prazo de votação acabar, o
	// tempo bateria em prazoVotacaoNaDisputa. A margem é folgada de propósito
	// — o que se afirma é a ordem de grandeza, não uma temporização.
	if decorrido > prazoVotacaoNaDisputa/2 {
		t.Errorf("T₁ levou %s, quase o prazo de votação (%s): a disputa não se resolveu pelo wait-die",
			decorrido, prazoVotacaoNaDisputa)
	}

	// T₂ perdeu a disputa nas três tentativas da D36. Não é SEM_ASSENTO: havia
	// assento, e outra transação levou.
	if err := <-t2; !errors.Is(err, dominio.ErrDisputaPerdida) {
		t.Errorf("T₂ = %v, want ErrDisputaPerdida", err)
	}

	esperarQuiescencia(t, consorcio)
	todas := auditoriasDe(t, consorcio)
	verificarInvariantesP2(t, todas)

	// Os dois assentos em disputa são do vencedor, e nenhum ficou em espera.
	for _, nome := range []string{"rotacapixaba", "rotafluminense"} {
		carona := todas[nome].Caronas[0]
		if carona.Livres[0] != 0 || carona.Confirmados[0] != 1 || carona.EmEspera[0] != 0 {
			t.Errorf("%s: %d livres, %d confirmados, %d em espera, want 0, 1, 0",
				nome, carona.Livres[0], carona.Confirmados[0], carona.EmEspera[0])
		}
	}
}

// TestTR4NovaTentativaUsaOCarimboOriginalEEspera é o TR4 da seção 11.3: a
// transação que desiste pelo wait-die tenta de novo com o carimbo original e,
// na nova tentativa, **espera** em vez de desistir.
//
// É o argumento da ausência de inanição (seção 8.5) observado em uma rodada:
// o carimbo não muda entre as tentativas (D36), então a transação fica
// relativamente mais velha a cada disputa — e passar de PRECEDENCIA a AGUARDE
// é exatamente o que "mais velha" significa na tabela da seção 8.2.
//
// O cenário é montado pelo gancho, que vê cada PREPARAR antes de ele ser
// entregue e troca o portador do assento entre uma tentativa e outra:
//
//  1. antes da 1ª tentativa, o assento está com uma transação **mais velha**
//     que T → T desiste com PRECEDENCIA e o coordenador refaz a reserva;
//  2. antes da 2ª tentativa, o portador vira uma transação **mais nova** que T
//     → T espera;
//  3. a repetição da 2ª tentativa, 5 ms depois, é a prova do AGUARDE: só um
//     AGUARDE faz o coordenador reenviar o **mesmo** txID. Aí o portador
//     aborta e T leva o assento.
func TestTR4NovaTentativaUsaOCarimboOriginalEEspera(t *testing.T) {
	hub := federacao.NovaRedeEmMemoria()

	// O que o gancho registra de cada entrega a vaijunto, para as asserções:
	// um txID por tentativa, e o carimbo que veio com ele.
	var mu sync.Mutex
	var ordem []string                       // txIDs na ordem da 1ª entrega
	entregas := map[string]int{}             // entregas por txID
	carimbos := map[string]dominio.Carimbo{} // carimbo de cada txID
	var portador string                      // quem está com o assento agora

	consorcio := subirConsorcioP2(t, opcoesConsorcio{
		caronas: caronasEncadeadas(map[string]int{
			"vaijunto":       1,
			"rotacapixaba":   1,
			"rotafluminense": 1,
		}),
		auditoria:     true,
		redeEmMemoria: hub,
		prazoVotacao:  prazoVotacaoNaDisputa,
		prazoAguarde:  prazoAguardeNaDisputa,
		prazoFase2:    prazoAguardeNaDisputa,
	})

	dono := consorcio["vaijunto"]

	// trocarPortador aborta quem está com o assento e, se vier um carimbo,
	// põe outra transação em espera no lugar. Roda no gancho, isto é, **antes**
	// de a mensagem interceptada ser entregue, e por isso o PREPARAR que vem a
	// seguir já encontra o estado novo.
	trocarPortador := func(t *testing.T, novo string, carimbo dominio.Carimbo) {
		t.Helper()
		if portador != "" {
			if _, err := dono.participante.Abortar(portador); err != nil {
				t.Errorf("abortar o portador %s: %v", portador, err)
			}
			portador = ""
		}
		if novo == "" {
			return
		}
		pedido := pedidoComCarimbo("rotafluminense", novo, "vaijunto:car-1", carimbo)
		if _, err := dono.participante.Preparar(pedido); err != nil {
			t.Errorf("pôr %s em espera: %v", novo, err)
			return
		}
		portador = novo
	}

	// O assento começa com uma transação mais velha que T, que terá o carimbo
	// 50 (ver o Restaurar abaixo).
	trocarPortador(t, "rotafluminense:res-velha", dominio.Carimbo{Lamport: 1, Empresa: "rotafluminense"})

	hub.Interceptar(func(c federacao.Chamada) {
		if c.Operacao != federacao.OperacaoPreparar || c.Destino != "vaijunto" || c.Origem != "rotacapixaba" {
			return
		}
		mu.Lock()
		defer mu.Unlock()

		entregas[c.TxID]++
		if entregas[c.TxID] == 1 {
			ordem = append(ordem, c.TxID)
			carimbos[c.TxID] = c.Carimbo
		}

		switch {
		case len(ordem) == 2 && entregas[c.TxID] == 1:
			// 2ª tentativa chegando: o portador passa a ser mais novo que T,
			// que por isso vai esperar em vez de desistir.
			trocarPortador(t, "rotafluminense:res-nova", dominio.Carimbo{Lamport: 99, Empresa: "rotafluminense"})
		case len(ordem) == 2 && entregas[c.TxID] == 2:
			// A 2ª tentativa está sendo reenviada com o mesmo txID: só o
			// AGUARDE produz isso. O portador sai e T leva o assento.
			trocarPortador(t, "", dominio.Carimbo{})
		}
	})

	// T nasce entre os dois portadores: mais nova que a de carimbo 1, mais
	// velha que a de carimbo 99.
	consorcio["rotacapixaba"].relogio.Restaurar(49)

	err := <-reservarPeloCoordenador(consorcio["rotacapixaba"], "pedro",
		dominio.ItemReserva{CaronaID: "vaijunto:car-1", De: 0, Ate: 1})
	if err != nil {
		t.Fatalf("T não confirmou: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Duas tentativas, com txID próprio cada uma (D36).
	if len(ordem) != 2 {
		t.Fatalf("tentativas = %d (%v), want 2", len(ordem), ordem)
	}
	if ordem[0] == ordem[1] {
		t.Errorf("as duas tentativas usaram o mesmo txID %q, e a D36 pede uma transação por tentativa", ordem[0])
	}
	// E o mesmo carimbo nas duas: é isso que impede a inanição (seção 8.5).
	if carimbos[ordem[0]] != carimbos[ordem[1]] {
		t.Errorf("carimbos = %+v e %+v, want iguais: a nova tentativa tem de manter o carimbo original (D36)",
			carimbos[ordem[0]], carimbos[ordem[1]])
	}

	// A 1ª tentativa foi entregue uma vez só — desistiu com PRECEDENCIA. A 2ª
	// foi entregue duas vezes — esperou com AGUARDE e o coordenador voltou.
	if entregas[ordem[0]] != 1 {
		t.Errorf("a 1ª tentativa foi entregue %d vezes, want 1: ela devia ter desistido na hora", entregas[ordem[0]])
	}
	if entregas[ordem[1]] != 2 {
		t.Errorf("a 2ª tentativa foi entregue %d vezes, want 2: ela devia ter esperado e sido repetida", entregas[ordem[1]])
	}

	esperarQuiescencia(t, consorcio)
	todas := auditoriasDe(t, consorcio)
	verificarInvariantesP2(t, todas)

	carona := todas["vaijunto"].Caronas[0]
	if carona.Livres[0] != 0 || carona.Confirmados[0] != 1 || carona.EmEspera[0] != 0 {
		t.Errorf("vaijunto: %d livres, %d confirmados, %d em espera, want 0, 1, 0",
			carona.Livres[0], carona.Confirmados[0], carona.EmEspera[0])
	}
}

// Um trecho esgotado **só por assentos confirmados** é SEM_ASSENTO imediato,
// sem passar pelo wait-die: não há portador nenhum, e portanto não há o que
// esperar (primeira linha da tabela da seção 8.2).
//
// É a linha que separa "o assento está vendido" de "o assento está em
// disputa", e é ela que impede o coordenador de gastar o prazo de votação
// esperando por algo que nunca vai vagar.
func TestTrechoEsgotadoSoPorConfirmadosRecusaNaHora(t *testing.T) {
	hub := federacao.NovaRedeEmMemoria()
	consorcio := subirConsorcioP2(t, opcoesConsorcio{
		caronas: caronasEncadeadas(map[string]int{
			"vaijunto":     1,
			"rotacapixaba": 1,
		}),
		auditoria:     true,
		redeEmMemoria: hub,
		prazoVotacao:  prazoVotacaoNaDisputa,
		prazoAguarde:  prazoAguardeNaDisputa,
		prazoFase2:    prazoAguardeNaDisputa,
	})

	// O único assento de vaijunto:car-1 é vendido: preparado e confirmado.
	dono := consorcio["vaijunto"].participante
	vendida := pedidoComCarimbo("rotacapixaba", "rotacapixaba:res-vendida", "vaijunto:car-1",
		dominio.Carimbo{Lamport: 1, Empresa: "rotacapixaba"})
	if _, err := dono.Preparar(vendida); err != nil {
		t.Fatalf("Preparar da vendida: %v", err)
	}
	if _, err := dono.Confirmar(vendida.TxID); err != nil {
		t.Fatalf("Confirmar da vendida: %v", err)
	}

	// Mesmo sendo a mais velha de todas, esta não espera: não há portador.
	consorcio["rotacapixaba"].relogio.Restaurar(0)
	inicio := time.Now()
	err := <-reservarPeloCoordenador(consorcio["rotacapixaba"], "pedro",
		dominio.ItemReserva{CaronaID: "vaijunto:car-1", De: 0, Ate: 1})

	var detalhe *dominio.ErroSemAssento
	if !errors.As(err, &detalhe) {
		t.Fatalf("err = %v, want *ErroSemAssento", err)
	}
	if detalhe.CaronaID != "vaijunto:car-1" || detalhe.IndiceTrecho != 0 {
		t.Errorf("detalhe = %+v, want vaijunto:car-1 trecho 0", detalhe)
	}
	if decorrido := time.Since(inicio); decorrido > prazoVotacaoNaDisputa/2 {
		t.Errorf("a recusa levou %s: SEM_ASSENTO tem de sair na hora, sem esperar o prazo", decorrido)
	}
}

// A rede em memória entrega ao mesmo participante que o HTTP entregaria, e o
// gancho não muda o que é entregue — só quando.
//
// Existe para que uma quebra do gancho apareça aqui, e não como um TR3
// intermitente: um gancho que engolisse a chamada, ou que a entregasse duas
// vezes, faria o TR3 falhar de um jeito que pareceria um problema do wait-die.
func TestGanchoDaRedeEmMemoriaVeTodasAsChamadas(t *testing.T) {
	hub := federacao.NovaRedeEmMemoria()

	var mu sync.Mutex
	var vistas []federacao.Chamada
	hub.Interceptar(func(c federacao.Chamada) {
		mu.Lock()
		defer mu.Unlock()
		vistas = append(vistas, c)
	})

	consorcio := subirConsorcioP2(t, opcoesConsorcio{
		caronas: caronasEncadeadas(map[string]int{
			"vaijunto":     1,
			"rotacapixaba": 1,
		}),
		auditoria:     true,
		redeEmMemoria: hub,
		prazoVotacao:  prazoVotacaoNaDisputa,
		prazoAguarde:  prazoAguardeNaDisputa,
		prazoFase2:    prazoAguardeNaDisputa,
	})
	consorcio["rotacapixaba"].relogio.Restaurar(40)

	err := <-reservarPeloCoordenador(consorcio["rotacapixaba"], "pedro",
		dominio.ItemReserva{CaronaID: "vaijunto:car-1", De: 0, Ate: 1})
	if err != nil {
		t.Fatalf("Reservar: %v", err)
	}
	esperarQuiescencia(t, consorcio)

	mu.Lock()
	defer mu.Unlock()

	var preparar, confirmar int
	for _, c := range vistas {
		if c.Origem != "rotacapixaba" || c.Destino != "vaijunto" {
			t.Errorf("chamada inesperada: %+v", c)
		}
		switch c.Operacao {
		case federacao.OperacaoPreparar:
			preparar++
			// Só o preparar leva carimbo, e ele é o da transação.
			if c.Carimbo != (dominio.Carimbo{Lamport: 41, Empresa: "rotacapixaba"}) {
				t.Errorf("carimbo = %+v, want (41,rotacapixaba)", c.Carimbo)
			}
		case federacao.OperacaoConfirmar:
			confirmar++
			if c.Carimbo != (dominio.Carimbo{}) {
				t.Errorf("confirmar com carimbo %+v, want vazio", c.Carimbo)
			}
		default:
			t.Errorf("operação inesperada: %+v", c)
		}
	}
	if preparar != 1 || confirmar != 1 {
		t.Errorf("chamadas = %d preparar e %d confirmar, want 1 e 1 (%+v)", preparar, confirmar, vistas)
	}
	// E a entrega aconteceu de verdade: o assento saiu do dono.
	if o := consorcio["vaijunto"].estado.Ocupacao()[0]; o.Livres != 0 || o.Confirmados != 1 {
		t.Errorf("dono: %+v, want 0 livres e 1 confirmado", o)
	}
}

// Sanidade do vocabulário: AGUARDE não é motivo de voto NÃO, e PRECEDENCIA é.
// É o contrato da seção 4.3 da API, de que o coordenador depende para saber se
// volta em 200 ms ou se tenta de novo (D36).
func TestVocabularioDoWaitDie(t *testing.T) {
	if transacao.VotoAguarde == transacao.VotoNao || transacao.VotoAguarde == transacao.VotoSim {
		t.Errorf("AGUARDE tem de ser um voto próprio, e não %q", transacao.VotoAguarde)
	}
	if transacao.MotivoPrecedencia != "PRECEDENCIA" {
		t.Errorf("motivo = %q, want PRECEDENCIA (seção 5.2 da API)", transacao.MotivoPrecedencia)
	}
}
