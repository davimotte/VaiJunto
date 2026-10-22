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
	"vaijunto/internal/persistencia"
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

// prazoNosTestes é o prazo de cada chamada a um par (D41) encurtado para os
// testes, mas largo o bastante para 50 chamadas simultâneas num processo
// carregado pelo -race.
const prazoNosTestes = 2 * time.Second

// empresaDeTransacoes é uma empresa do consórcio com a porta dos pares no ar e
// o participante do 2PC montado, como no boot (cmd/servidor/main.go).
type empresaDeTransacoes struct {
	nome         string
	estado       *dominio.Estado
	participante *transacao.Participante
	rede         transacao.Rede // o cliente HTTP desta empresa
	rest         *federacao.Servidor
	pares        map[string]string
	registro     *saidaSegura
	wal          *persistencia.Arquivo // nil quando o log é só em memória
}

// subirConsorcioDeTransacoes sobe uma empresa por entrada de caronasPorEmpresa,
// com estado, participante, porta HTTP e cliente dos pares.
//
// É o subirConsorcioDeBusca sem a porta TCP: esta fase não mexe no RESERVAR do
// cliente, e abrir uma porta que nenhum teste usa só tornaria a falha mais
// difícil de ler.
//
// dirLogs vazio deixa o log em memória. Com diretório, cada empresa ganha o seu
// arquivo e passa pelo caminho completo do boot — semente, abrir, replay e só
// então instalar o arquivo (D37) —, que é o que o teste de reinício precisa.
//
// Os endereços HTTP só existem depois do Escutar com porta 0, e por isso as
// listas de pares são preenchidas depois, antes de qualquer Servir: assim
// nenhuma goroutine lê o mapa enquanto ele é escrito.
func subirConsorcioDeTransacoes(t *testing.T, caronasPorEmpresa map[string][]byte, dirLogs string) map[string]*empresaDeTransacoes {
	t.Helper()

	usuarios, err := os.ReadFile(filepath.Join(diretorioSemente, "usuarios.json"))
	if err != nil {
		t.Fatalf("ler usuários do fixture: %v", err)
	}

	consorcio := make(map[string]*empresaDeTransacoes, len(caronasPorEmpresa))
	configs := make(map[string]federacao.Config, len(caronasPorEmpresa))

	for nome, caronas := range caronasPorEmpresa {
		estado, err := dominio.CarregarEstado(nome, usuarios, caronas)
		if err != nil {
			t.Fatalf("empresa %s: carga inicial: %v", nome, err)
		}

		e := &empresaDeTransacoes{
			nome:         nome,
			estado:       estado,
			participante: transacao.NovoParticipante(estado),
			pares:        make(map[string]string),
			registro:     &saidaSegura{},
		}
		if dirLogs != "" {
			e.wal = instalarWAL(t, estado, filepath.Join(dirLogs, nome+".wal"))
		}

		cfg := federacao.Config{
			Empresa:      nome,
			Pares:        e.pares,
			Relogio:      transacao.NovoRelogio(),
			Registro:     federacao.NovoRegistro(e.registro, nome),
			Estado:       estado,
			Participante: e.participante,
			PrazoChamada: prazoNosTestes,
			PrazoBusca:   prazoNosTestes,
		}

		rest, err := federacao.Escutar("127.0.0.1:0", cfg)
		if err != nil {
			t.Fatalf("empresa %s: escutar porta dos pares: %v", nome, err)
		}
		e.rest = rest
		consorcio[nome] = e
		configs[nome] = cfg
	}

	for nome, e := range consorcio {
		for outro, o := range consorcio {
			if outro != nome {
				e.pares[outro] = "http://" + o.rest.Endereco()
			}
		}

		rest := e.rest
		go func() { _ = rest.Servir() }()
		t.Cleanup(func() { _ = rest.Fechar() })

		e.rede = federacao.NovoCliente(configs[nome])
	}
	return consorcio
}

// instalarWAL repete o caminho do boot: abre o arquivo, reaplica o que está
// nele e só então o instala como destino do log, para que a reaplicação nunca
// grave de volta o que está lendo (D37).
func instalarWAL(t *testing.T, estado *dominio.Estado, caminho string) *persistencia.Arquivo {
	t.Helper()
	arquivo, registros, err := persistencia.Abrir(caminho)
	if err != nil {
		t.Fatalf("abrir %s: %v", caminho, err)
	}
	if _, err := estado.Reaplicar(registros); err != nil {
		t.Fatalf("replay de %s: %v", caminho, err)
	}
	estado.UsarLog(arquivo)
	return arquivo
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
	return dominio.PedidoPreparacao{
		TxID:          txID,
		Coordenador:   coordenador,
		Participantes: []string{coordenador, "vaijunto"},
		Carimbo:       dominio.Carimbo{Lamport: 41, Empresa: coordenador},
		Passageiro:    dominio.Passageiro{Ref: coordenador + ":maria", Nome: "Maria Souza"},
		Itens:         []dominio.ItemReserva{{CaronaID: caronaID, De: 0, Ate: 1}},
	}
}

// conferirIP21Em verifica a invariante mestra em todas as empresas do consórcio
// (seção 11.1).
func conferirIP21Em(t *testing.T, consorcio map[string]*empresaDeTransacoes) {
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
			pedido := pedidoPara(tent.coordenador, tx, "vaijunto:car-1")
			prontas.Done()
			<-largada

			if !tent.pelaRede {
				// Participante de si mesma: mesmo código, sem rede.
				_, err := consorcio[tent.coordenador].participante.Preparar(pedido)
				switch {
				case err == nil:
					votos[i] = transacao.VotoSim
				case errors.Is(err, dominio.ErrSemAssento):
					votos[i], motivos[i] = transacao.VotoNao, "SEM_ASSENTO"
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

	sins, naos := 0, 0
	for i := range tentativas {
		switch {
		case erros[i] != nil:
			t.Errorf("tentativa %d (%s): %v", i, tentativas[i].coordenador, erros[i])
		case votos[i] == transacao.VotoSim:
			sins++
		case votos[i] == transacao.VotoNao && motivos[i] == "SEM_ASSENTO":
			naos++
		default:
			t.Errorf("tentativa %d: voto %q motivo %q", i, votos[i], motivos[i])
		}
	}

	if sins != 1 {
		t.Errorf("votos SIM = %d, want exatamente 1", sins)
	}
	if naos != len(tentativas)-1 {
		t.Errorf("votos NÃO por falta de assento = %d, want %d", naos, len(tentativas)-1)
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

// O assento liberado por um abort volta ao mercado: o PREPARAR seguinte, que
// antes recebia NÃO, passa a receber SIM. É a espera do wait-die funcionando
// pela camada 1 (ordem de chegada no dono), antes de a fase 7 acrescentar a
// precedência por Lamport.
func TestAbortLiberaOAssentoParaOProximo(t *testing.T) {
	consorcio := subirConsorcioDeTransacoes(t, map[string][]byte{
		"vaijunto":     caronasDe("vaijunto", 1),
		"rotacapixaba": caronasDe("rotacapixaba", 1),
	}, "")
	rede := consorcio["rotacapixaba"].rede
	ctx := context.Background()

	primeira := pedidoPara("rotacapixaba", "rotacapixaba:res-00000001", "vaijunto:car-1")
	segunda := pedidoPara("rotacapixaba", "rotacapixaba:res-00000002", "vaijunto:car-1")

	if _, err := rede.Preparar(ctx, "vaijunto", primeira); err != nil {
		t.Fatalf("primeiro Preparar: %v", err)
	}
	voto, err := rede.Preparar(ctx, "vaijunto", segunda)
	if err != nil {
		t.Fatalf("segundo Preparar: %v", err)
	}
	if voto.Voto != transacao.VotoNao || voto.Motivo != "SEM_ASSENTO" {
		t.Fatalf("segundo voto = %+v, want NAO/SEM_ASSENTO", voto)
	}

	if _, err := rede.Abortar(ctx, "vaijunto", primeira.TxID); err != nil {
		t.Fatalf("Abortar: %v", err)
	}

	voto, err = rede.Preparar(ctx, "vaijunto", segunda)
	if err != nil {
		t.Fatalf("Preparar após o abort: %v", err)
	}
	if voto.Voto != transacao.VotoSim {
		t.Errorf("voto após o abort = %+v, want SIM", voto)
	}
	conferirIP21Em(t, consorcio)
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
