package testes

import (
	"fmt"
	"testing"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/federacao"
	"vaijunto/internal/protocolo"
)

// Cenários de cancelamento e outbox (PROJETO_P2.md, seção 10; seção 11.3:
// TC1 a TC4; D29, D39).
//
// Diferente da confirmação, cancelar não tem votação a disputar — nenhuma
// empresa pode recusá-lo —, então o que estes testes afirmam não é
// atomicidade entre várias tentativas, e sim **entrega confiável** de uma
// decisão já tomada: o aviso sobrevive à queda do destino, chega quando ele
// volta, e nunca devolve um assento antes da hora (seção 10.3).
//
// As caronas têm partida relativa ao relógio, nunca uma data fixa do cenário:
// os testes cancelam, e o prazo de uma hora do passageiro (D13) é contado do
// relógio real.

// caronaFutura monta o caronas.json de uma única carona de um trecho, com
// partida relativa ao relógio.
func caronaFutura(empresaPrefixo, origem, destino string, partida, chegada time.Time, assentos, precoCentavos int) []byte {
	return fmt.Appendf(nil, `[{"id":%q,"motorista":"joao",
		"rota":[%q,%q],"horarios":[%q,%q],
		"assentos":%d,"precos_centavos":[%d]}]`,
		empresaPrefixo+":car-1", origem, destino,
		partida.Format(time.RFC3339), chegada.Format(time.RFC3339),
		assentos, precoCentavos)
}

// caronasDuasEmpresas monta um itinerário de duas pernas encadeadas, a
// primeira na empresa coordenadora e a segunda na participante — folga de 1 h
// entre elas, acima da MARGEM_BALDEACAO e bem abaixo do teto, para que o
// encadeamento passe na validação do coordenador (D34) sem que isso seja o
// que o teste mede.
func caronasDuasEmpresas(coordenadora, participante string, assentosCoord, assentosPart int) map[string][]byte {
	base := diaSeguroNoFuturo()
	return map[string][]byte{
		coordenadora: caronaFutura(coordenadora, "Salvador", "Feira de Santana",
			base, base.Add(2*time.Hour), assentosCoord, 2000),
		participante: caronaFutura(participante, "Feira de Santana", "Jequié",
			base.Add(3*time.Hour), base.Add(5*time.Hour), assentosPart, 3000),
	}
}

// TC1 — o passageiro cancela a reserva com um participante remoto fora do ar
// (seção 11.3; I-P2-5).
//
// A resposta ao passageiro é imediata: o coordenador decide o cancelamento
// sem votar nada (D39), cancela a perna local e enfileira o aviso
// CANCELAR_PERNAS para o participante caído. O assento dele só volta quando o
// aviso for entregue — a janela conservadora da seção 10.3 — e é isso que o
// teste observa antes de religar.
func TestCancelamentoDeReserva_ParticipanteForaDoAr_OutboxEntregaNaVolta(t *testing.T) {
	op := opcoesConsorcio{
		caronas:      caronasDuasEmpresas("vaijunto", "rotacapixaba", 3, 3),
		tcp:          true,
		dirLogs:      t.TempDir(),
		auditoria:    true,
		prazoChamada: chamadaNosTestes,
		prazoFase2:   fase2RapidaNosTestes,
	}
	consorcio := subirConsorcioP2(t, op)

	passageiro := conectar(t, consorcio["vaijunto"].endereco)
	passageiro.entrar("maria", "abcd")
	var confirmada protocolo.ReservarResposta
	passageiro.exigirOK(protocolo.TipoReservar, reserva(
		trecho("vaijunto:car-1", 0, 1), trecho("rotacapixaba:car-1", 0, 1)), &confirmada)
	tx := confirmada.ReservaID

	conferirAssentosLivres(t, consorcio["rotacapixaba"], 2)

	derrubarEmpresa(t, consorcio["rotacapixaba"])

	// O cancelamento responde OK sem esperar o participante caído: é decisão
	// unilateral, não votação.
	var cancelada protocolo.CancelarReservaResposta
	passageiro.exigirOK(protocolo.TipoCancelarReserva,
		protocolo.CancelarReservaRequisicao{ReservaID: tx}, &cancelada)
	if cancelada.ReservaID != tx {
		t.Fatalf("CANCELAR_RESERVA devolveu %q, want %q", cancelada.ReservaID, tx)
	}

	// A perna local já voltou ao mercado.
	conferirAssentosLivres(t, consorcio["vaijunto"], 3)

	// O aviso está pendente na auditoria da vaijunto, esperando o participante
	// voltar — é a janela da seção 10.3.
	a := lerAuditoria(t, consorcio["vaijunto"].urlBase())
	if len(a.Outbox) != 1 {
		t.Fatalf("outbox da vaijunto = %+v, want exatamente 1 aviso pendente", a.Outbox)
	}

	religarEmpresa(t, consorcio, "rotacapixaba", op)

	// O outbox entrega sozinho quando o participante volta, sem que ninguém
	// de fora precise pedir de novo.
	esperarSituacao(t, consorcio["rotacapixaba"], tx, dominio.SituacaoCancelado)
	conferirAssentosLivres(t, consorcio["rotacapixaba"], 3)

	esperarQuiescencia(t, consorcio)
	verificarInvariantesP2(t, auditoriasDe(t, consorcio))
}

// TC2 — o motorista cancela uma carona usada por reserva de outra empresa,
// com o coordenador fora do ar (seção 11.3; D29).
//
// vaijunto só participa da transação — quem a coordena é a rotacapixaba, que
// também é dona da primeira perna. O cancelamento da carona em vaijunto
// cancela a perna local na hora e, como vaijunto não coordena, enfileira um
// aviso CARONA_CANCELADA para a rotacapixaba decidir o resto. Enquanto ela
// está fora, a reserva continua "ativa" do ponto de vista dela — a mesma
// janela conservadora do TC1, vista do outro lado da cascata.
func TestCancelamentoDeCarona_CoordenadorForaDoAr_AvisoEntregaNaVolta(t *testing.T) {
	op := opcoesConsorcio{
		caronas:      caronasDuasEmpresas("rotacapixaba", "vaijunto", 3, 3),
		tcp:          true,
		dirLogs:      t.TempDir(),
		auditoria:    true,
		prazoChamada: chamadaNosTestes,
		prazoFase2:   fase2RapidaNosTestes,
	}
	consorcio := subirConsorcioP2(t, op)

	passageiro := conectar(t, consorcio["rotacapixaba"].endereco)
	passageiro.entrar("maria", "abcd")
	var confirmada protocolo.ReservarResposta
	passageiro.exigirOK(protocolo.TipoReservar, reserva(
		trecho("rotacapixaba:car-1", 0, 1), trecho("vaijunto:car-1", 0, 1)), &confirmada)
	tx := confirmada.ReservaID

	// A fase 2 roda em segundo plano (seção 7.1, passo 9): o passageiro já
	// recebeu OK, mas o CONFIRMAR à vaijunto pode não ter chegado ainda. Sem
	// esperar, o cancelamento encontraria a participação em PREPARADO e seria
	// recusado por CARONA_EM_TRANSACAO — um resultado real, só não o que este
	// cenário quer observar.
	esperarSituacao(t, consorcio["vaijunto"], tx, dominio.SituacaoConfirmado)

	derrubarEmpresa(t, consorcio["rotacapixaba"])

	motorista := conectar(t, consorcio["vaijunto"].endereco)
	motorista.entrar("joao", "1234")
	var cascata protocolo.CancelarCaronaResposta
	motorista.exigirOK(protocolo.TipoCancelarCarona,
		protocolo.CancelarCaronaRequisicao{CaronaID: "vaijunto:car-1"}, &cascata)
	if cascata.ReservasCanceladas != 1 {
		t.Fatalf("reservas_canceladas = %d, want 1 (perna local cancelada agora)", cascata.ReservasCanceladas)
	}

	// A perna local já voltou ao mercado, e o aviso espera na auditoria da
	// vaijunto — ela não coordena, então só avisa.
	conferirAssentosLivres(t, consorcio["vaijunto"], 3)
	a := lerAuditoria(t, consorcio["vaijunto"].urlBase())
	if len(a.Outbox) != 1 || a.Outbox[0].Destino != "rotacapixaba" {
		t.Fatalf("outbox da vaijunto = %+v, want exatamente 1 aviso para rotacapixaba", a.Outbox)
	}

	religarEmpresa(t, consorcio, "rotacapixaba", op)

	// O aviso entrega sozinho: a rotacapixaba decide o cancelamento do
	// itinerário inteiro e avisa de volta, inclusive a própria vaijunto
	// (idempotente — a perna local já estava cancelada).
	rotacapixaba := consorcio["rotacapixaba"]
	limite := time.Now().Add(prazoDeQuiescencia)
	for time.Now().Before(limite) {
		if r, existe := rotacapixaba.estado.Reserva(tx); existe && !r.Ativa {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r, existe := rotacapixaba.estado.Reserva(tx); !existe || r.Ativa {
		t.Fatalf("reserva %s na rotacapixaba = %+v (existe=%t), want cancelada", tx, r, existe)
	}
	conferirAssentosLivres(t, rotacapixaba, 3)

	esperarQuiescencia(t, consorcio)
	verificarInvariantesP2(t, auditoriasDe(t, consorcio))
}

// TC3 — o motorista tenta cancelar uma carona com uma participação em
// PREPARADO (seção 11.3; D39).
//
// Cancelar não pode abortar quem já votou SIM, então a recusa é por inteiro,
// sem gravar nada: CARONA_EM_TRANSACAO. A participação fica presa em
// PREPARADO porque a rede em memória nunca entrega o PREPARAR à rotacapixaba
// dentro do prazo de votação — para a vaijunto, que vota localmente e sem
// rede (D21), isso não muda nada: ela já registrou o voto SIM.
func TestCancelarCarona_ComParticipacaoPreparada_RecusaCaronaEmTransacao(t *testing.T) {
	hub := federacao.NovaRedeEmMemoria()
	hub.Atrasar("rotacapixaba", 10*time.Second)

	op := opcoesConsorcio{
		caronas:       caronasDuasEmpresas("vaijunto", "rotacapixaba", 2, 2),
		tcp:           true,
		auditoria:     true,
		redeEmMemoria: hub,
		prazoChamada:  chamadaNosTestes,
		prazoVotacao:  300 * time.Millisecond,
	}
	consorcio := subirConsorcioP2(t, op)

	reservarEmSegundoPlano(t, consorcio["vaijunto"].endereco, "maria", "abcd",
		trecho("vaijunto:car-1", 0, 1), trecho("rotacapixaba:car-1", 0, 1))

	pt := esperarParticipacao(t, consorcio["vaijunto"])

	motorista := conectar(t, consorcio["vaijunto"].endereco)
	motorista.entrar("joao", "1234")
	recusa := motorista.enviar(protocolo.TipoCancelarCarona,
		protocolo.CancelarCaronaRequisicao{CaronaID: "vaijunto:car-1"})
	if recusa.Codigo != protocolo.CodigoCaronaEmTransacao {
		t.Fatalf("CANCELAR_CARONA: codigo = %q, want %s (mensagem: %q)",
			recusa.Codigo, protocolo.CodigoCaronaEmTransacao, recusa.Mensagem)
	}
	// A recusa não mudou nada: o assento continua em espera.
	conferirAssentosLivres(t, consorcio["vaijunto"], 1)

	// A rotacapixaba nunca responde dentro do prazo de votação, então a
	// transação aborta por EMPRESA_INDISPONIVEL — e a participação da
	// vaijunto, agora ABORTADO, libera a carona para o cancelamento.
	esperarSituacao(t, consorcio["vaijunto"], pt.TxID, dominio.SituacaoAbortado)
	conferirAssentosLivres(t, consorcio["vaijunto"], 2)

	var cancelada protocolo.CancelarCaronaResposta
	motorista.exigirOK(protocolo.TipoCancelarCarona,
		protocolo.CancelarCaronaRequisicao{CaronaID: "vaijunto:car-1"}, &cancelada)
}

// TC4 — o detalhe da carona mostra o passageiro de outra empresa só com
// referência e nome (seção 11.3; D32).
//
// vaijunto só participa da transação — quem coordena é a rotacapixaba —, e
// por isso não existe Reserva nenhuma na vaijunto para esse passageiro: o que
// existe é a Participacao que o PREPARAR criou, com a referência opaca e o
// nome que atravessaram a fronteira no voto. Nenhum outro dado do passageiro
// (login local, perfil) pode aparecer.
func TestDetalharCarona_PassageiroDeOutraEmpresaSoComReferenciaENome(t *testing.T) {
	op := opcoesConsorcio{
		caronas:      caronasDuasEmpresas("rotacapixaba", "vaijunto", 3, 3),
		tcp:          true,
		auditoria:    true,
		prazoChamada: chamadaNosTestes,
	}
	consorcio := subirConsorcioP2(t, op)

	passageiro := conectar(t, consorcio["rotacapixaba"].endereco)
	passageiro.entrar("maria", "abcd")
	passageiro.exigirOK(protocolo.TipoReservar, reserva(
		trecho("rotacapixaba:car-1", 0, 1), trecho("vaijunto:car-1", 0, 1)), nil)

	motorista := conectar(t, consorcio["vaijunto"].endereco)
	motorista.entrar("joao", "1234")
	var detalhe protocolo.DetalharCaronaResposta
	motorista.exigirOK(protocolo.TipoDetalharCarona,
		protocolo.DetalharCaronaRequisicao{CaronaID: "vaijunto:car-1"}, &detalhe)

	if len(detalhe.Trechos) != 1 || len(detalhe.Trechos[0].Passageiros) != 1 {
		t.Fatalf("DETALHAR_CARONA = %+v, want exatamente 1 passageiro no trecho 0", detalhe)
	}
	p := detalhe.Trechos[0].Passageiros[0]
	if p.Usuario != "rotacapixaba:maria" {
		t.Errorf("usuario = %q, want a referência opaca %q", p.Usuario, "rotacapixaba:maria")
	}
	if p.Nome == "" {
		t.Errorf("nome vazio: o motorista precisa de um nome de exibição (RF14)")
	}
}
