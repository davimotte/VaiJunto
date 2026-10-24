package dominio

import (
	"fmt"
	"sort"
	"time"
)

// Participante do two-phase commit, pelo lado do dono dos assentos (D21;
// PROJETO_P2.md, seções 4.4 e 7.2).
//
// Como em caronas.go e reservas.go, nada aqui adquire o mutex: estas funções
// recebem o estado já travado por estado.go (D05). É o que torna verdadeira a
// afirmação da seção 7.5 — dois PREPARAR pelo último assento são serializados
// pelo mutex do único dono daquela carona, e só o primeiro encontra
// Livres >= 1.
//
// A forma de preparar repete a de reservar: os passos 1 a 3 só leem e podem
// retornar erro a qualquer momento; o passo 4 só escreve e não pode mais
// falhar. Nenhum assento é descontado fora do passo 4, e nenhum voto NÃO grava
// coisa alguma.

// Situacao é a situação de uma participação (seção 4.4; seção 3.6 do
// API_CONSORCIO.md).
//
// Os valores são os mesmos que trafegam na API, e não há tradução na borda: a
// situação é um estado do domínio, descrito na máquina de estados da seção
// 7.3, e a API apenas o publica. Diferente dos motivos do voto NÃO, que são
// vocabulário de protocolo e por isso nascem na tabela de internal/federacao.
//
// DESCONHECIDA não entra aqui: ela é resposta da consultar (fase 6A) para uma
// transação que o servidor não conhecia, e não uma situação guardada.
type Situacao string

const (
	SituacaoPreparado  Situacao = "PREPARADO"
	SituacaoConfirmado Situacao = "CONFIRMADO"
	SituacaoAbortado   Situacao = "ABORTADO"
	SituacaoCancelado  Situacao = "CANCELADO"
)

// Decisao é o que o **coordenador** responde quando é consultado sobre uma
// transação (seção 9.4; seções 3.6 e 4.6 do API_CONSORCIO.md).
//
// Mora aqui, ao lado de Situacao, pelo mesmo motivo que ela: os valores são os
// que trafegam na API, e não há tradução na borda. A diferença entre as duas é
// de quem fala — Situacao é a visão do dono dos assentos sobre a sua
// participação, Decisao é a visão do coordenador sobre o destino da transação
// inteira. Um servidor que seja os dois ao mesmo tempo responde pelo papel que
// o prefixo do txID lhe dá (seção 4.6 da API).
type Decisao string

const (
	DecisaoConfirmada  Decisao = "CONFIRMADA"
	DecisaoEmAndamento Decisao = "EM_ANDAMENTO"
	DecisaoAbortada    Decisao = "ABORTADA"
)

// Carimbo é a prioridade de uma transação na disputa por assentos (D24,
// seção 8.1).
//
// Mora no domínio, e não na camada de transação, porque é campo da
// participação e porque é sob o lock que o wait-die compara carimbos
// (decidirDisputa, seção 8.2). O *relógio* de Lamport continua em
// internal/transacao: o domínio guarda o carimbo que recebeu, nunca o produz.
type Carimbo struct {
	Lamport uint64
	Empresa string
}

// MaisVelhoQue responde se este carimbo precede o outro na ordem total da
// seção 8.1: primeiro pelo número, e em empate pelo identificador da empresa
// em ordem alfabética. Carimbo menor é transação mais velha.
//
// A ordem é total e igual em todos os servidores, e é isso que permite a dois
// donos diferentes decidirem a mesma disputa do mesmo jeito sem trocar
// mensagem nenhuma (seção 8.4).
func (c Carimbo) MaisVelhoQue(outro Carimbo) bool {
	if c.Lamport != outro.Lamport {
		return c.Lamport < outro.Lamport
	}
	return c.Empresa < outro.Empresa
}

// Passageiro é tudo o que um participante recebe e guarda sobre um passageiro
// de outra empresa (D32; seção 3.5 da API): a referência opaca
// "<empresa>:<usuario>" e o nome que o motorista vê no detalhe da carona
// (RF14). Senha e perfil nunca saem da empresa de origem.
type Passageiro struct {
	Ref  string
	Nome string
}

// Participacao é a visão que o dono das caronas tem de uma transação que usa
// assentos dele (seção 4.4). Quem a guarda é o participante; a Reserva da
// seção 4.5, guardada pelo coordenador, é outra coisa.
//
// Um veto (D38) é uma participação ABORTADO sem itens: ela existe para que um
// PREPARAR atrasado encontre a transação já abortada e receba NÃO.
type Participacao struct {
	TxID          string
	Coordenador   string
	Participantes []string // todas as empresas da transação, para a terminação (D38)
	Itens         []ItemReserva
	Carimbo       Carimbo
	Passageiro    Passageiro
	Situacao      Situacao

	// PreparadaEm arma o prazo de dúvida (D41). Não vai ao log: após um
	// reboot o prazo recomeça (seção 4.4), e por isso o replay a deixa zerada.
	// Nada a lê nesta fase; quem passa a agir sobre ela é a terminação
	// cooperativa da fase 6A.
	PreparadaEm time.Time
}

// copia devolve uma cópia profunda, pelo mesmo motivo de (*Carona).copia:
// nenhum ponteiro para dentro do estado pode escapar da seção crítica.
func (p *Participacao) copia() Participacao {
	copiada := *p
	copiada.Participantes = append([]string(nil), p.Participantes...)
	copiada.Itens = append([]ItemReserva(nil), p.Itens...)
	return copiada
}

// Perna são os dados autoritativos de uma perna, que o dono devolve junto com
// o voto SIM e o coordenador guarda na reserva (D34, D42; seção 3.3 da API).
//
// É tipo próprio, e não o PernaItinerario da busca, por duas razões:
//
//   - Cidades traz **todas** as paradas de De a Ate, inclusive as
//     intermediárias, porque é com elas que o coordenador verifica cidade
//     repetida no itinerário (D13). O PernaItinerario carrega só Origem e
//     Destino, que são a primeira e a última delas;
//   - os dois nascem em momentos diferentes: a perna autoritativa é o que o
//     dono **afirma** sobre a sua carona no voto, e o PernaItinerario é a
//     perna já resolvida para exibição ao passageiro.
//
// Acrescentar Cidades ao PernaItinerario tornaria Origem e Destino deriváveis
// dele, que é exatamente o tipo de duplicação que o projeto evita (PROJETO.md,
// seção 4).
type Perna struct {
	CaronaID      string
	Empresa       string
	MotoristaNome string
	De, Ate       int
	Cidades       []string // Rota[De..Ate], com as paradas intermediárias
	Partida       time.Time
	Chegada       time.Time
	PrecoCentavos int
}

// PedidoPreparacao é o que o coordenador envia no PREPARAR (seção 4.3 da API).
//
// Chega pela borda REST ou, quando a empresa do passageiro também é dona de
// algum trecho, por chamada direta do coordenador local (seção 7.1, passo 5).
// Os dois caminhos passam pela mesma validação, que é o motivo de ela viver
// aqui e não no handler.
type PedidoPreparacao struct {
	TxID          string
	Coordenador   string
	Participantes []string
	Carimbo       Carimbo
	Passageiro    Passageiro
	Itens         []ItemReserva
}

// OcupacaoTrecho é a conta da invariante mestra I-P2-1 (seção 11.1) para um
// trecho de uma carona: Livres + confirmados + em espera == Assentos, com
// Livres >= 0.
//
// Existe porque assentos em espera e participações não aparecem para os
// clientes, então o verificador do P1, que reconstruía as invariantes pelo
// protocolo, não basta mais (seção 11.2). A conta é feita sob o lock, em um
// passo só: lê-la em duas chamadas deixaria uma transação concorrente entrar
// no meio e acusar uma violação que nunca existiu.
//
// Confirmados soma as participações CONFIRMADO: com o 2PC, quem desconta
// assento é sempre o dono da carona, pela participação dele.
type OcupacaoTrecho struct {
	CaronaID    string
	Trecho      int
	Assentos    int
	Livres      int
	Confirmados int
	EmEspera    int
}

// preparar é a fase 1 do 2PC pelo lado do dono (seção 7.2).
func preparar(e *Estado, p PedidoPreparacao, agora time.Time) ([]Perna, error) {
	// Passo 1 — idempotência (D28). A mesma requisição recebida duas vezes
	// produz a mesma resposta, e é assim que o sistema lida com resposta
	// perdida e timeout: o coordenador repete sem medo.
	//
	// CONFIRMADO e CANCELADO também respondem SIM: os assentos continuam
	// descontados, e um PREPARAR que chega depois da fase 2 só pode ser uma
	// retransmissão atrasada da fase 1 que já foi aceita.
	if existente, repetido := e.participacoes[p.TxID]; repetido {
		if existente.Situacao == SituacaoAbortado {
			return nil, fmt.Errorf("%w: %q", ErrTransacaoAbortada, p.TxID)
		}
		return pernasDe(e, existente.Itens), nil
	}

	// Passo 2 — validar o que só este dono sabe (D34): as caronas são dele,
	// existem, não estão canceladas e os índices são válidos.
	caronas, err := validarPedido(e, p)
	if err != nil {
		return nil, err
	}

	// Passo 3 — disponibilidade e disputa, trecho a trecho (D25, D24).
	//
	// Se todo trecho pedido tem Livres >= 1, a lista sai vazia e seguimos
	// direto para o passo 4. Se não, o wait-die da seção 8.2 decide entre
	// esperar, desistir e recusar — e nenhuma das três respostas grava nada.
	var esgotados []trechoEsgotado
	for i, item := range p.Itens {
		c := caronas[i]
		for t := item.De; t < item.Ate; t++ {
			if c.Livres[t] >= 1 {
				continue
			}
			esgotados = append(esgotados, trechoEsgotado{
				carona:     c,
				trecho:     t,
				portadores: portadores(e, c.ID, t),
			})
		}
	}
	if err := decidirDisputa(p.Carimbo, esgotados); err != nil {
		return nil, err
	}

	// Passo 4 — escrever. D22, regra 3: o registro vai ao log com fsync antes
	// de qualquer mudança e antes de o voto SIM sair. Um SIM é uma promessa de
	// assento, e prometer sem ter gravado deixaria o servidor sem saber, ao
	// voltar de uma queda, o que ele prometeu aos pares (D02, revogada).
	//
	// É a última coisa que pode falhar; daqui para baixo nada retorna erro.
	if err := gravar(e, tipoPreparado, dadosPreparadoDe(p)); err != nil {
		return nil, err
	}

	for i, item := range p.Itens {
		c := caronas[i]
		for t := item.De; t < item.Ate; t++ {
			c.Livres[t]--
		}
	}

	nova := &Participacao{
		TxID:        p.TxID,
		Coordenador: p.Coordenador,
		// Copiados porque vêm decodificados do JSON de quem chamou: o estado
		// não compartilha memória com a borda.
		Participantes: append([]string(nil), p.Participantes...),
		Itens:         append([]ItemReserva(nil), p.Itens...),
		Carimbo:       p.Carimbo,
		Passageiro:    p.Passageiro,
		Situacao:      SituacaoPreparado,
		PreparadaEm:   agora,
	}
	e.participacoes[p.TxID] = nova

	return pernasDe(e, nova.Itens), nil
}

// trechoEsgotado é um trecho pedido que está sem assento livre, junto de quem
// está com ele em espera. É o insumo da tabela da seção 8.2, montado sob o lock
// no passo 3 do preparar.
type trechoEsgotado struct {
	carona     *Carona
	trecho     int
	portadores []*Participacao
}

// decidirDisputa aplica a tabela do wait-die (seção 8.2) aos trechos esgotados
// de um PREPARAR. Devolve nil quando não há disputa, isto é, quando a lista
// está vazia.
//
// É o **único** lugar do programa em que carimbos de transações diferentes são
// comparados (D24). Isso importa: dois donos distintos decidem a mesma disputa
// do mesmo jeito porque rodam esta mesma função sobre a mesma ordem total, sem
// trocar mensagem nenhuma (seção 8.4). Espalhar a comparação por mais de um
// lugar seria o caminho para dois donos discordarem e o sistema travar.
//
// As três regras, na ordem da tabela — e a ordem importa, porque um PREPARAR
// pode ter trechos esgotados de naturezas diferentes:
//
//  1. Algum trecho esgotado **sem portadores**: ele está cheio só de assentos
//     confirmados, e assento confirmado não volta (regra 4 do CLAUDE.md). Não
//     há o que esperar, e esperar pelos outros trechos seria esperar por um
//     assento que não viria de qualquer forma.
//  2. Nenhum portador de nenhum trecho esgotado é mais velho: AGUARDE.
//  3. Caso contrário: PRECEDENCIA.
//
// A comparação da regra 2 é `portador.MaisVelhoQue(meu)`, e não
// `meu.MaisVelhoQue(portador)`. A diferença está só no empate, e o empate
// existe: a D36 refaz a reserva com o **mesmo carimbo** e txID novo, enquanto o
// ABORTAR da tentativa anterior vai em segundo plano, então a nova tentativa
// pode encontrar a anterior ainda em PREPARADO, com carimbo idêntico. Pela
// comparação estrita ela desistiria de si mesma e queimaria as três tentativas;
// desta forma ela espera, o ABORTAR chega e a repetição de 200 ms leva o
// assento. Para duas transações diferentes o empate é impossível, porque a
// ordem de (lamport, empresa) é total.
func decidirDisputa(meu Carimbo, esgotados []trechoEsgotado) error {
	if len(esgotados) == 0 {
		return nil
	}

	for _, t := range esgotados {
		if len(t.portadores) == 0 {
			return &ErroSemAssento{
				CaronaID:     t.carona.ID,
				IndiceTrecho: t.trecho,
				Origem:       t.carona.Rota[t.trecho],
				Destino:      t.carona.Rota[t.trecho+1],
			}
		}
	}

	for _, t := range esgotados {
		for _, portador := range t.portadores {
			if portador.Carimbo.MaisVelhoQue(meu) {
				return &ErroPrecedencia{
					CaronaID:     t.carona.ID,
					IndiceTrecho: t.trecho,
					Origem:       t.carona.Rota[t.trecho],
					Destino:      t.carona.Rota[t.trecho+1],
				}
			}
		}
	}

	// Mais velha que todos: espera. O trecho informado é o primeiro esgotado,
	// como manda a seção 4.3 da API — serve ao diagnóstico e ao registro.
	primeiro := esgotados[0]
	return &ErroAguarde{
		CaronaID:     primeiro.carona.ID,
		IndiceTrecho: primeiro.trecho,
		Origem:       primeiro.carona.Rota[primeiro.trecho],
		Destino:      primeiro.carona.Rota[primeiro.trecho+1],
	}
}

// confirmar é a fase 2 pelo lado do dono, quando a decisão é confirmar
// (seção 7.2; seção 4.4 da API).
//
// Participação desconhecida ou ABORTADO é impossível pelo argumento da seção
// 7.4: significaria que o coordenador decidiu confirmar sem unanimidade. O
// servidor recusa com ErrViolacaoProtocolo em vez de aplicar, para que a
// violação seja visível e verificável pelo teste, em vez de corromper o estado
// em silêncio.
func confirmar(e *Estado, txID string) (Situacao, error) {
	pt, conhecida := e.participacoes[txID]
	if !conhecida {
		return "", fmt.Errorf("%w: CONFIRMAR de transação desconhecida %q", ErrViolacaoProtocolo, txID)
	}

	switch pt.Situacao {
	case SituacaoPreparado:
		if err := gravar(e, tipoConfirmado, dadosTransacao{Tx: txID}); err != nil {
			return "", err
		}
		// Os assentos continuam descontados: o em espera virou confirmado, e a
		// conta de I-P2-1 não se altera (seção 4.8).
		pt.Situacao = SituacaoConfirmado
		return SituacaoConfirmado, nil

	case SituacaoConfirmado, SituacaoCancelado:
		// Repetição do CONFIRMAR, ou cancelamento que chegou antes dele
		// (seção 7.3): nada a fazer, mesma resposta.
		return pt.Situacao, nil

	default: // SituacaoAbortado
		return "", fmt.Errorf("%w: CONFIRMAR de transação abortada %q", ErrViolacaoProtocolo, txID)
	}
}

// abortar é a fase 2 pelo lado do dono, quando a decisão é abortar (seção 7.2;
// seção 4.5 da API).
//
// Transação desconhecida grava o **veto** da D38 antes de responder: uma
// participação ABORTADO sem itens, que fará um PREPARAR atrasado ser recusado.
// Sem ele, o PREPARAR que chegasse depois do ABORTAR deixaria um assento
// descontado por uma transação que já foi abortada, e ninguém mais apareceria
// para liberá-lo.
//
// O abort apenas libera assentos em espera. Estado confirmado nunca é desfeito
// aqui: isto é 2PC, não Saga (regra 4 do CLAUDE.md), e CONFIRMADO ou CANCELADO
// são, pela seção 7.4, violação de protocolo.
func abortar(e *Estado, txID string) (Situacao, error) {
	pt, conhecida := e.participacoes[txID]
	if !conhecida {
		if err := gravar(e, tipoAbortado, dadosTransacao{Tx: txID}); err != nil {
			return "", err
		}
		e.participacoes[txID] = &Participacao{TxID: txID, Situacao: SituacaoAbortado}
		return SituacaoAbortado, nil
	}

	switch pt.Situacao {
	case SituacaoPreparado:
		if err := gravar(e, tipoAbortado, dadosTransacao{Tx: txID}); err != nil {
			return "", err
		}
		devolverItens(e, pt.Itens)
		pt.Situacao = SituacaoAbortado
		return SituacaoAbortado, nil

	case SituacaoAbortado:
		// Repetição, ou ABORTAR que chega depois de um veto: mesma resposta.
		return SituacaoAbortado, nil

	default: // SituacaoConfirmado, SituacaoCancelado
		return "", fmt.Errorf("%w: ABORTAR de transação em %s: %q", ErrViolacaoProtocolo, pt.Situacao, txID)
	}
}

// decisaoDaTransacao responde a consulta pelo lado do **coordenador**
// (seção 9.4; tabela da seção 4.6 da API).
//
// **Não grava nada**, e é esse o conteúdo do abort presumido (D23): transação
// sem decisão no log e sem votação na memória já está abortada, então gravar o
// abort seria gravar o padrão. Como a função não escreve, ela também não pode
// falhar — daí não devolver erro.
//
// Reserva inativa continua respondendo CONFIRMADA: a pergunta é sobre a
// **decisão** da transação, e um cancelamento posterior não a desfaz. O
// cancelamento chega ao participante pelo recurso próprio (seção 4.7 da API).
//
// A ordem dos dois testes importa pouco, porque os dois mapas são lidos sob o
// mesmo lock e não há instante em que a transação esteja fora dos dois depois
// de confirmada. A reserva vem primeiro só porque é a resposta definitiva: a
// marca de votação sai logo depois do commit, e responder EM_ANDAMENTO para
// algo já gravado faria o perguntador esperar mais um prazo à toa.
func decisaoDaTransacao(e *Estado, txID string) Decisao {
	if _, confirmada := e.reservas[txID]; confirmada {
		return DecisaoConfirmada
	}
	if e.transacoesEmVotacao[txID] {
		return DecisaoEmAndamento
	}
	return DecisaoAbortada
}

// consultarParticipacao responde a consulta pelo lado do **participante**
// (seção 9.4; tabela da seção 4.6 da API). O segundo retorno é falso quando a
// transação é desconhecida, caso em que o veto já foi gravado.
//
// O veto (D38) sai de abortar, que é o único lugar do programa que o escreve:
// uma participação ABORTADO sem itens, gravada **antes** de responder. É ela
// que torna a terminação segura — um PREPARAR que chegue atrasado depois disso
// recebe NÃO, o coordenador nunca obtém unanimidade, e quem perguntou pode
// abortar sem risco de contradizer uma decisão de confirmar.
//
// O bool existe porque DESCONHECIDA **não** é um valor de Situacao: ela é
// resposta de protocolo para uma transação que este servidor não guarda, e não
// um estado guardado (seção 3.6 da API). Inventá-la como constante de Situacao
// criaria uma situação que nenhuma máquina de estados da seção 7.3 produz.
func consultarParticipacao(e *Estado, txID string) (Situacao, bool, error) {
	if pt, conhecida := e.participacoes[txID]; conhecida {
		return pt.Situacao, true, nil
	}
	if _, err := abortar(e, txID); err != nil {
		return "", false, err
	}
	return "", false, nil
}

// participacoesEmDuvida devolve as participações em PREPARADO cujo prazo de
// dúvida já venceu (D41; seção 9.4), isto é, as que precisam da terminação
// cooperativa.
//
// O filtro é feito aqui dentro, e não por quem chama, porque o varredor da
// camada 5 roda a cada repetição da consulta: copiar todas as participações a
// cada tique para descartar quase todas seria trabalho sob o lock sem motivo.
//
// A comparação usa a aritmética monotônica de time.Time. PreparadaEm vem de um
// time.Now() cru, sem In() (participante.go), justamente para preservar a
// leitura monotônica: assim o prazo é imune a ajuste do relógio de parede, que
// poderia tanto vencer a dúvida cedo demais quanto nunca.
//
// Participação com PreparadaEm zerada não entra: é a que o replay acabou de
// recriar e que armarPrazosDeDuvida ainda não armou (seção 4.4).
func participacoesEmDuvida(e *Estado, agora time.Time, prazo time.Duration) []Participacao {
	var lista []Participacao
	for _, pt := range e.participacoes {
		if pt.Situacao != SituacaoPreparado || pt.PreparadaEm.IsZero() {
			continue
		}
		if pt.PreparadaEm.Add(prazo).After(agora) {
			continue
		}
		lista = append(lista, pt.copia())
	}
	sort.Slice(lista, func(i, j int) bool { return lista[i].TxID < lista[j].TxID })
	return lista
}

// armarPrazosDeDuvida é a primeira tarefa de recuperação da seção 9.3:
// "participação em PREPARADO → arma o prazo de dúvida". Devolve quantas armou,
// para a linha de registro do boot.
//
// É preciso porque PreparadaEm não vai ao log (seção 4.4): o instante é local e
// monotônico, e o valor de antes da queda não significa nada no processo novo.
// O prazo recomeça no boot, e isso é conservador — espera-se um prazo de dúvida
// inteiro antes de perguntar, mesmo que a participação seja antiga.
//
// Não grava nada e não pode falhar: PreparadaEm é memória pura.
func armarPrazosDeDuvida(e *Estado, agora time.Time) int {
	n := 0
	for _, pt := range e.participacoes {
		if pt.Situacao == SituacaoPreparado && pt.PreparadaEm.IsZero() {
			pt.PreparadaEm = agora
			n++
		}
	}
	return n
}

// reservasNaoConcluidas devolve as reservas cuja fase 2 não terminou, para a
// segunda tarefa de recuperação da seção 9.3: "reserva confirmada e não
// concluída → retoma a fase 2".
//
// O critério é só Concluida, e não Ativa: uma reserva cancelada que nunca
// concluiu a fase 2 também precisa que o CONFIRMAR chegue a todos, porque o
// cancelamento da D39 pressupõe participações CONFIRMADO para cancelar
// (seção 7.3). Nesta fase ainda não há cancelamento, mas o critério já é o
// certo.
//
// Ordenada por ID pelo mesmo motivo de sempre: a iteração de mapa em Go é
// aleatória, e o boot deve fazer o mesmo trabalho na mesma ordem a cada vez.
func reservasNaoConcluidas(e *Estado) []Reserva {
	var lista []Reserva
	for _, r := range e.reservas {
		if !r.Concluida {
			lista = append(lista, r.copia())
		}
	}
	sort.Slice(lista, func(i, j int) bool { return lista[i].ID < lista[j].ID })
	return lista
}

// validarPedido é o passo 2 do PREPARAR (D34) e devolve as caronas resolvidas,
// na ordem dos itens, para que o passo 3 não precise procurá-las de novo.
//
// A validação de forma do pedido está aqui, e não no handler REST, porque o
// coordenador local chama o participante direto (seção 7.1, passo 5): uma
// regra escrita só no handler não valeria para metade das chamadas. O handler
// confere apenas o que depende da URL e dos cabeçalhos — o papel de quem
// chama e a coerência do coordenador com o caminho.
func validarPedido(e *Estado, p PedidoPreparacao) ([]*Carona, error) {
	// O coordenador é a empresa do prefixo do txID (D27): é o prefixo que
	// responde, sem consultar ninguém, quem coordena a transação.
	empresaDoTx, err := EmpresaDoID(p.TxID)
	if err != nil || empresaDoTx != p.Coordenador {
		return nil, fmt.Errorf("%w: txID %q não combina com o coordenador %q", ErrPedidoInvalido, p.TxID, p.Coordenador)
	}

	// A lista serve à terminação cooperativa (D38): sem esta empresa nela, um
	// participante em dúvida não saberia a quem perguntar por nós.
	if !contem(p.Participantes, e.empresa) {
		return nil, fmt.Errorf("%w: %q não está na lista de participantes %v", ErrPedidoInvalido, e.empresa, p.Participantes)
	}

	if err := ValidarEmpresa(p.Carimbo.Empresa); err != nil {
		return nil, fmt.Errorf("%w: carimbo sem empresa válida: %v", ErrPedidoInvalido, err)
	}

	// D32: a referência do passageiro é opaca e vem prefixada pela empresa
	// dele, que é quem pode resolvê-la. O nome é o único outro dado que
	// atravessa a fronteira, e é o que o motorista vê (RF14).
	if _, err := EmpresaDoID(p.Passageiro.Ref); err != nil {
		return nil, fmt.Errorf("%w: referência de passageiro %q: %v", ErrPedidoInvalido, p.Passageiro.Ref, err)
	}
	if p.Passageiro.Nome == "" {
		return nil, fmt.Errorf("%w: passageiro sem nome de exibição", ErrPedidoInvalido)
	}

	if len(p.Itens) == 0 {
		return nil, fmt.Errorf("%w: nenhum item informado", ErrPedidoInvalido)
	}

	caronas := make([]*Carona, len(p.Itens))
	vistas := make(map[string]bool, len(p.Itens))
	for i, item := range p.Itens {
		// Item de carona de outra empresa é erro do coordenador, e não voto
		// NÃO: ele agrupou as pernas por dono errado (seção 4.3 da API).
		dono, err := EmpresaDoID(item.CaronaID)
		if err != nil || dono != e.empresa {
			return nil, fmt.Errorf("%w: a carona %q não é de %q", ErrItemDeOutraEmpresa, item.CaronaID, e.empresa)
		}
		c, existe := e.caronas[item.CaronaID]
		if !existe {
			return nil, fmt.Errorf("%w: %q", ErrCaronaNaoEncontrada, item.CaronaID)
		}
		if c.Cancelada {
			return nil, fmt.Errorf("%w: %q", ErrCaronaCancelada, item.CaronaID)
		}
		if item.De < 0 || item.De >= item.Ate || item.Ate > len(c.Rota)-1 {
			return nil, fmt.Errorf("%w: trecho [%d,%d) fora da rota de %q, que tem %d cidades",
				ErrItemInvalido, item.De, item.Ate, item.CaronaID, len(c.Rota))
		}
		// O coordenador já proíbe carona repetida (seção 7.1, passo 1), mas o
		// participante não confia nele: dois itens da mesma carona fariam o
		// passo 3 avaliar cada um contra o mesmo contador e o passo 4
		// descontar duas vezes o que foi conferido uma.
		if vistas[item.CaronaID] {
			return nil, fmt.Errorf("%w: a carona %q aparece duas vezes", ErrItinerarioInvalido, item.CaronaID)
		}
		vistas[item.CaronaID] = true
		caronas[i] = c
	}
	return caronas, nil
}

// pernasDe monta os dados autoritativos das pernas de uma participação
// (seção 3.3 da API), resolvendo o nome do motorista, que só esta empresa
// conhece (D32).
//
// Roda depois da validação, ou sobre itens que já foram validados quando a
// participação nasceu. A checagem de carona ausente existe pelo mesmo motivo
// da de itinerarioDasPernas: uma inconsistência futura deve virar uma resposta
// incompleta e visível, e não um pânico dentro da seção crítica, que
// derrubaria o servidor inteiro.
func pernasDe(e *Estado, itens []ItemReserva) []Perna {
	pernas := make([]Perna, 0, len(itens))
	for _, item := range itens {
		c, existe := e.caronas[item.CaronaID]
		if !existe || item.Ate > len(c.Rota)-1 {
			continue
		}

		motorista := c.MotoristaID
		if u, ok := e.usuarios[c.MotoristaID]; ok {
			motorista = u.Nome
		}

		preco := 0
		for t := item.De; t < item.Ate; t++ {
			preco += c.PrecoTrecho[t]
		}

		pernas = append(pernas, Perna{
			CaronaID:      c.ID,
			Empresa:       e.empresa,
			MotoristaNome: motorista,
			De:            item.De,
			Ate:           item.Ate,
			// Todas as paradas de De a Ate, inclusive as intermediárias: é com
			// elas que o coordenador verifica cidade repetida (D13).
			Cidades:       append([]string(nil), c.Rota[item.De:item.Ate+1]...),
			Partida:       c.Horarios[item.De],
			Chegada:       c.Horarios[item.Ate],
			PrecoCentavos: preco,
		})
	}
	return pernas
}

// portadores são as participações em PREPARADO que cobrem um trecho, isto é,
// quem está com os assentos em espera dele (seção 4.3).
//
// É valor derivado, calculado a cada consulta, e não uma lista mantida em
// paralelo nas caronas: dado derivável guardado duas vezes é dado que um dia
// diverge (PROJETO.md, seção 4). Como os assentos são contadores (D25), um
// trecho esgotado pode ter vários portadores ao mesmo tempo.
//
// Quem os usa para decidir é o wait-die (decidirDisputa, seção 8.2); eles
// também sustentam a conta de I-P2-1. A ordem é por txID, para que duas
// leituras do mesmo estado deem a mesma lista — a iteração de mapa em Go é
// aleatória.
func portadores(e *Estado, caronaID string, trecho int) []*Participacao {
	var lista []*Participacao
	for _, pt := range e.participacoes {
		if pt.Situacao == SituacaoPreparado && cobre(pt.Itens, caronaID, trecho) {
			lista = append(lista, pt)
		}
	}
	sort.Slice(lista, func(i, j int) bool { return lista[i].TxID < lista[j].TxID })
	return lista
}

// cobre responde se algum item consome o trecho indicado da carona. Um item
// {De, Ate} consome os trechos [De, Ate), como no P1.
func cobre(itens []ItemReserva, caronaID string, trecho int) bool {
	for _, item := range itens {
		if item.CaronaID == caronaID && item.De <= trecho && trecho < item.Ate {
			return true
		}
	}
	return false
}

// contem é a busca linear numa lista curta de empresas (três, no consórcio).
func contem(lista []string, valor string) bool {
	for _, v := range lista {
		if v == valor {
			return true
		}
	}
	return false
}

// ocupacao devolve a conta de I-P2-1 para todos os trechos de todas as caronas
// desta empresa, ordenada por carona e trecho.
//
// Inclui as caronas canceladas: o assento de uma carona cancelada não vale
// nada, mas a conta tem de fechar mesmo nelas, ou um defeito de verdade ficaria
// escondido atrás do cancelamento (é o mesmo motivo de devolverItens devolver
// assento de carona cancelada).
func ocupacao(e *Estado) []OcupacaoTrecho {
	var lista []OcupacaoTrecho
	for _, c := range e.caronas {
		for t := range c.Livres {
			lista = append(lista, OcupacaoTrecho{
				CaronaID:    c.ID,
				Trecho:      t,
				Assentos:    c.Assentos,
				Livres:      c.Livres[t],
				Confirmados: confirmadosNoTrecho(e, c.ID, t),
				EmEspera:    len(portadores(e, c.ID, t)),
			})
		}
	}
	sort.Slice(lista, func(i, j int) bool {
		if lista[i].CaronaID != lista[j].CaronaID {
			return lista[i].CaronaID < lista[j].CaronaID
		}
		return lista[i].Trecho < lista[j].Trecho
	})
	return lista
}

// confirmadosNoTrecho conta os assentos de fato vendidos num trecho: as
// participações CONFIRMADO.
//
// Conta **só** as participações, e é aqui que mora um ponto fino do P2. As
// reservas que esta empresa guarda são a visão de **coordenador** (seção 4.5):
// elas podem ter pernas em outras empresas, e quem desconta assento é sempre o
// dono da carona, pela sua participação. Somar as duas coisas contaria duas
// vezes o mesmo assento toda vez que a empresa fosse coordenadora e
// participante da mesma transação — que é o caso comum, porque o passageiro
// costuma começar a viagem numa carona da própria empresa.
//
// Até a fase 4 a soma das reservas do P1 existia aqui, porque o RESERVAR ainda
// descontava assentos sem passar pelo 2PC. Com o coordenador da fase 5 ela
// saiu: toda venda passa pelo PREPARAR do dono.
func confirmadosNoTrecho(e *Estado, caronaID string, trecho int) int {
	n := 0
	for _, pt := range e.participacoes {
		if pt.Situacao == SituacaoConfirmado && cobre(pt.Itens, caronaID, trecho) {
			n++
		}
	}
	return n
}

// Auditoria é o estado completo da empresa para o verificador de invariantes
// (seção 11.2; seção 4.9 da API).
//
// Existe porque, no P2, uma parte do estado correto não aparece para os
// clientes: assentos em espera e participações são invisíveis pelo protocolo
// TCP, então o verificador do P1, que reconstruía as invariantes pelo próprio
// protocolo, não basta mais. E outra parte do estado correto é **a concordância
// entre empresas**, que só pode ser observada olhando todas.
//
// Não é um componente central: é instrumento de observação, como o -race. Não
// participa de nenhuma decisão, é somente leitura e fica desligado por padrão
// (flag --auditoria).
type Auditoria struct {
	Empresa string
	// Lamport é o relógio desta empresa no instante do retrato (seção 4.9 da
	// API). Vem de dentro da seção crítica, junto com o resto: lido depois, por
	// quem monta a resposta, daria ao verificador um retrato que nunca existiu.
	Lamport       uint64
	Caronas       []CaronaAuditada
	Participacoes []Participacao
	Reservas      []Reserva
}

// CaronaAuditada são os contadores de uma carona, trecho a trecho. A I-P2-1 é
// verificada linha a linha: Livres + Confirmados + EmEspera == Assentos.
type CaronaAuditada struct {
	ID          string
	Assentos    int
	Cancelada   bool
	Livres      []int
	Confirmados []int
	EmEspera    []int
}

// auditoria monta o retrato do estado. Roda com e.mu já travado (D05).
//
// Tudo numa chamada só, sob um único Lock, pelo mesmo motivo de ocupacao: as
// invariantes do P2 são relações **entre** caronas, participações e reservas.
// Ler cada parte numa chamada separada deixaria uma transação concorrente
// entrar no meio, e o verificador acusaria uma violação que nunca existiu.
func auditoria(e *Estado) Auditoria {
	a := Auditoria{
		Empresa:       e.empresa,
		Lamport:       e.lamport(),
		Caronas:       make([]CaronaAuditada, 0, len(e.caronas)),
		Participacoes: make([]Participacao, 0, len(e.participacoes)),
		Reservas:      make([]Reserva, 0, len(e.reservas)),
	}

	for _, c := range e.caronas {
		linha := CaronaAuditada{
			ID:          c.ID,
			Assentos:    c.Assentos,
			Cancelada:   c.Cancelada,
			Livres:      append([]int(nil), c.Livres...),
			Confirmados: make([]int, len(c.Livres)),
			EmEspera:    make([]int, len(c.Livres)),
		}
		for t := range c.Livres {
			linha.Confirmados[t] = confirmadosNoTrecho(e, c.ID, t)
			linha.EmEspera[t] = len(portadores(e, c.ID, t))
		}
		a.Caronas = append(a.Caronas, linha)
	}

	for _, pt := range e.participacoes {
		a.Participacoes = append(a.Participacoes, pt.copia())
	}
	for _, r := range e.reservas {
		a.Reservas = append(a.Reservas, r.copia())
	}

	// Ordem explícita nas três listas: a iteração de mapa em Go é aleatória, e
	// sem ela o mesmo GET /auditoria devolveria ordens diferentes, o que
	// tornaria o teste instável e a comparação entre empresas penosa.
	sort.Slice(a.Caronas, func(i, j int) bool { return a.Caronas[i].ID < a.Caronas[j].ID })
	sort.Slice(a.Participacoes, func(i, j int) bool { return a.Participacoes[i].TxID < a.Participacoes[j].TxID })
	sort.Slice(a.Reservas, func(i, j int) bool { return a.Reservas[i].ID < a.Reservas[j].ID })
	return a
}
