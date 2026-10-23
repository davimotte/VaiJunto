package testes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
	"vaijunto/internal/transacao"
)

// Injeção de falhas por pontos nomeados — TF1 a TF7 (PROJETO_P2.md, seção 9.5;
// seção 11.3; D31).
//
// A fase 6A provou o que acontece **depois** de uma queda, mas a queda era
// provocada pelos testes nos únicos instantes que eles alcançavam de fora. O
// que faltava era o meio do protocolo: cair entre o fsync do PREPARADO e a
// resposta, ou entre a decisão gravada e o primeiro CONFIRMAR, são janelas de
// microssegundos, e um teste que tentasse acertá-las com temporização seria
// instável sob -count=20 e provaria pouco quando passasse.
//
// Com os pontos de FALHAR_EM, cada um destes cenários cai **sempre** no mesmo
// lugar. Cada teste afirma a linha correspondente da tabela da seção 9.5 e,
// depois da quiescência, as cinco invariantes da seção 11.1 — porque o que a
// RD04 exige não é que a queda não doa, é que nada fique preso depois dela.
//
// Os prazos da D41 vêm em milissegundos, e dois deles decidem o cenário:
//
//   - prazoFase2 curto quando o teste depende de a repetição do CONFIRMAR
//     chegar (TF6, TF7) e **longo** quando depende de ela não chegar antes da
//     terminação (TF4, TF5), para que o caminho afirmado seja o do documento e
//     não o que o escalonador escolheu;
//   - prazoDuvida curto quando o teste quer a terminação (TF1, TF5) e folgado
//     quando quer observar um estado intermediário antes de ela agir (TF3).
//
// TF8 (aviso-apos-registrar) fica na fase 8, com o outbox da D39.

const (
	// votacaoNosTestes é o prazo de votação (D41, 5 s em produção). Largo o
	// bastante para três PREPARAR num processo sob -race, e curto o bastante
	// para que TF4 e TF5, que esperam o prazo esgotar, não dominem a suíte.
	votacaoNosTestes = 600 * time.Millisecond

	// fase2RapidaNosTestes faz a repetição de CONFIRMAR acontecer várias vezes
	// por segundo, para os cenários em que ela é o caminho de volta.
	fase2RapidaNosTestes = 25 * time.Millisecond

	// fase2LentaNosTestes é maior que o teste inteiro: a primeira tentativa de
	// CONFIRMAR ou de ABORTAR sai, falha contra a empresa caída, e a repetição
	// não volta mais durante o cenário. É assim que TF4 e TF5 isolam a
	// terminação cooperativa do reenvio do coordenador.
	fase2LentaNosTestes = 5 * time.Second

	// duvidaFolgadaNosTestes mantém a terminação fora do caminho enquanto o
	// teste observa um estado intermediário (TF3).
	duvidaFolgadaNosTestes = 5 * time.Second
)

// opcoesDeFalha é a configuração comum dos cenários desta seção: itinerário
// encadeado entre as empresas pedidas, porta TCP aberta (o RESERVAR entra como
// um passageiro de verdade), log em arquivo — o caminho completo do boot da
// seção 9.2, que é o que a recuperação precisa — e auditoria para o
// verificador.
func opcoesDeFalha(t *testing.T, assentos map[string]int, falhaEm map[string]string) opcoesConsorcio {
	t.Helper()
	return opcoesConsorcio{
		caronas:       caronasEncadeadas(assentos),
		tcp:           true,
		dirLogs:       t.TempDir(),
		auditoria:     true,
		falhaEm:       falhaEm,
		prazoChamada:  chamadaNosTestes,
		prazoVotacao:  votacaoNosTestes,
		prazoDuvida:   duvidaNosTestes,
		prazoConsulta: consultaNosTestes,
		prazoFase2:    fase2RapidaNosTestes,
	}
}

// resultadoDoReservar é o que o RESERVAR em segundo plano devolve. O erro é
// esperado nos cenários em que o coordenador cai no meio: a conexão do
// passageiro morre sem resposta, e isso **é** o comportamento afirmado.
type resultadoDoReservar struct {
	resposta protocolo.Resposta
	err      error
}

// reservarEmSegundoPlano autentica e dispara um RESERVAR numa goroutine,
// devolvendo o canal da resposta.
//
// É em segundo plano porque, nos cenários em que o coordenador cai, a resposta
// nunca chega: servidor.Fechar() encerra o listener e não as conexões já
// abertas, então quem está esperando a resposta fica esperando até o prazo da
// conexão. O teste precisa, nesse meio-tempo, observar a queda e religar a
// empresa — e não pode fazer isso bloqueado numa leitura.
//
// Conectar e autenticar ficam na goroutine do teste, onde t.Fatalf é legítimo;
// só o envio vai para a goroutine, com erro devolvido em vez de t.Fatalf.
func reservarEmSegundoPlano(t *testing.T, endereco, usuario, senha string, itens ...protocolo.ItemReserva) <-chan resultadoDoReservar {
	t.Helper()
	c := conectar(t, endereco)
	c.entrar(usuario, senha)

	saida := make(chan resultadoDoReservar, 1)
	go func() {
		resposta, err := enviarTolerandoQueda(c, protocolo.TipoReservar, reserva(itens...))
		saida <- resultadoDoReservar{resposta: resposta, err: err}
	}()
	return saida
}

// enviarTolerandoQueda é o enviar de integracao_test.go sem os t.Fatalf: a
// conexão morrer no meio é um resultado possível, e não uma falha do teste.
func enviarTolerandoQueda(c *cliente, tipo string, dados any) (protocolo.Resposta, error) {
	corpo, err := json.Marshal(dados)
	if err != nil {
		return protocolo.Resposta{}, fmt.Errorf("montar dados de %s: %w", tipo, err)
	}
	c.seq++
	linha, err := json.Marshal(protocolo.Requisicao{ID: fmt.Sprintf("req-%d", c.seq), Tipo: tipo, Dados: corpo})
	if err != nil {
		return protocolo.Resposta{}, fmt.Errorf("montar envelope de %s: %w", tipo, err)
	}
	if err := c.conn.SetDeadline(time.Now().Add(prazoDeQuiescencia)); err != nil {
		return protocolo.Resposta{}, err
	}
	if _, err := c.conn.Write(append(linha, '\n')); err != nil {
		return protocolo.Resposta{}, err
	}
	bruta, err := c.leitor.LerLinha()
	if err != nil {
		return protocolo.Resposta{}, err
	}
	return protocolo.DecodificarResposta(bruta)
}

// esperarParticipacao espera que a empresa tenha exatamente uma participação e
// a devolve.
//
// Os cenários desta seção não conhecem o txID de antemão: ele nasce dentro do
// coordenador, numa tentativa (D36), e é por ele que as asserções olham o
// estado dos donos. Uma participação só, porque cada cenário faz uma reserva.
func esperarParticipacao(t *testing.T, e *empresa) dominio.Participacao {
	t.Helper()
	limite := time.Now().Add(prazoDeQuiescencia)
	for time.Now().Before(limite) {
		if pts := e.estado.Participacoes(); len(pts) == 1 {
			return pts[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: participações = %+v, want exatamente uma em %s",
		e.nome, e.estado.Participacoes(), prazoDeQuiescencia)
	return dominio.Participacao{}
}

// conferirSituacoes afirma a situação da participação da transação em cada
// empresa listada.
func conferirSituacoes(t *testing.T, consorcio map[string]*empresa, txID string, querida dominio.Situacao, empresas ...string) {
	t.Helper()
	for _, nome := range empresas {
		pt, existe := consorcio[nome].estado.Participacao(txID)
		if !existe || pt.Situacao != querida {
			t.Errorf("%s: participação %s = %+v (existe=%t), want %s", nome, txID, pt, existe, querida)
		}
	}
}

// esperarSituacoes espera a situação pedida em cada empresa listada.
func esperarSituacoes(t *testing.T, consorcio map[string]*empresa, txID string, querida dominio.Situacao, empresas ...string) {
	t.Helper()
	for _, nome := range empresas {
		esperarSituacao(t, consorcio[nome], txID, querida)
	}
}

// esperarReservaConcluida espera o CONCLUIDA da fase 2 (seção 4.8).
//
// É a prova de que a fase 2 não só chegou a todos como **soube** que chegou: o
// CONCLUIDA só é gravado depois de cada participante confirmar o recebimento.
func esperarReservaConcluida(t *testing.T, e *empresa, txID string) {
	t.Helper()
	limite := time.Now().Add(prazoDeQuiescencia)
	for time.Now().Before(limite) {
		if r, existe := e.estado.Reserva(txID); existe && r.Concluida {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	r, existe := e.estado.Reserva(txID)
	t.Fatalf("%s: reserva %s = %+v (existe=%t), want concluída em %s", e.nome, txID, r, existe, prazoDeQuiescencia)
}

// lerLog devolve o conteúdo do log de uma empresa.
//
// Os cenários do participante afirmam coisas sobre o **log**, e não só sobre a
// memória: a diferença entre o TF4 e o TF5 é exatamente um registro gravado com
// fsync, e é no arquivo que ela aparece. Olhar a memória da instância religada
// não serviria: ela é construída a partir deste arquivo.
func lerLog(t *testing.T, op opcoesConsorcio, nome string) string {
	t.Helper()
	conteudo, err := os.ReadFile(filepath.Join(op.dirLogs, nome+".wal"))
	if err != nil {
		t.Fatalf("ler log de %s: %v", nome, err)
	}
	return string(conteudo)
}

// TF1 — o coordenador cai depois de enviar todos os PREPARAR, sem decidir
// (seção 9.5).
//
// O estado no instante da queda é o mais delicado do 2PC: todos os donos
// garantiram o assento e ninguém sabe o destino da transação, porque ele nunca
// existiu fora da memória do coordenador. Nenhum participante pode decidir
// sozinho (passo 3 do argumento da seção 7.4), e é por isso que o cenário fica
// parado até o coordenador voltar — o bloqueio residual da seção 9.6.
//
// Quando ele volta, o replay não encontra DECISAO_CONFIRMAR e a transação não
// está na memória, porque nunca sobreviveria a um reinício. Então o abort
// presumido (D23) responde ABORTADA, e os três assentos voltam. O coordenador
// não gravou **nada** em nenhum momento, e é isso que torna o abort presumido
// seguro: uma transação sem decisão no log nunca mais pode ser confirmada.
func TestTF1_CoordenadorCaiAposPrepararSemDecidir(t *testing.T) {
	assentos := map[string]int{"vaijunto": 3, "rotacapixaba": 3, "rotafluminense": 3}
	op := opcoesDeFalha(t, assentos, map[string]string{
		"vaijunto": string(transacao.PontoCoordAposPreparar),
	})
	consorcio := subirConsorcioP2(t, op)
	donos := []string{"vaijunto", "rotacapixaba", "rotafluminense"}

	reservarEmSegundoPlano(t, consorcio["vaijunto"].endereco, "maria", "abcd",
		trecho("vaijunto:car-1", 0, 1),
		trecho("rotacapixaba:car-1", 0, 1),
		trecho("rotafluminense:car-1", 0, 1))

	if ponto := esperarQueda(t, consorcio["vaijunto"]); ponto != transacao.PontoCoordAposPreparar {
		t.Fatalf("caiu em %q, want %q", ponto, transacao.PontoCoordAposPreparar)
	}

	// Os três donos votaram SIM antes da queda, então os três têm um assento em
	// espera — inclusive a vaijunto, que é participante de si mesma (D21).
	tx := esperarParticipacao(t, consorcio["rotacapixaba"]).TxID
	conferirSituacoes(t, consorcio, tx, dominio.SituacaoPreparado, "rotacapixaba", "rotafluminense")
	for _, dono := range []string{"rotacapixaba", "rotafluminense"} {
		conferirAssentosLivres(t, consorcio[dono], 2)
	}

	// Nenhum dos dois decide enquanto o coordenador está fora, por mais rodadas
	// de consulta que aconteçam: cada um pergunta à vaijunto e não é atendido,
	// pergunta ao outro e ouve PREPARADO. É o bloqueio residual, e é o que
	// torna o 2PC correto em vez de falho.
	janela := 4 * consultaNosTestes
	for _, dono := range []string{"rotacapixaba", "rotafluminense"} {
		conferirSituacaoEstavel(t, consorcio[dono], tx, dominio.SituacaoPreparado, janela)
	}

	vaijunto := religarEmpresa(t, consorcio, "vaijunto", op)

	// Religada, a vaijunto responde ABORTADA e os três assentos voltam. A
	// participação local dela sai pelo mesmo caminho: o terminador consulta a
	// própria empresa, sem HTTP contra si mesma.
	esperarSituacoes(t, consorcio, tx, dominio.SituacaoAbortado, donos...)
	for _, dono := range donos {
		conferirAssentosLivres(t, consorcio[dono], 3)
	}
	if n := len(vaijunto.estado.ReservasNaoConcluidas()); n != 0 {
		t.Errorf("reservas a retomar após o reinício = %d, want 0 (nada foi decidido)", n)
	}

	esperarQuiescencia(t, consorcio)
	verificarInvariantesP2(t, auditoriasDe(t, consorcio))
}

// TF2 — o coordenador cai com a decisão gravada e nenhum CONFIRMAR enviado
// (seção 9.5).
//
// É o cenário que o relatório cita: **o passageiro não sabe o resultado de uma
// operação que deu certo**. A decisão está no log com fsync, logo a reserva
// existe; mas a resposta OK morreu com a conexão, e nada no protocolo de
// requisição/resposta pode devolvê-la depois.
//
// O que o sistema garante é outra coisa, e é o suficiente: a decisão gravada
// sempre chega (passo 4 da seção 7.4). Na volta, a recuperação encontra uma
// reserva confirmada e não concluída, retoma a fase 2, e os donos passam de
// PREPARADO a CONFIRMADO. O passageiro descobre o resultado olhando — a reserva
// está na lista dele.
func TestTF2_CoordenadorCaiAposDecidirERetomaAFase2(t *testing.T) {
	assentos := map[string]int{"vaijunto": 3, "rotacapixaba": 3, "rotafluminense": 3}
	op := opcoesDeFalha(t, assentos, map[string]string{
		"vaijunto": string(transacao.PontoCoordAposDecidir),
	})
	consorcio := subirConsorcioP2(t, op)
	donos := []string{"vaijunto", "rotacapixaba", "rotafluminense"}

	resultado := reservarEmSegundoPlano(t, consorcio["vaijunto"].endereco, "maria", "abcd",
		trecho("vaijunto:car-1", 0, 1),
		trecho("rotacapixaba:car-1", 0, 1),
		trecho("rotafluminense:car-1", 0, 1))

	if ponto := esperarQueda(t, consorcio["vaijunto"]); ponto != transacao.PontoCoordAposDecidir {
		t.Fatalf("caiu em %q, want %q", ponto, transacao.PontoCoordAposDecidir)
	}

	// O passageiro perde a resposta: a goroutine da conexão morreu com a queda,
	// e o defer conn.Close() dela deu EOF a quem esperava.
	if r := <-resultado; r.err == nil {
		t.Errorf("RESERVAR respondeu %+v, want conexão encerrada sem resposta", r.resposta)
	}

	tx := esperarParticipacao(t, consorcio["rotacapixaba"]).TxID
	conferirSituacoes(t, consorcio, tx, dominio.SituacaoPreparado, "rotacapixaba", "rotafluminense")

	vaijunto := religarEmpresa(t, consorcio, "vaijunto", op)

	// A fase 2 recomeça pelo mesmo laço de sempre: a retomada não é um caminho
	// novo, é o caminho normal reiniciado (seção 9.3).
	esperarSituacoes(t, consorcio, tx, dominio.SituacaoConfirmado, donos...)
	esperarReservaConcluida(t, vaijunto, tx)

	// E o passageiro encontra a reserva ao listar, numa conexão nova: é assim
	// que o resultado perdido se torna observável.
	passageiro := conectar(t, vaijunto.endereco)
	passageiro.entrar("maria", "abcd")
	var listadas protocolo.ListarMinhasReservasResposta
	passageiro.exigirOK(protocolo.TipoListarMinhasReservas,
		protocolo.ListarMinhasReservasRequisicao{}, &listadas)
	if len(listadas.Reservas) != 1 || listadas.Reservas[0].ReservaID != tx {
		t.Fatalf("reservas de maria = %+v, want exatamente a %s", listadas.Reservas, tx)
	}
	if n := len(listadas.Reservas[0].Trechos); n != 3 {
		t.Errorf("trechos da reserva = %d, want 3 (um por empresa)", n)
	}

	// O assento saiu de "em espera" para "confirmado" sem passar pelo mercado:
	// a conta da I-P2-1 não muda, e é por isso que a queda não vendeu nada duas
	// vezes.
	for _, dono := range donos {
		conferirAssentosLivres(t, consorcio[dono], 2)
	}
	esperarQuiescencia(t, consorcio)
	verificarInvariantesP2(t, auditoriasDe(t, consorcio))
}

// TF3 — o coordenador cai no meio da fase 2, com o primeiro CONFIRMAR enviado
// e os demais não (seção 9.5).
//
// É o estado que parece inconsistente e não é: um dono já está CONFIRMADO
// enquanto os outros seguem em PREPARADO. A I-P2-5 não vale aqui de propósito —
// ela é cobrada "após a quiescência" —, porque enquanto a fase 2 está no ar
// essa diferença é o funcionamento normal, e não um defeito.
//
// Na volta, a retomada reenvia o CONFIRMAR a todos. Quem já estava CONFIRMADO
// responde de novo, idempotente pelo txID (D28, regra 6), e é por isso que o
// coordenador pode reenviar sem se preocupar com o que o outro lado já fez.
//
// O prazo de dúvida vem folgado: com ele curto, a terminação cooperativa
// resolveria os outros dois antes de o teste conseguir observar o estado
// parcial — o que também é correto, mas não é o que esta linha da tabela
// descreve.
func TestTF3_CoordenadorCaiDuranteOConfirmar(t *testing.T) {
	assentos := map[string]int{"vaijunto": 3, "rotacapixaba": 3, "rotafluminense": 3}
	op := opcoesDeFalha(t, assentos, map[string]string{
		"vaijunto": string(transacao.PontoCoordDuranteConfirmar),
	})
	op.prazoDuvida = duvidaFolgadaNosTestes
	consorcio := subirConsorcioP2(t, op)
	donos := []string{"vaijunto", "rotacapixaba", "rotafluminense"}

	reservarEmSegundoPlano(t, consorcio["vaijunto"].endereco, "maria", "abcd",
		trecho("vaijunto:car-1", 0, 1),
		trecho("rotacapixaba:car-1", 0, 1),
		trecho("rotafluminense:car-1", 0, 1))

	if ponto := esperarQueda(t, consorcio["vaijunto"]); ponto != transacao.PontoCoordDuranteConfirmar {
		t.Fatalf("caiu em %q, want %q", ponto, transacao.PontoCoordDuranteConfirmar)
	}

	tx := esperarParticipacao(t, consorcio["rotacapixaba"]).TxID

	// Exatamente um dono recebeu o CONFIRMAR. Qual deles é indiferente — o laço
	// da fase 2 percorre um mapa —, e é justamente por ser indiferente que a
	// asserção é sobre a contagem.
	confirmados, preparados := 0, 0
	for _, dono := range donos {
		pt, existe := consorcio[dono].estado.Participacao(tx)
		if !existe {
			t.Fatalf("%s: sem participação de %s", dono, tx)
		}
		switch pt.Situacao {
		case dominio.SituacaoConfirmado:
			confirmados++
		case dominio.SituacaoPreparado:
			preparados++
		default:
			t.Fatalf("%s: participação = %+v, want CONFIRMADO ou PREPARADO", dono, pt)
		}
	}
	if confirmados != 1 || preparados != 2 {
		t.Fatalf("no instante da queda: %d confirmado(s) e %d preparado(s), want 1 e 2", confirmados, preparados)
	}

	vaijunto := religarEmpresa(t, consorcio, "vaijunto", op)

	esperarSituacoes(t, consorcio, tx, dominio.SituacaoConfirmado, donos...)
	esperarReservaConcluida(t, vaijunto, tx)
	for _, dono := range donos {
		conferirAssentosLivres(t, consorcio[dono], 2)
	}
	esperarQuiescencia(t, consorcio)
	verificarInvariantesP2(t, auditoriasDe(t, consorcio))
}

// TF4 — o participante cai ao receber o PREPARAR, antes do fsync (seção 9.5).
//
// A afirmação é sobre o log: ele não tem **um byte**. É a regra 3 do CLAUDE.md
// e a da seção 9.1 — nenhuma resposta que comprometa o servidor sai antes do
// fsync do registro correspondente — vista do outro lado: como nada foi
// gravado, nada foi prometido, e a instância que volta não tem o que recuperar.
//
// Do lado do coordenador, a empresa que não responde é indistinguível de
// qualquer outra queda (D30): ele repete o PREPARAR, que é idempotente, até o
// prazo de votação esgotar, e então aborta com EMPRESA_INDISPONIVEL. Nenhum
// assento fica preso, inclusive o da perna que já tinha votado SIM.
//
// O prazo da fase 2 vem longo para isolar o cenário: a primeira tentativa de
// ABORTAR sai com a rotacapixaba ainda fora e falha, e a repetição não volta
// durante o teste. Assim, a participação que se observa depois do religamento é
// só o que o replay produziu — e não um veto que o ABORTAR atrasado gravou.
func TestTF4_ParticipanteCaiAntesDeGravar(t *testing.T) {
	assentos := map[string]int{"vaijunto": 3, "rotacapixaba": 3}
	op := opcoesDeFalha(t, assentos, map[string]string{
		"rotacapixaba": string(transacao.PontoPartAntesDeGravar),
	})
	op.prazoFase2 = fase2LentaNosTestes
	consorcio := subirConsorcioP2(t, op)

	resultado := reservarEmSegundoPlano(t, consorcio["vaijunto"].endereco, "maria", "abcd",
		trecho("vaijunto:car-1", 0, 1),
		trecho("rotacapixaba:car-1", 0, 1))

	if ponto := esperarQueda(t, consorcio["rotacapixaba"]); ponto != transacao.PontoPartAntesDeGravar {
		t.Fatalf("caiu em %q, want %q", ponto, transacao.PontoPartAntesDeGravar)
	}

	// O coordenador está no ar, então o passageiro recebe a recusa: a empresa
	// dona de uma das pernas não respondeu dentro do prazo de votação.
	r := <-resultado
	if r.err != nil {
		t.Fatalf("RESERVAR: %v", r.err)
	}
	if r.resposta.Status != protocolo.StatusErro || r.resposta.Codigo != protocolo.CodigoEmpresaIndisponivel {
		t.Fatalf("RESERVAR = %+v, want ERRO/%s", r.resposta, protocolo.CodigoEmpresaIndisponivel)
	}

	// O ponto é este: nada foi gravado.
	if conteudo := lerLog(t, op, "rotacapixaba"); conteudo != "" {
		t.Errorf("log da rotacapixaba = %q, want vazio (nada foi gravado antes do fsync)", conteudo)
	}

	// A perna da vaijunto, que votou SIM, voltou pelo ABORTAR local.
	esperarSituacao(t, consorcio["vaijunto"], esperarParticipacao(t, consorcio["vaijunto"]).TxID, dominio.SituacaoAbortado)
	conferirAssentosLivres(t, consorcio["vaijunto"], 3)

	rotacapixaba := religarEmpresa(t, consorcio, "rotacapixaba", op)
	if pts := rotacapixaba.estado.Participacoes(); len(pts) != 0 {
		t.Errorf("participações após o reinício = %+v, want nenhuma", pts)
	}
	conferirAssentosLivres(t, rotacapixaba, 3)

	esperarQuiescencia(t, consorcio)
	verificarInvariantesP2(t, auditoriasDe(t, consorcio))
}

// TF5 — o participante grava PREPARADO e cai antes de responder (seção 9.5).
//
// É o complemento exato do TF4, e a diferença entre os dois é um registro no
// log. Aqui o assento **está** descontado em disco, e o coordenador nunca soube:
// para ele, a empresa simplesmente não respondeu, e ele abortou por prazo sem
// gravar nada (D23).
//
// Essa assimetria é o que a terminação cooperativa existe para resolver. Na
// volta, o replay recria a participação em PREPARADO, o boot arma o prazo de
// dúvida — PreparadaEm não vai ao log (seção 4.4), então sem isso o assento
// ficaria preso para sempre — e, vencido o prazo, a consulta ao coordenador
// recebe ABORTADA pelo abort presumido. O assento volta.
//
// O prazo da fase 2 vem longo justamente para que seja a **consulta** a
// devolver o assento, e não o ABORTAR repetido do coordenador: os dois gravam o
// mesmo ABORTADO, e sem isso o teste afirmaria um caminho e exercitaria o
// outro, conforme o escalonador.
func TestTF5_ParticipanteCaiAposGravarPreparado(t *testing.T) {
	assentos := map[string]int{"vaijunto": 3, "rotacapixaba": 3}
	op := opcoesDeFalha(t, assentos, map[string]string{
		"rotacapixaba": string(transacao.PontoPartAposGravar),
	})
	op.prazoFase2 = fase2LentaNosTestes
	consorcio := subirConsorcioP2(t, op)

	resultado := reservarEmSegundoPlano(t, consorcio["vaijunto"].endereco, "maria", "abcd",
		trecho("vaijunto:car-1", 0, 1),
		trecho("rotacapixaba:car-1", 0, 1))

	if ponto := esperarQueda(t, consorcio["rotacapixaba"]); ponto != transacao.PontoPartAposGravar {
		t.Fatalf("caiu em %q, want %q", ponto, transacao.PontoPartAposGravar)
	}

	r := <-resultado
	if r.err != nil {
		t.Fatalf("RESERVAR: %v", r.err)
	}
	if r.resposta.Status != protocolo.StatusErro || r.resposta.Codigo != protocolo.CodigoEmpresaIndisponivel {
		t.Fatalf("RESERVAR = %+v, want ERRO/%s", r.resposta, protocolo.CodigoEmpresaIndisponivel)
	}

	// O PREPARADO está no log, com fsync, apesar de ninguém lá fora saber.
	conteudo := lerLog(t, op, "rotacapixaba")
	if !strings.Contains(conteudo, `"tipo":"PREPARADO"`) {
		t.Fatalf("log da rotacapixaba = %q, want um registro PREPARADO", conteudo)
	}

	rotacapixaba := religarEmpresa(t, consorcio, "rotacapixaba", op)

	// O replay traz o assento descontado de volta ao ar...
	tx := esperarParticipacao(t, rotacapixaba).TxID
	conferirAssentosLivres(t, rotacapixaba, 2)

	// ...e a terminação o devolve, ao descobrir que a transação foi abortada.
	esperarSituacao(t, rotacapixaba, tx, dominio.SituacaoAbortado)
	conferirAssentosLivres(t, rotacapixaba, 3)
	conferirAssentosLivres(t, consorcio["vaijunto"], 3)

	esperarQuiescencia(t, consorcio)
	verificarInvariantesP2(t, auditoriasDe(t, consorcio))
}

// TF6 — o participante cai depois de o voto SIM ter sido enviado (seção 9.5).
//
// O voto chegou, então a transação **confirma**: o coordenador obteve
// unanimidade, gravou a decisão e o passageiro recebeu OK — tudo isso com o
// dono de uma das pernas já morto. É o caso em que a queda não se vê de fora,
// e ele só é seguro porque o assento daquele dono está descontado em disco
// desde o fsync que antecedeu o voto.
//
// Na volta, o CONFIRMAR repetido (D41: sem limite, até a confirmação) encontra
// a participação em PREPARADO e a conclui. Pela terminação, o resultado seria o
// mesmo: ela perguntaria ao coordenador e ouviria CONFIRMADA.
func TestTF6_ParticipanteCaiAposResponderSim(t *testing.T) {
	assentos := map[string]int{"vaijunto": 3, "rotacapixaba": 3}
	op := opcoesDeFalha(t, assentos, map[string]string{
		"rotacapixaba": string(transacao.PontoPartAposResponder),
	})
	consorcio := subirConsorcioP2(t, op)

	resultado := reservarEmSegundoPlano(t, consorcio["vaijunto"].endereco, "maria", "abcd",
		trecho("vaijunto:car-1", 0, 1),
		trecho("rotacapixaba:car-1", 0, 1))

	if ponto := esperarQueda(t, consorcio["rotacapixaba"]); ponto != transacao.PontoPartAposResponder {
		t.Fatalf("caiu em %q, want %q", ponto, transacao.PontoPartAposResponder)
	}

	// O voto SIM estava no fio antes da queda, então a reserva é confirmada.
	r := <-resultado
	if r.err != nil {
		t.Fatalf("RESERVAR: %v", r.err)
	}
	var confirmada protocolo.ReservarResposta
	if r.resposta.Status != protocolo.StatusOK {
		t.Fatalf("RESERVAR = %+v, want OK (o voto SIM foi enviado antes da queda)", r.resposta)
	}
	if err := json.Unmarshal(r.resposta.Dados, &confirmada); err != nil {
		t.Fatalf("decodificar resposta do RESERVAR: %v", err)
	}
	tx := confirmada.ReservaID

	// A perna local confirma pela fase 2, que roda em segundo plano: o
	// passageiro recebe OK antes de ela terminar, e isso é seguro porque a
	// decisão já está gravada (seção 7.1, passo 9). Daí a espera, e não uma
	// conferência imediata.
	esperarSituacao(t, consorcio["vaijunto"], tx, dominio.SituacaoConfirmado)

	rotacapixaba := religarEmpresa(t, consorcio, "rotacapixaba", op)

	esperarSituacao(t, rotacapixaba, tx, dominio.SituacaoConfirmado)
	esperarReservaConcluida(t, consorcio["vaijunto"], tx)
	conferirAssentosLivres(t, rotacapixaba, 2)

	esperarQuiescencia(t, consorcio)
	verificarInvariantesP2(t, auditoriasDe(t, consorcio))
}

// TF7 — o participante grava CONFIRMADO e cai antes de responder (seção 9.5).
//
// Aqui a resposta perdida é a da fase 2, e a consequência é que o coordenador
// continua repetindo o CONFIRMAR para alguém que já confirmou. A repetição é
// inofensiva porque toda operação de transação é idempotente pelo txID (D28,
// regra 6): na volta, o mesmo CONFIRMAR encontra a participação já em
// CONFIRMADO, não grava nada e responde a mesma situação.
//
// É isso que destrava o CONCLUIDA: o coordenador não precisa saber se o
// CONFIRMAR anterior chegou, basta repetir até alguém confirmar o recebimento.
func TestTF7_ParticipanteCaiAposGravarConfirmado(t *testing.T) {
	assentos := map[string]int{"vaijunto": 3, "rotacapixaba": 3}
	op := opcoesDeFalha(t, assentos, map[string]string{
		"rotacapixaba": string(transacao.PontoPartAposConfirmar),
	})
	consorcio := subirConsorcioP2(t, op)

	resultado := reservarEmSegundoPlano(t, consorcio["vaijunto"].endereco, "maria", "abcd",
		trecho("vaijunto:car-1", 0, 1),
		trecho("rotacapixaba:car-1", 0, 1))

	if ponto := esperarQueda(t, consorcio["rotacapixaba"]); ponto != transacao.PontoPartAposConfirmar {
		t.Fatalf("caiu em %q, want %q", ponto, transacao.PontoPartAposConfirmar)
	}

	r := <-resultado
	if r.err != nil {
		t.Fatalf("RESERVAR: %v", r.err)
	}
	if r.resposta.Status != protocolo.StatusOK {
		t.Fatalf("RESERVAR = %+v, want OK", r.resposta)
	}
	var confirmada protocolo.ReservarResposta
	if err := json.Unmarshal(r.resposta.Dados, &confirmada); err != nil {
		t.Fatalf("decodificar resposta do RESERVAR: %v", err)
	}
	tx := confirmada.ReservaID

	// O CONFIRMADO está no log da empresa caída, e o coordenador não sabe: para
	// ele, a reserva continua sem concluir.
	if conteudo := lerLog(t, op, "rotacapixaba"); !strings.Contains(conteudo, `"tipo":"CONFIRMADO"`) {
		t.Fatalf("log da rotacapixaba = %q, want um registro CONFIRMADO", conteudo)
	}
	if reserva, existe := consorcio["vaijunto"].estado.Reserva(tx); !existe || reserva.Concluida {
		t.Fatalf("reserva no coordenador = %+v (existe=%t), want confirmada e não concluída", reserva, existe)
	}

	rotacapixaba := religarEmpresa(t, consorcio, "rotacapixaba", op)

	// A repetição encontra a participação já CONFIRMADO e responde o mesmo,
	// sem gravar de novo. Com isso o coordenador fecha a fase 2.
	esperarReservaConcluida(t, consorcio["vaijunto"], tx)
	conferirSituacoes(t, consorcio, tx, dominio.SituacaoConfirmado, "vaijunto", "rotacapixaba")
	conferirAssentosLivres(t, rotacapixaba, 2)

	esperarQuiescencia(t, consorcio)
	verificarInvariantesP2(t, auditoriasDe(t, consorcio))
}
