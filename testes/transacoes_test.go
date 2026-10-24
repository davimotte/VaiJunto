package testes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/federacao"
	"vaijunto/internal/transacao"
)

// Testes multiempresa do participante do 2PC (PROJETO_P2.md, seção 7.2).
//
// As empresas sobem no mesmo processo, cada uma com a sua porta HTTP numa porta
// efêmera de loopback, e conversam pelo cliente da federação — rede de verdade,
// serialização de verdade, prazos de verdade (seção 13.3). O compose fica para
// o teste final e para a demonstração.
//
// O que se afirma aqui, e não nos testes de unidade: que o caminho inteiro —
// cliente, HTTP, middleware, handler, participante, domínio, log — produz o
// resultado certo, inclusive sob disputa.

// subirConsorcioDeTransacoes sobe o consórcio sem a porta dos clientes: os
// testes deste arquivo exercitam o participante do 2PC chamando-o pela rede ou
// direto, e não pelo PROTOCOL.md.
//
// dirLogs vazio deixa o log em memória; com diretório, cada empresa passa pelo
// caminho completo do boot (D37), que é o que o teste de reinício precisa.
func subirConsorcioDeTransacoes(t *testing.T, caronasPorEmpresa map[string][]byte, dirLogs string) map[string]*empresa {
	t.Helper()
	return subirConsorcioP2(t, opcoesConsorcio{caronas: caronasPorEmpresa, dirLogs: dirLogs})
}

// caronasDe devolve o JSON de carga de uma empresa com uma única carona de
// Salvador a Feira de Santana, com os assentos pedidos.
//
// Carona montada à mão, e não a da semente, porque o número de assentos é o
// parâmetro do teste: a disputa pelo último assento precisa de uma carona com
// exatamente um.
func caronasDe(empresa string, assentos int) []byte {
	return fmt.Appendf(nil, `[{"id":%q,"motorista":"joao",
		"rota":["Salvador","Feira de Santana"],
		"horarios":["2026-10-01T06:00:00-03:00","2026-10-01T07:45:00-03:00"],
		"assentos":%d,"precos_centavos":[2000]}]`, empresa+":car-1", assentos)
}

// pedidoPara monta um PedidoPreparacao de um coordenador para uma perna de
// outra empresa.
func pedidoPara(coordenador, txID, caronaID string) dominio.PedidoPreparacao {
	return pedidoComCarimbo(coordenador, txID, caronaID, dominio.Carimbo{Lamport: 41, Empresa: coordenador})
}

// pedidoComCarimbo é o mesmo pedido com o carimbo escolhido, para os testes em
// que a precedência do wait-die é o que se afirma (seção 8.2).
func pedidoComCarimbo(coordenador, txID, caronaID string, carimbo dominio.Carimbo) dominio.PedidoPreparacao {
	return dominio.PedidoPreparacao{
		TxID:          txID,
		Coordenador:   coordenador,
		Participantes: []string{coordenador, "vaijunto"},
		Carimbo:       carimbo,
		Passageiro:    dominio.Passageiro{Ref: coordenador + ":maria", Nome: "Maria Souza"},
		Itens:         []dominio.ItemReserva{{CaronaID: caronaID, De: 0, Ate: 1}},
	}
}

// conferirIP21Em verifica a invariante mestra em todas as empresas do consórcio
// (seção 11.1).
func conferirIP21Em(t *testing.T, consorcio map[string]*empresa) {
	t.Helper()
	for nome, e := range consorcio {
		for _, o := range e.estado.Ocupacao() {
			if o.Livres < 0 {
				t.Errorf("I-P2-1 em %s: %s trecho %d com Livres = %d", nome, o.CaronaID, o.Trecho, o.Livres)
			}
			if soma := o.Livres + o.Confirmados + o.EmEspera; soma != o.Assentos {
				t.Errorf("I-P2-1 em %s: %s trecho %d: %d + %d + %d = %d, want %d",
					nome, o.CaronaID, o.Trecho, o.Livres, o.Confirmados, o.EmEspera, soma, o.Assentos)
			}
		}
	}
}

// Um PREPARAR atravessa a rede e volta com as pernas autoritativas do dono
// (D34), e o assento fica em espera no dono — não em quem coordenou.
func TestPrepararEntreEmpresasDescontaNoDono(t *testing.T) {
	consorcio := subirConsorcioDeTransacoes(t, map[string][]byte{
		"vaijunto":     caronasDe("vaijunto", 3),
		"rotacapixaba": caronasDe("rotacapixaba", 3),
	}, "")

	tx := "rotacapixaba:res-00000001"
	voto, err := consorcio["rotacapixaba"].rede.Preparar(context.Background(), "vaijunto",
		pedidoPara("rotacapixaba", tx, "vaijunto:car-1"))
	if err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	if voto.Voto != transacao.VotoSim || len(voto.Pernas) != 1 {
		t.Fatalf("voto = %+v, want SIM com 1 perna", voto)
	}
	if voto.Pernas[0].Empresa != "vaijunto" || voto.Pernas[0].MotoristaNome != "João Silva" {
		t.Errorf("perna = %+v, want empresa vaijunto e motorista João Silva", voto.Pernas[0])
	}

	// O assento saiu do dono, e a empresa do coordenador não perdeu nada: cada
	// servidor guarda só os dados da sua empresa.
	if o := consorcio["vaijunto"].estado.Ocupacao()[0]; o.Livres != 2 || o.EmEspera != 1 {
		t.Errorf("vaijunto: livres %d e em espera %d, want 2 e 1", o.Livres, o.EmEspera)
	}
	if o := consorcio["rotacapixaba"].estado.Ocupacao()[0]; o.Livres != 3 {
		t.Errorf("rotacapixaba: livres = %d, want 3 (coordenar não desconta nada)", o.Livres)
	}
	// D32: do passageiro de outra empresa, só referência e nome (I-P2-4).
	pt, existe := consorcio["vaijunto"].estado.Participacao(tx)
	if !existe || pt.Passageiro != (dominio.Passageiro{Ref: "rotacapixaba:maria", Nome: "Maria Souza"}) {
		t.Errorf("participação no dono = %+v (existe=%t)", pt, existe)
	}
	conferirIP21Em(t, consorcio)
}

// Cinquenta PREPARAR simultâneos pelo último assento de um trecho, de
// transações distintas e de três coordenadores diferentes — dois pela rede, um
// por chamada direta, porque a empresa dona também coordena transações suas
// (seção 7.1, passo 5).
//
// Exatamente um SIM. É a afirmação central de RD02, e ela não depende da rede
// nem de relógio: os cinquenta pedidos se enfileiram no mutex do único dono
// daquela carona, e só o primeiro encontra Livres >= 1 (seção 7.5).
//
// E é a **camada 1** da seção 8.3 em escala, porque aqui não há coordenador
// nenhum: ninguém confirma e ninguém aborta, então o vencedor fica em
// PREPARADO e os outros 49 caem no wait-die contra ele. Quem ganhou o assento
// foi quem chegou primeiro ao mutex, e não o mais velho — o relógio não
// escolhe o vencedor, só decide o que acontece com quem perdeu. Dando a cada
// tentativa um carimbo próprio, a resposta de cada perdedora fica
// determinada pela tabela da seção 8.2: mais velha que o portador espera
// (AGUARDE), mais nova desiste (PRECEDENCIA). Nenhuma recebe SEM_ASSENTO: o
// trecho está esgotado por um portador, não por assento vendido.
func TestCinquentaPrepararSimultaneosPeloUltimoAssento(t *testing.T) {
	consorcio := subirConsorcioDeTransacoes(t, map[string][]byte{
		"vaijunto":       caronasDe("vaijunto", 1),
		"rotacapixaba":   caronasDe("rotacapixaba", 1),
		"rotafluminense": caronasDe("rotafluminense", 1),
	}, "")

	// Dezessete de cada empresa que fala pela rede, dezesseis da própria dona,
	// que chama o participante direto. Cinquenta no total.
	type tentativa struct {
		coordenador string
		pelaRede    bool
	}
	var tentativas []tentativa
	for i := 0; i < 17; i++ {
		tentativas = append(tentativas, tentativa{"rotacapixaba", true}, tentativa{"rotafluminense", true})
	}
	for i := 0; i < 16; i++ {
		tentativas = append(tentativas, tentativa{"vaijunto", false})
	}

	votos := make([]transacao.Voto, len(tentativas))
	motivos := make([]string, len(tentativas))
	erros := make([]error, len(tentativas))
	// Um carimbo por tentativa, todos distintos: o índice vira o número de
	// Lamport, e a ordem total é a ordem dos índices. Sem isso os cinquenta
	// empatariam e a tabela do wait-die não teria o que decidir.
	carimbos := make([]dominio.Carimbo, len(tentativas))
	for i, tent := range tentativas {
		carimbos[i] = dominio.Carimbo{Lamport: uint64(i + 1), Empresa: tent.coordenador}
	}

	// Barreira: as goroutines ficam prontas e são liberadas de uma vez, para
	// que a disputa aconteça de verdade (é o padrão do disputa de
	// concorrencia_test.go).
	largada := make(chan struct{})
	var prontas, terminadas sync.WaitGroup
	prontas.Add(len(tentativas))
	terminadas.Add(len(tentativas))

	for i, tent := range tentativas {
		go func(i int, tent tentativa) {
			defer terminadas.Done()
			tx := fmt.Sprintf("%s:res-%08d", tent.coordenador, i)
			pedido := pedidoComCarimbo(tent.coordenador, tx, "vaijunto:car-1", carimbos[i])
			prontas.Done()
			<-largada

			if !tent.pelaRede {
				// Participante de si mesma: mesmo código, sem rede.
				_, err := consorcio[tent.coordenador].participante.Preparar(pedido)
				switch {
				case err == nil:
					votos[i] = transacao.VotoSim
				case errors.Is(err, dominio.ErrAguarde):
					votos[i] = transacao.VotoAguarde
				case errors.Is(err, dominio.ErrPrecedencia):
					votos[i], motivos[i] = transacao.VotoNao, transacao.MotivoPrecedencia
				case errors.Is(err, dominio.ErrSemAssento):
					votos[i], motivos[i] = transacao.VotoNao, transacao.MotivoSemAssento
				default:
					erros[i] = err
				}
				return
			}

			voto, err := consorcio[tent.coordenador].rede.Preparar(context.Background(), "vaijunto", pedido)
			if err != nil {
				erros[i] = err
				return
			}
			votos[i], motivos[i] = voto.Voto, voto.Motivo
		}(i, tent)
	}

	prontas.Wait()
	close(largada)
	terminadas.Wait()

	sins, vencedor := 0, -1
	for i := range tentativas {
		if erros[i] != nil {
			t.Fatalf("tentativa %d (%s): %v", i, tentativas[i].coordenador, erros[i])
		}
		if votos[i] == transacao.VotoSim {
			sins++
			vencedor = i
		}
	}
	if sins != 1 {
		t.Fatalf("votos SIM = %d, want exatamente 1", sins)
	}

	// A tabela da seção 8.2 aplicada ao portador que de fato ganhou: cada
	// perdedora tinha de esperar ou desistir conforme o seu carimbo, e nenhuma
	// podia receber SEM_ASSENTO.
	for i := range tentativas {
		if i == vencedor {
			continue
		}
		querVoto, querMotivo := transacao.VotoNao, transacao.MotivoPrecedencia
		if carimbos[i].MaisVelhoQue(carimbos[vencedor]) {
			querVoto, querMotivo = transacao.VotoAguarde, ""
		}
		if votos[i] != querVoto || motivos[i] != querMotivo {
			t.Errorf("tentativa %d, carimbo %+v contra o portador %+v: voto %q motivo %q, want %q %q",
				i, carimbos[i], carimbos[vencedor], votos[i], motivos[i], querVoto, querMotivo)
		}
	}
	if o := consorcio["vaijunto"].estado.Ocupacao()[0]; o.Livres != 0 || o.EmEspera != 1 {
		t.Errorf("dono: livres %d e em espera %d, want 0 e 1", o.Livres, o.EmEspera)
	}
	conferirIP21Em(t, consorcio)
}

// Confirmar e abortar repetidos pela rede respondem sempre a mesma situação, e
// os assentos não se movem (D28). É o que torna seguro o coordenador repetir
// até receber 2xx (seção 1.3 da API).
func TestConfirmarEAbortarRepetidosEntreEmpresas(t *testing.T) {
	consorcio := subirConsorcioDeTransacoes(t, map[string][]byte{
		"vaijunto":     caronasDe("vaijunto", 2),
		"rotacapixaba": caronasDe("rotacapixaba", 2),
	}, "")
	rede := consorcio["rotacapixaba"].rede
	dono := consorcio["vaijunto"].estado
	ctx := context.Background()

	confirmada := "rotacapixaba:res-00000001"
	abortada := "rotacapixaba:res-00000002"
	for _, tx := range []string{confirmada, abortada} {
		if _, err := rede.Preparar(ctx, "vaijunto", pedidoPara("rotacapixaba", tx, "vaijunto:car-1")); err != nil {
			t.Fatalf("Preparar %s: %v", tx, err)
		}
	}
	if o := dono.Ocupacao()[0]; o.Livres != 0 || o.EmEspera != 2 {
		t.Fatalf("após os dois PREPARAR: livres %d e em espera %d, want 0 e 2", o.Livres, o.EmEspera)
	}

	for i := 0; i < 3; i++ {
		situacao, err := rede.Confirmar(ctx, "vaijunto", confirmada)
		if err != nil || situacao != dominio.SituacaoConfirmado {
			t.Errorf("Confirmar %d: %q, %v; want CONFIRMADO, nil", i+1, situacao, err)
		}
		situacao, err = rede.Abortar(ctx, "vaijunto", abortada)
		if err != nil || situacao != dominio.SituacaoAbortado {
			t.Errorf("Abortar %d: %q, %v; want ABORTADO, nil", i+1, situacao, err)
		}
	}

	// O confirmado mantém o assento; o abortado o devolveu. Uma vez só, por
	// mais repetições que tenham chegado.
	o := dono.Ocupacao()[0]
	if o.Livres != 1 || o.Confirmados != 1 || o.EmEspera != 0 {
		t.Errorf("ocupação = %+v, want livres 1, confirmados 1, em espera 0", o)
	}
	conferirIP21Em(t, consorcio)
}

// Camada 1 da seção 8.3, com a transação que chega depois sendo a **mais
// velha**: ela não preempta quem já está com o assento — espera —, e leva o
// assento quando o portador aborta.
//
// É o par do teste seguinte, e juntos eles são a camada 1 inteira: quem leva o
// assento é quem chegou primeiro ao mutex do dono, e o carimbo decide apenas o
// que acontece com quem chegou depois.
func TestAbortLiberaOAssentoParaAMaisVelhaQueEsperou(t *testing.T) {
	consorcio := subirConsorcioDeTransacoes(t, map[string][]byte{
		"vaijunto":     caronasDe("vaijunto", 1),
		"rotacapixaba": caronasDe("rotacapixaba", 1),
	}, "")
	rede := consorcio["rotacapixaba"].rede
	ctx := context.Background()

	portadora := pedidoComCarimbo("rotacapixaba", "rotacapixaba:res-00000001", "vaijunto:car-1",
		dominio.Carimbo{Lamport: 9, Empresa: "rotacapixaba"})
	maisVelha := pedidoComCarimbo("rotacapixaba", "rotacapixaba:res-00000002", "vaijunto:car-1",
		dominio.Carimbo{Lamport: 5, Empresa: "rotacapixaba"})

	if _, err := rede.Preparar(ctx, "vaijunto", portadora); err != nil {
		t.Fatalf("Preparar da portadora: %v", err)
	}

	voto, err := rede.Preparar(ctx, "vaijunto", maisVelha)
	if err != nil {
		t.Fatalf("Preparar da mais velha: %v", err)
	}
	if voto.Voto != transacao.VotoAguarde {
		t.Fatalf("voto da mais velha = %+v, want AGUARDE", voto)
	}
	// O AGUARDE não grava nada (seção 8.2): o assento continua com a portadora.
	if o := consorcio["vaijunto"].estado.Ocupacao()[0]; o.Livres != 0 || o.EmEspera != 1 {
		t.Errorf("após o AGUARDE: livres %d e em espera %d, want 0 e 1", o.Livres, o.EmEspera)
	}

	if _, err := rede.Abortar(ctx, "vaijunto", portadora.TxID); err != nil {
		t.Fatalf("Abortar: %v", err)
	}

	voto, err = rede.Preparar(ctx, "vaijunto", maisVelha)
	if err != nil {
		t.Fatalf("Preparar após o abort: %v", err)
	}
	if voto.Voto != transacao.VotoSim {
		t.Errorf("voto após o abort = %+v, want SIM", voto)
	}
	conferirIP21Em(t, consorcio)
}

// Camada 1 da seção 8.3, com a transação que chega depois sendo a **mais
// nova**: ela desiste na hora, com PRECEDENCIA, e o coordenador tenta de novo
// com o mesmo carimbo (D36).
//
// Desistir na hora é o que evita o deadlock: se as duas esperassem, e cada uma
// fosse portadora de um assento de que a outra precisa, nenhuma das duas
// sairia do lugar (seção 8.5).
func TestMaisNovaDesisteNaHoraComPrecedencia(t *testing.T) {
	consorcio := subirConsorcioDeTransacoes(t, map[string][]byte{
		"vaijunto":     caronasDe("vaijunto", 1),
		"rotacapixaba": caronasDe("rotacapixaba", 1),
	}, "")
	rede := consorcio["rotacapixaba"].rede
	ctx := context.Background()

	portadora := pedidoComCarimbo("rotacapixaba", "rotacapixaba:res-00000001", "vaijunto:car-1",
		dominio.Carimbo{Lamport: 5, Empresa: "rotacapixaba"})
	maisNova := pedidoComCarimbo("rotacapixaba", "rotacapixaba:res-00000002", "vaijunto:car-1",
		dominio.Carimbo{Lamport: 9, Empresa: "rotacapixaba"})

	if _, err := rede.Preparar(ctx, "vaijunto", portadora); err != nil {
		t.Fatalf("Preparar da portadora: %v", err)
	}
	voto, err := rede.Preparar(ctx, "vaijunto", maisNova)
	if err != nil {
		t.Fatalf("Preparar da mais nova: %v", err)
	}
	if voto.Voto != transacao.VotoNao || voto.Motivo != transacao.MotivoPrecedencia {
		t.Fatalf("voto da mais nova = %+v, want NAO/PRECEDENCIA", voto)
	}
	if voto.CaronaID != "vaijunto:car-1" || voto.Trecho != 0 {
		t.Errorf("voto = %+v, want carona vaijunto:car-1 e trecho 0 (seção 4.3)", voto)
	}
	// A recusa não gravou nada e não mexeu no assento da portadora.
	if o := consorcio["vaijunto"].estado.Ocupacao()[0]; o.Livres != 0 || o.EmEspera != 1 {
		t.Errorf("após a PRECEDENCIA: livres %d e em espera %d, want 0 e 1", o.Livres, o.EmEspera)
	}
	if _, existe := consorcio["vaijunto"].estado.Participacao(maisNova.TxID); existe {
		t.Errorf("a PRECEDENCIA criou participação")
	}
	conferirIP21Em(t, consorcio)
}

// Empate no número de Lamport: o desempate é pelo identificador da empresa em
// ordem alfabética (seção 8.1), e é o que torna a ordem **total**.
//
// Sem ordem total, duas transações com o mesmo número esperariam uma pela
// outra em donos diferentes, que é exatamente o deadlock que o wait-die existe
// para evitar.
func TestEmpateDeLamportDesempataPelaEmpresa(t *testing.T) {
	// Ordem alfabética: rotacapixaba < rotafluminense. Quem chega depois é a
	// segunda; o que muda entre os casos é de que lado do desempate ela está.
	for _, caso := range []struct {
		nome       string
		portadora  string
		segunda    string
		querVoto   transacao.Voto
		querMotivo string
	}{
		{"a que chega depois é mais velha", "rotafluminense", "rotacapixaba", transacao.VotoAguarde, ""},
		{"a que chega depois é mais nova", "rotacapixaba", "rotafluminense", transacao.VotoNao, transacao.MotivoPrecedencia},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			consorcio := subirConsorcioDeTransacoes(t, map[string][]byte{
				"vaijunto":       caronasDe("vaijunto", 1),
				"rotacapixaba":   caronasDe("rotacapixaba", 1),
				"rotafluminense": caronasDe("rotafluminense", 1),
			}, "")
			ctx := context.Background()

			// O mesmo número de Lamport nas duas: só o nome da empresa separa.
			const mesmoLamport = 7

			portadora := pedidoComCarimbo(caso.portadora, caso.portadora+":res-00000001", "vaijunto:car-1",
				dominio.Carimbo{Lamport: mesmoLamport, Empresa: caso.portadora})
			if _, err := consorcio[caso.portadora].rede.Preparar(ctx, "vaijunto", portadora); err != nil {
				t.Fatalf("Preparar da portadora: %v", err)
			}

			segunda := pedidoComCarimbo(caso.segunda, caso.segunda+":res-00000002", "vaijunto:car-1",
				dominio.Carimbo{Lamport: mesmoLamport, Empresa: caso.segunda})
			voto, err := consorcio[caso.segunda].rede.Preparar(ctx, "vaijunto", segunda)
			if err != nil {
				t.Fatalf("Preparar da segunda: %v", err)
			}
			if voto.Voto != caso.querVoto || voto.Motivo != caso.querMotivo {
				t.Errorf("voto = %+v, want %q %q", voto, caso.querVoto, caso.querMotivo)
			}
			conferirIP21Em(t, consorcio)
		})
	}
}

// Participante de si mesma: a mesma empresa como coordenadora e dona passa pelo
// mesmo código, sem rede (seção 7.1, passo 5), e produz o mesmo resultado que a
// chamada de um par.
func TestParticipanteDeSiMesmaUsaOMesmoCodigo(t *testing.T) {
	consorcio := subirConsorcioDeTransacoes(t, map[string][]byte{
		"vaijunto":     caronasDe("vaijunto", 2),
		"rotacapixaba": caronasDe("rotacapixaba", 2),
	}, "")
	vaijunto := consorcio["vaijunto"]

	local := pedidoPara("vaijunto", "vaijunto:res-00000001", "vaijunto:car-1")
	local.Participantes = []string{"vaijunto"}
	pernasLocais, err := vaijunto.participante.Preparar(local)
	if err != nil {
		t.Fatalf("Preparar local: %v", err)
	}

	remoto, err := consorcio["rotacapixaba"].rede.Preparar(context.Background(), "vaijunto",
		pedidoPara("rotacapixaba", "rotacapixaba:res-00000002", "vaijunto:car-1"))
	if err != nil {
		t.Fatalf("Preparar remoto: %v", err)
	}

	// As pernas são as mesmas: é o mesmo dono afirmando sobre a mesma carona.
	if fmt.Sprint(pernasLocais) != fmt.Sprint(remoto.Pernas) {
		t.Errorf("pernas locais %v, remotas %v", pernasLocais, remoto.Pernas)
	}
	// E as duas participações existem no mesmo lugar, com o coordenador certo.
	for tx, coordenador := range map[string]string{
		"vaijunto:res-00000001":     "vaijunto",
		"rotacapixaba:res-00000002": "rotacapixaba",
	} {
		pt, existe := vaijunto.estado.Participacao(tx)
		if !existe || pt.Coordenador != coordenador {
			t.Errorf("participação %s = %+v (existe=%t), want coordenador %s", tx, pt, existe, coordenador)
		}
	}
	if o := vaijunto.estado.Ocupacao()[0]; o.Livres != 0 || o.EmEspera != 2 {
		t.Errorf("ocupação = %+v, want livres 0 e em espera 2", o)
	}
	conferirIP21Em(t, consorcio)
}

// Reinício com o arquivo real: participações e contadores voltam do log, e o
// estado reconstruído é indistinguível do que caiu (D22, D37).
func TestReinicioPreservaParticipacoesEContadores(t *testing.T) {
	dirLogs := t.TempDir()
	antes := subirConsorcioDeTransacoes(t, map[string][]byte{
		"vaijunto":     caronasDe("vaijunto", 3),
		"rotacapixaba": caronasDe("rotacapixaba", 3),
	}, dirLogs)
	rede := antes["rotacapixaba"].rede
	ctx := context.Background()

	confirmada := "rotacapixaba:res-00000001"
	emEspera := "rotacapixaba:res-00000002"
	vetada := "rotacapixaba:res-00000003"
	for _, tx := range []string{confirmada, emEspera} {
		if _, err := rede.Preparar(ctx, "vaijunto", pedidoPara("rotacapixaba", tx, "vaijunto:car-1")); err != nil {
			t.Fatalf("Preparar %s: %v", tx, err)
		}
	}
	if _, err := rede.Confirmar(ctx, "vaijunto", confirmada); err != nil {
		t.Fatalf("Confirmar: %v", err)
	}
	// Veto (D38): abortar transação desconhecida grava ABORTADO sem itens.
	if _, err := rede.Abortar(ctx, "vaijunto", vetada); err != nil {
		t.Fatalf("Abortar: %v", err)
	}

	ocupacaoAntes := fmt.Sprint(antes["vaijunto"].estado.Ocupacao())
	participacoesAntes := antes["vaijunto"].estado.Participacoes()

	// "Derrubar" a empresa: fecha o arquivo e abandona o estado.
	if err := antes["vaijunto"].wal.Fechar(); err != nil {
		t.Fatalf("fechar o log: %v", err)
	}

	usuarios, err := os.ReadFile(filepath.Join(diretorioSemente, "usuarios.json"))
	if err != nil {
		t.Fatalf("ler usuários: %v", err)
	}
	depois, err := dominio.CarregarEstado("vaijunto", usuarios, caronasDe("vaijunto", 3))
	if err != nil {
		t.Fatalf("carga inicial: %v", err)
	}
	arquivo := instalarWAL(t, depois, filepath.Join(dirLogs, "vaijunto.wal"))
	defer arquivo.Fechar()

	if got := fmt.Sprint(depois.Ocupacao()); got != ocupacaoAntes {
		t.Errorf("ocupação após o reinício:\n%s\nwant\n%s", got, ocupacaoAntes)
	}
	volta := depois.Participacoes()
	if len(volta) != len(participacoesAntes) {
		t.Fatalf("participações = %d, want %d", len(volta), len(participacoesAntes))
	}
	for i, pt := range volta {
		original := participacoesAntes[i]
		// PreparadaEm não vai ao log: o prazo de dúvida recomeça no boot
		// (seção 4.4).
		original.PreparadaEm = time.Time{}
		if fmt.Sprint(pt) != fmt.Sprint(original) {
			t.Errorf("participação %d = %+v, want %+v", i, pt, original)
		}
	}
	// O veto voltou como participação abortada sem itens.
	veto, existe := depois.Participacao(vetada)
	if !existe || veto.Situacao != dominio.SituacaoAbortado || len(veto.Itens) != 0 {
		t.Errorf("veto = %+v (existe=%t), want ABORTADO sem itens", veto, existe)
	}
}

// A queda de um par é detectada por timeout e não é erro fatal (D30): quem
// chama recebe ErrParIndisponivel e decide o caminho seguro.
func TestParForaDoArViraParIndisponivel(t *testing.T) {
	consorcio := subirConsorcioDeTransacoes(t, map[string][]byte{
		"vaijunto":     caronasDe("vaijunto", 1),
		"rotacapixaba": caronasDe("rotacapixaba", 1),
	}, "")

	if err := consorcio["vaijunto"].rest.Fechar(); err != nil {
		t.Fatalf("fechar a porta dos pares: %v", err)
	}

	_, err := consorcio["rotacapixaba"].rede.Preparar(context.Background(), "vaijunto",
		pedidoPara("rotacapixaba", "rotacapixaba:res-00000001", "vaijunto:car-1"))
	if !errors.Is(err, federacao.ErrParIndisponivel) {
		t.Errorf("err = %v, want ErrParIndisponivel", err)
	}
}
