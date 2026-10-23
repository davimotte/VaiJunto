package transacao

import (
	"context"
	"sort"
	"sync"
	"time"

	"vaijunto/internal/dominio"
)

// Terminador é a terminação cooperativa do 2PC (D38; PROJETO_P2.md, seção
// 9.4): o caminho de volta de um assento que ficou em espera porque a decisão
// nunca chegou.
//
// É a peça que torna a RD04 verdadeira. Sem ela, uma participação em PREPARADO
// só sai desse estado por ordem do coordenador; se o coordenador cai depois de
// mandar o PREPARAR e antes de mandar a decisão, o assento fica descontado
// enquanto o processo viver, e nem o boot o devolve — porque o PREPARADO está
// no log e o replay o reaplica fielmente. Com ela, todo PREPARADO tem um prazo
// (D41) e, vencido o prazo, um procedimento definido para descobrir o destino
// da transação.
//
// É camada 5, como o Coordenador, e vale aqui a mesma regra: é a única que
// conhece prazos e retransmissões, e nunca espera segurando o lock da camada 7
// (regra 2). Toda a E/S acontece sobre cópias que o estado já devolveu.
//
// Quem **não** decide nada aqui é o participante sozinho. O passo 3 do
// argumento da seção 7.4 lista as três únicas evidências que autorizam abortar
// um PREPARADO, e a rodada abaixo não produz nenhuma outra.
type Terminador struct {
	empresa  string
	estado   *dominio.Estado
	rede     Rede
	registro RegistroDecisao

	prazoDuvida   time.Duration
	prazoConsulta time.Duration

	// base encerra o varredor e as rodadas em voo. Ver Parar.
	base     context.Context
	encerrar context.CancelFunc

	// mu protege rodando, e nada tem a ver com o mutex do estado (D04): aqui
	// não há estado de domínio nenhum, só a contabilidade de quais transações
	// já têm uma rodada em voo.
	mu      sync.Mutex
	rodando map[string]bool
}

// ConfigTerminador são as peças que o boot liga ao terminador.
type ConfigTerminador struct {
	Empresa  string
	Estado   *dominio.Estado
	Rede     Rede // pares; nos testes, a rede em memória
	Registro RegistroDecisao

	// PrazoDuvida é quanto tempo uma participação fica em PREPARADO antes de
	// a primeira consulta sair (D41: 10 s).
	PrazoDuvida time.Duration
	// PrazoConsulta é o intervalo entre as consultas de quem continua em
	// dúvida (D41: 3 s). É também o tique do varredor e o teto de cada
	// chamada, como o PrazoFase2 é para o coordenador.
	PrazoConsulta time.Duration
}

// Prazos padrão da D41, usados quando a Config os deixa em zero. Ficam aqui,
// e não só como valor de flag, para que um Terminador montado num teste sem
// configurar nada já se comporte como o de produção.
//
// A relação entre os dois valores e os do coordenador é intencional: a dúvida
// (10 s) é maior que a votação (5 s) somada a uma chamada (2 s), de modo que,
// no caso normal, a decisão chega antes de alguém precisar perguntar. Consulta
// nenhuma deve acontecer num sistema saudável.
const (
	prazoDuvidaPadrao   = 10 * time.Second
	prazoConsultaPadrao = 3 * time.Second
)

func NovoTerminador(cfg ConfigTerminador) *Terminador {
	t := &Terminador{
		empresa:       cfg.Empresa,
		estado:        cfg.Estado,
		rede:          cfg.Rede,
		registro:      cfg.Registro,
		prazoDuvida:   cfg.PrazoDuvida,
		prazoConsulta: cfg.PrazoConsulta,
		rodando:       make(map[string]bool),
	}
	if t.prazoDuvida <= 0 {
		t.prazoDuvida = prazoDuvidaPadrao
	}
	if t.prazoConsulta <= 0 {
		t.prazoConsulta = prazoConsultaPadrao
	}
	t.base, t.encerrar = context.WithCancel(context.Background())
	return t
}

// Parar encerra o varredor e as rodadas em voo, como Coordenador.Parar e pelo
// mesmo motivo: em produção o servidor não tem rotina de desligamento (seção
// 9.1), mas num `go test -race -count=20` uma goroutine que acorda a cada
// tique continuaria batendo em servidores já fechados depois do fim do teste.
func (t *Terminador) Parar() { t.encerrar() }

// Recuperar executa a primeira tarefa da seção 9.3 e sobe o varredor. O boot a
// chama depois do replay e depois de abrir as portas (seção 9.2, passo 5).
//
// Armar o prazo é obrigatório, e não um detalhe: PreparadaEm não vai ao log
// (seção 4.4), então toda participação que veio do replay chega com o instante
// zerado. Sem isto, o varredor não a enxergaria nunca, e o assento dela ficaria
// preso para sempre — o oposto do que esta fase existe para garantir.
//
// Devolve quantas participações encontrou em dúvida, para a linha de registro
// do boot.
func (t *Terminador) Recuperar() int {
	// time.Now() cru, sem In(): este instante nunca é exibido, só comparado
	// com um prazo, e In() descartaria a leitura monotônica (D41, seção 9.4).
	n := t.estado.ArmarPrazosDeDuvida(time.Now())
	go t.varrer()
	return n
}

// varrer é o relógio da terminação: a cada PrazoConsulta, pergunta ao estado
// quem está em dúvida e dispara uma rodada por transação.
//
// É um varredor só por servidor, e não um temporizador por participação, por
// duas razões. A primeira é que o gatilho já está no estado: dominio.preparar
// grava PreparadaEm, e o boot rearma o que o replay zerou, então não é preciso
// gancho nenhum no caminho do voto. A segunda é que a repetição "de 3 em 3 s"
// da D41 cai de graça do tique: uma participação que continua em PREPARADO
// simplesmente reaparece na varredura seguinte.
func (t *Terminador) varrer() {
	tique := time.NewTicker(t.prazoConsulta)
	defer tique.Stop()
	for {
		select {
		case <-t.base.Done():
			return
		case <-tique.C:
			for _, pt := range t.estado.ParticipacoesEmDuvida(time.Now(), t.prazoDuvida) {
				if t.comecar(pt.TxID) {
					go t.rodada(pt)
				}
			}
		}
	}
}

// comecar reserva a rodada de uma transação e responde se ela pode ir.
//
// Existe porque uma rodada faz E/S e pode demorar mais que um tique: sem a
// marca, o varredor empilharia rodadas da mesma transação, e várias
// goroutines perguntariam a mesma coisa aos mesmos pares ao mesmo tempo.
func (t *Terminador) comecar(txID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rodando[txID] {
		return false
	}
	t.rodando[txID] = true
	return true
}

func (t *Terminador) terminar(txID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.rodando, txID)
}

// rodada é uma passada do procedimento da seção 9.4 sobre uma participação em
// dúvida. Não decide nada sozinha: ou encontra evidência conclusiva e aplica,
// ou volta sem fazer nada e o próximo tique tenta de novo.
//
// Nenhuma chamada daqui acontece com o lock do estado preso: pt é uma cópia que
// o estado já devolveu, e as escritas no fim são chamadas novas à camada 7.
func (t *Terminador) rodada(pt dominio.Participacao) {
	defer t.terminar(pt.TxID)

	coordenador, err := dominio.EmpresaDoID(pt.TxID)
	if err != nil {
		// txID sem prefixo não chega aqui: ele foi validado quando a
		// participação nasceu. Se chegar, não há a quem perguntar, e inventar
		// uma decisão seria pior que esperar.
		return
	}

	// Passo 1 — o coordenador. É a fonte autoritativa: ele é o único que sabe
	// se a decisão foi gravada.
	resposta, err := t.consultar(coordenador, pt.TxID)
	if err == nil && resposta.Papel == PapelCoordenador {
		switch resposta.Decisao {
		case dominio.DecisaoConfirmada:
			t.aplicarConfirmar(pt.TxID, "coordenador")
		case dominio.DecisaoAbortada:
			t.aplicarAbortar(pt.TxID, "coordenador")
		default:
			// EM_ANDAMENTO: a votação ainda está de pé, e perguntar de novo é
			// tudo o que se pode fazer. Abortar aqui seria desfazer uma
			// transação que ainda pode confirmar.
		}
		return
	}

	// Passo 2 — sem resposta do coordenador, os demais participantes, cuja
	// lista veio no PREPARAR (D38).
	//
	// Esta empresa fica de fora porque a resposta dela é conhecida — é ela que
	// está em dúvida —, e o coordenador também, porque acabou de não responder.
	for _, empresa := range outrosParticipantes(pt.Participantes, t.empresa, coordenador) {
		resposta, err := t.consultar(empresa, pt.TxID)
		if err != nil || resposta.Papel != PapelParticipante {
			continue
		}
		switch resposta.Situacao {
		case string(dominio.SituacaoConfirmado), string(dominio.SituacaoCancelado):
			// Outro dono já está CONFIRMADO. Pelo passo 1 do argumento da
			// seção 7.4, ele só chegou lá porque houve decisão de confirmar
			// gravada, então confirmar aqui não pode contradizer ninguém.
			t.aplicarConfirmar(pt.TxID, empresa)
			return
		case string(dominio.SituacaoAbortado), SituacaoDesconhecida:
			// Evidência de que a unanimidade é impossível: ou aquele dono já
			// abortou, ou acabou de vetar ao ser consultado sobre uma
			// transação que não conhece. Nos dois casos o coordenador nunca
			// obterá todos os votos SIM, e abortar aqui é seguro.
			t.aplicarAbortar(pt.TxID, empresa)
			return
		default:
			// PREPARADO: tão em dúvida quanto nós. Continua perguntando aos
			// outros.
		}
	}

	// Ninguém soube dizer. É o bloqueio residual da seção 9.6 quando todos
	// estão em PREPARADO e o coordenador está fora: nenhuma escolha unilateral
	// seria segura, então continuamos em dúvida e o próximo tique repete.
}

// consultar pergunta a uma empresa, pela rede ou localmente.
//
// Quando a empresa é a própria — o caso de a empresa do passageiro também ser
// dona de um trecho (D21) —, a resposta sai do estado direto, sem HTTP contra
// si mesma e sem poder falhar. É o espelho do que o coordenador faz com o
// votante local.
//
// O prazo de cada chamada é o PrazoConsulta, como o coordenador usa o
// PrazoFase2 na fase 2. O PrazoChamada do cliente HTTP continua valendo por
// baixo: context.WithTimeout sobre um ctx que já tem prazo mantém o mais
// apertado.
func (t *Terminador) consultar(destino, txID string) (RespostaConsulta, error) {
	if destino == t.empresa {
		return RespostaConsulta{
			Papel:   PapelCoordenador,
			Decisao: t.estado.DecisaoDaTransacao(txID),
		}, nil
	}
	ctx, cancelar := context.WithTimeout(t.base, t.prazoConsulta)
	defer cancelar()
	return t.rede.Consultar(ctx, destino, txID)
}

// aplicarConfirmar e aplicarAbortar são as duas únicas escritas da
// terminação, cada uma pela porta normal do estado, com o mesmo fsync e a
// mesma idempotência de quando a ordem vem do coordenador.
//
// Um erro aqui é quase sempre corrida benigna: a decisão de verdade chegou
// entre a nossa consulta e a nossa escrita, e o estado já está no destino
// certo. Vale registrar e seguir — insistir só repetiria a mesma transição
// impossível.
func (t *Terminador) aplicarConfirmar(txID, fonte string) {
	if _, err := t.estado.Confirmar(txID); err != nil {
		t.anotar(txID, "TERMINACAO-CONFIRMAR-RECUSADA por "+fonte)
		return
	}
	t.anotar(txID, "TERMINACAO → CONFIRMADO por "+fonte)
}

func (t *Terminador) aplicarAbortar(txID, fonte string) {
	if _, err := t.estado.Abortar(txID); err != nil {
		t.anotar(txID, "TERMINACAO-ABORTAR-RECUSADA por "+fonte)
		return
	}
	t.anotar(txID, "TERMINACAO → ABORTADO por "+fonte)
}

func (t *Terminador) anotar(txID, decisao string) {
	if t.registro != nil {
		t.registro.Decisao(txID, decisao)
	}
}

// outrosParticipantes são as empresas a quem ainda vale a pena perguntar: a
// lista do PREPARAR menos esta empresa e menos o coordenador.
//
// Ordenada para que duas execuções do mesmo cenário perguntem na mesma ordem,
// o que torna o registro de operações comparável e o teste estável.
func outrosParticipantes(participantes []string, propria, coordenador string) []string {
	lista := make([]string, 0, len(participantes))
	for _, empresa := range participantes {
		if empresa != propria && empresa != coordenador {
			lista = append(lista, empresa)
		}
	}
	sort.Strings(lista)
	return lista
}
