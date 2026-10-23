package testes

import (
	"context"
	"testing"
	"time"

	"vaijunto/internal/dominio"
)

// Cenários de queda e recuperação (PROJETO_P2.md, seções 9.3, 9.4 e 9.6;
// seção 11.3: TF9 e TF10).
//
// O que estes testes afirmam é a RD04: a falha de um servidor não deixa
// assento bloqueado indefinidamente. Até a fase 5 isso não era verdade — uma
// participação em PREPARADO só saía desse estado por ordem do coordenador, e
// um coordenador que caísse entre o PREPARAR e a decisão deixava o assento
// descontado para sempre, inclusive depois de um reinício, porque o PREPARADO
// está no log e o replay o reaplica fielmente.
//
// Todos sobem as empresas com log em arquivo (dirLogs): o que esta fase
// entrega é o caminho completo do boot da seção 9.2 — semente, replay e as
// tarefas de recuperação da seção 9.3 —, e um log em memória não o exercita.
//
// Os prazos da D41 vêm em milissegundos. Em produção são 10 s e 3 s, calibrados
// para que, num sistema saudável, consulta nenhuma chegue a acontecer; aqui o
// que se quer é justamente o sistema doente.

// prazosDeTerminacaoNosTestes são os prazos da D41 encurtados, mantendo a
// relação entre eles: a dúvida é maior que uma chamada a um par, de modo que o
// caminho normal continue sendo "a decisão chega antes de alguém perguntar".
const (
	duvidaNosTestes   = 150 * time.Millisecond
	consultaNosTestes = 50 * time.Millisecond
	chamadaNosTestes  = 100 * time.Millisecond
)

// opcoesDeTerminacao é a configuração comum destes cenários.
func opcoesDeTerminacao(t *testing.T, caronas map[string][]byte) opcoesConsorcio {
	t.Helper()
	return opcoesConsorcio{
		caronas:       caronas,
		dirLogs:       t.TempDir(),
		auditoria:     true,
		prazoChamada:  chamadaNosTestes,
		prazoDuvida:   duvidaNosTestes,
		prazoConsulta: consultaNosTestes,
	}
}

// prepararEntre faz um participante votar SIM numa transação coordenada por
// outra empresa, com a lista de participantes que a terminação vai usar (D38).
//
// Chama pela rede do coordenador, e não pelo participante direto, porque é
// assim que o PREPARAR chega de verdade: HTTP, middleware e verificação de
// papel incluídos.
func prepararEntre(t *testing.T, consorcio map[string]*empresa, coordenador, dono, txID string, participantes []string) {
	t.Helper()
	resposta, err := consorcio[coordenador].rede.Preparar(context.Background(), dono, dominio.PedidoPreparacao{
		TxID:          txID,
		Coordenador:   coordenador,
		Participantes: participantes,
		Carimbo:       dominio.Carimbo{Lamport: 41, Empresa: coordenador},
		Passageiro:    dominio.Passageiro{Ref: coordenador + ":maria", Nome: "Maria Souza"},
		Itens:         []dominio.ItemReserva{{CaronaID: dono + ":car-1", De: 0, Ate: 1}},
	})
	if err != nil {
		t.Fatalf("PREPARAR de %s para %s: %v", coordenador, dono, err)
	}
	if resposta.Voto != "SIM" {
		t.Fatalf("PREPARAR de %s para %s: voto = %+v, want SIM", coordenador, dono, resposta)
	}
}

// esperarSituacao espera até que a participação chegue à situação pedida.
//
// É espera observada, e não um sleep fixo, pelo mesmo motivo de
// esperarQuiescencia: a terminação depende de um tique e de chamadas de rede,
// e um sleep calibrado seria ou lento ou instável sob -count=20.
func esperarSituacao(t *testing.T, e *empresa, txID string, querida dominio.Situacao) {
	t.Helper()
	limite := time.Now().Add(prazoDeQuiescencia)
	for time.Now().Before(limite) {
		pt, existe := e.estado.Participacao(txID)
		if existe && pt.Situacao == querida {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	pt, existe := e.estado.Participacao(txID)
	t.Fatalf("%s: participação %s = %+v (existe=%t), want %s em %s",
		e.nome, txID, pt, existe, querida, prazoDeQuiescencia)
}

// conferirSituacaoEstavel afirma que a situação **continua** a mesma durante
// uma janela, e não só num instante.
//
// É o oposto de esperarSituacao, e é o que o TF10 precisa: afirmar que nada
// aconteceu é afirmar sobre um intervalo, e observar um instante só não
// distinguiria "ficou em dúvida" de "ainda não deu tempo".
func conferirSituacaoEstavel(t *testing.T, e *empresa, txID string, querida dominio.Situacao, janela time.Duration) {
	t.Helper()
	limite := time.Now().Add(janela)
	for time.Now().Before(limite) {
		pt, existe := e.estado.Participacao(txID)
		if !existe || pt.Situacao != querida {
			t.Fatalf("%s: participação %s saiu de %s para %+v (existe=%t) antes da hora",
				e.nome, txID, querida, pt, existe)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// conferirAssentosLivres confere os livres do trecho 0 da carona da empresa.
func conferirAssentosLivres(t *testing.T, e *empresa, querLivres int) {
	t.Helper()
	ocupacao := e.estado.Ocupacao()
	if len(ocupacao) != 1 {
		t.Fatalf("%s: %d trechos, want 1", e.nome, len(ocupacao))
	}
	if ocupacao[0].Livres != querLivres {
		t.Errorf("%s: livres = %d, want %d (ocupação: %+v)", e.nome, ocupacao[0].Livres, querLivres, ocupacao[0])
	}
}

// TF9 — coordenador fora e um participante que nunca recebeu o PREPARAR
// (seção 11.3; diagrama da seção 6.3 da API).
//
// É o cenário que mostra o veto (D38) fazendo o seu trabalho. A rotacapixaba
// votou SIM e tem um assento em espera; a vaijunto, que coordena, cai antes de
// decidir; a rotafluminense nunca soube da transação. Passado o prazo de
// dúvida, a rotacapixaba pergunta à vaijunto e não obtém resposta, pergunta à
// rotafluminense, que **grava o veto e responde DESCONHECIDA**, e com isso
// aprende que a unanimidade é impossível: o coordenador nunca mais conseguirá
// todos os votos SIM, porque um PREPARAR atrasado que chegue à rotafluminense
// agora será recusado.
//
// Sem o veto, abortar aqui seria um palpite: nada impediria o coordenador de
// voltar, obter o SIM que faltava e confirmar uma transação que a rotacapixaba
// já tinha desfeito.
func TestTF9_ParticipanteEmDuvidaAbortaPeloVeto(t *testing.T) {
	const tx = "vaijunto:res-00000001"
	op := opcoesDeTerminacao(t, map[string][]byte{
		"vaijunto":       caronasDe("vaijunto", 3),
		"rotacapixaba":   caronasDe("rotacapixaba", 3),
		"rotafluminense": caronasDe("rotafluminense", 3),
	})
	consorcio := subirConsorcioP2(t, op)
	participantes := []string{"rotacapixaba", "rotafluminense"}

	// Só a rotacapixaba vota SIM: o PREPARAR da rotafluminense se perdeu.
	prepararEntre(t, consorcio, "vaijunto", "rotacapixaba", tx, participantes)
	conferirAssentosLivres(t, consorcio["rotacapixaba"], 2)

	// A vaijunto cai antes de decidir. A transação some com a memória dela, que
	// é exatamente o que o abort presumido assume (D23).
	derrubarEmpresa(t, consorcio["vaijunto"])

	esperarSituacao(t, consorcio["rotacapixaba"], tx, dominio.SituacaoAbortado)

	// O assento voltou: é a RD04 observada no contador.
	conferirAssentosLivres(t, consorcio["rotacapixaba"], 3)

	// E a rotafluminense ficou com o veto: participação ABORTADO sem itens, que
	// é o que recusa um PREPARAR atrasado (seção 4.4).
	veto, existe := consorcio["rotafluminense"].estado.Participacao(tx)
	if !existe || veto.Situacao != dominio.SituacaoAbortado || len(veto.Itens) != 0 {
		t.Fatalf("veto na rotafluminense = %+v (existe=%t), want ABORTADO sem itens", veto, existe)
	}
	conferirAssentosLivres(t, consorcio["rotafluminense"], 3)

	// O PREPARAR atrasado chega e é recusado, fechando o argumento: a
	// unanimidade era mesmo impossível quando a rotacapixaba abortou.
	resposta, err := consorcio["vaijunto"].rede.Preparar(context.Background(), "rotafluminense", dominio.PedidoPreparacao{
		TxID:          tx,
		Coordenador:   "vaijunto",
		Participantes: participantes,
		Carimbo:       dominio.Carimbo{Lamport: 41, Empresa: "vaijunto"},
		Passageiro:    dominio.Passageiro{Ref: "vaijunto:maria", Nome: "Maria Souza"},
		Itens:         []dominio.ItemReserva{{CaronaID: "rotafluminense:car-1", De: 0, Ate: 1}},
	})
	if err != nil {
		t.Fatalf("PREPARAR atrasado: %v", err)
	}
	if resposta.Voto != "NAO" || resposta.Motivo != "TRANSACAO_ABORTADA" {
		t.Errorf("PREPARAR atrasado = %+v, want NAO/TRANSACAO_ABORTADA", resposta)
	}
}

// TF10 — coordenador fora depois de todos os votos SIM (seção 11.3), que é o
// bloqueio residual do 2PC da seção 9.6.
//
// Aqui o veto não tem a quem aparecer: as duas empresas votaram SIM, e cada
// uma só encontra a outra em PREPARADO. Nenhuma pode decidir, e isso **não é
// um defeito**: ninguém sabe se a vaijunto gravou a decisão antes de cair, e
// qualquer escolha unilateral poderia contradizê-la. É o limite conhecido do
// 2PC, e o teste afirma as duas metades dele — que o bloqueio existe, e que
// ele termina quando o coordenador volta, que é o que a RD04 exige.
func TestTF10_BloqueioResidualTerminaQuandoOCoordenadorVolta(t *testing.T) {
	const tx = "vaijunto:res-00000001"
	op := opcoesDeTerminacao(t, map[string][]byte{
		"vaijunto":       caronasDe("vaijunto", 3),
		"rotacapixaba":   caronasDe("rotacapixaba", 3),
		"rotafluminense": caronasDe("rotafluminense", 3),
	})
	consorcio := subirConsorcioP2(t, op)
	participantes := []string{"rotacapixaba", "rotafluminense"}

	for _, dono := range participantes {
		prepararEntre(t, consorcio, "vaijunto", dono, tx, participantes)
		conferirAssentosLivres(t, consorcio[dono], 2)
	}

	// A vaijunto cai com os dois votos SIM na mão e sem ter gravado decisão.
	derrubarEmpresa(t, consorcio["vaijunto"])

	// As duas continuam em PREPARADO, e continuam por várias rodadas de
	// consulta: cada uma pergunta à vaijunto e não é atendida, pergunta à outra
	// e ouve PREPARADO. Nenhuma evidência da seção 7.4 apareceu, então nenhuma
	// decide — e é isso que torna o 2PC correto, não o que o torna falho.
	janela := 6 * consultaNosTestes
	for _, dono := range participantes {
		conferirSituacaoEstavel(t, consorcio[dono], tx, dominio.SituacaoPreparado, janela)
		conferirAssentosLivres(t, consorcio[dono], 2)
	}

	// A vaijunto volta, pelo mesmo log. O replay não encontra
	// DECISAO_CONFIRMAR, a transação não está na memória — ela nunca
	// sobreviveria a um reinício (seção 9.3) — e o abort presumido responde
	// ABORTADA (D23).
	religarEmpresa(t, consorcio, "vaijunto", op)

	for _, dono := range participantes {
		esperarSituacao(t, consorcio[dono], tx, dominio.SituacaoAbortado)
		conferirAssentosLivres(t, consorcio[dono], 3)
	}

	// I-P2-3 depois da quiescência, que é o ponto do cenário: nenhum assento
	// ficou preso após a queda e a recuperação.
	esperarQuiescencia(t, consorcio)
	verificarInvariantesP2(t, auditoriasDe(t, consorcio))
}

// O coordenador reiniciado responde ABORTADA para uma transação sem decisão
// (linha 3 da tabela da seção 4.6; linha "transação em andamento" da seção
// 9.3).
//
// Antes da queda ele respondia EM_ANDAMENTO pela mesma transação. A diferença
// é o conteúdo do abort presumido: a transação em andamento vive **apenas na
// memória** (seção 4.6), e o que não está no log não aconteceu. Por isso o
// coordenador não grava nada antes de decidir — se gravasse, teria de apagar
// depois, e a janela entre gravar e apagar seria mais um estado a explicar.
func TestCoordenadorReiniciadoRespondeAbortadaSemDecisao(t *testing.T) {
	const tx = "vaijunto:res-0000abcd"
	op := opcoesDeTerminacao(t, map[string][]byte{
		"vaijunto":     caronasDe("vaijunto", 3),
		"rotacapixaba": caronasDe("rotacapixaba", 3),
	})
	consorcio := subirConsorcioP2(t, op)
	ctx := context.Background()

	// Com a transação em votação, a resposta é EM_ANDAMENTO: ainda pode
	// confirmar, e quem perguntou deve continuar esperando.
	consorcio["vaijunto"].estado.MarcarTransacaoEmVotacao(tx)
	resposta, err := consorcio["rotacapixaba"].rede.Consultar(ctx, "vaijunto", tx)
	if err != nil {
		t.Fatalf("Consultar antes da queda: %v", err)
	}
	if resposta.Papel != "COORDENADOR" || resposta.Decisao != dominio.DecisaoEmAndamento {
		t.Fatalf("resposta antes da queda = %+v, want COORDENADOR/EM_ANDAMENTO", resposta)
	}

	religarEmpresa(t, consorcio, "vaijunto", op)

	resposta, err = consorcio["rotacapixaba"].rede.Consultar(ctx, "vaijunto", tx)
	if err != nil {
		t.Fatalf("Consultar após o reinício: %v", err)
	}
	if resposta.Papel != "COORDENADOR" || resposta.Decisao != dominio.DecisaoAbortada {
		t.Errorf("resposta após o reinício = %+v, want COORDENADOR/ABORTADA", resposta)
	}

	// Responder ABORTADA não grava nada: o coordenador continua sem
	// participação nenhuma para esta transação, e um PREPARAR legítimo que
	// chegasse depois não encontraria um veto que ninguém pediu.
	if n := len(consorcio["vaijunto"].estado.Participacoes()); n != 0 {
		t.Errorf("a consulta criou %d participação(ões) no coordenador, want 0", n)
	}
}

// Reserva confirmada e não concluída é concluída depois do reinício (segunda
// linha da tabela da seção 9.3).
//
// É o passo 4 do argumento de atomicidade da seção 7.4 — "decisão gravada
// sempre chega" — estendido à queda do próprio coordenador. O cenário é a
// janela do TF2: o DECISAO_CONFIRMAR está no log com fsync, e nenhum CONFIRMAR
// chegou a sair. Se o boot não retomasse a fase 2, o participante ficaria em
// PREPARADO para sempre enquanto o coordenador se dá por satisfeito, que é a
// quebra mais grave possível da I-P2-5.
//
// O estado é montado chamando o ponto de commit direto, e não pela porta TCP,
// porque o que se quer é o instante exato entre gravar a decisão e enviá-la —
// um instante que o coordenador normal atravessa rápido demais para um teste
// pegar sem corrida.
func TestReservaNaoConcluidaRetomaAFase2NoReinicio(t *testing.T) {
	const tx = "vaijunto:res-00000001"
	op := opcoesDeTerminacao(t, map[string][]byte{
		"vaijunto":     caronasDe("vaijunto", 3),
		"rotacapixaba": caronasDe("rotacapixaba", 3),
	})
	consorcio := subirConsorcioP2(t, op)
	participantes := []string{"rotacapixaba"}

	// A rotacapixaba vota SIM e fica com o assento em espera.
	prepararEntre(t, consorcio, "vaijunto", "rotacapixaba", tx, participantes)

	// A vaijunto grava a decisão de confirmar — o ponto de commit, com fsync —
	// e cai antes de enviar o CONFIRMAR.
	pernas := []dominio.Perna{{
		CaronaID:      "rotacapixaba:car-1",
		Empresa:       "rotacapixaba",
		MotoristaNome: "João",
		De:            0,
		Ate:           1,
		Cidades:       []string{"Salvador", "Feira de Santana"},
		Partida:       time.Date(2026, 10, 1, 6, 0, 0, 0, dominio.FusoDasCidades()),
		Chegada:       time.Date(2026, 10, 1, 7, 45, 0, 0, dominio.FusoDasCidades()),
		PrecoCentavos: 2000,
	}}
	itens := []dominio.ItemReserva{{CaronaID: "rotacapixaba:car-1", De: 0, Ate: 1}}
	if _, _, err := consorcio["vaijunto"].estado.ConfirmarReserva(
		tx, "maria", participantes, itens, pernas, time.Now().In(dominio.FusoDasCidades())); err != nil {
		t.Fatalf("ConfirmarReserva: %v", err)
	}
	derrubarEmpresa(t, consorcio["vaijunto"])

	// Até aqui a reserva está confirmada no coordenador e o dono do assento
	// ainda está em PREPARADO: é o estado que o reinício tem de resolver.
	if pt, existe := consorcio["rotacapixaba"].estado.Participacao(tx); !existe || pt.Situacao != dominio.SituacaoPreparado {
		t.Fatalf("antes do reinício: participação = %+v (existe=%t), want PREPARADO", pt, existe)
	}

	vaijunto := religarEmpresa(t, consorcio, "vaijunto", op)

	// O replay recria a reserva pelo DECISAO_CONFIRMAR, a recuperação vê que
	// ela não foi concluída e retoma a fase 2.
	esperarSituacao(t, consorcio["rotacapixaba"], tx, dominio.SituacaoConfirmado)

	// E a fase 2 termina de verdade: o CONCLUIDA é gravado quando todos os
	// participantes confirmaram o recebimento.
	limite := time.Now().Add(prazoDeQuiescencia)
	for time.Now().Before(limite) {
		if r, existe := vaijunto.estado.Reserva(tx); existe && r.Concluida {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	r, existe := vaijunto.estado.Reserva(tx)
	if !existe || !r.Concluida {
		t.Fatalf("reserva após o reinício = %+v (existe=%t), want concluída", r, existe)
	}

	// O assento continua descontado — em espera virou confirmado, e a conta de
	// I-P2-1 não muda — e as cinco invariantes valem nas duas empresas.
	conferirAssentosLivres(t, consorcio["rotacapixaba"], 2)
	esperarQuiescencia(t, consorcio)
	verificarInvariantesP2(t, auditoriasDe(t, consorcio))
}
