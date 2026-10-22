package federacao

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/transacao"
)

// RedeEmMemoria é a segunda implementação de transacao.Rede (PROJETO_P2.md,
// seção 5.4): entrega cada chamada direto ao participante do destino, sem HTTP,
// e permite **injetar atraso, perda de mensagem e partição** de forma
// determinística.
//
// É isso que torna testáveis os cenários que dependem de ordem de chegada, como
// a disputa cruzada da seção 8.3: o teste decide em que ordem cada PREPARAR
// chega a cada dono, em vez de torcer para o escalonador produzir a ordem certa.
//
// Mora neste pacote, e não em internal/transacao, por um motivo concreto: a
// tradução de erro de domínio em voto NÃO está na tabela única desta borda
// (seção 5.5), e é dela que as duas implementações precisam. Uma rede em
// memória que traduzisse por conta própria deixaria de ser substituto fiel
// exatamente na distinção que o coordenador mais precisa acertar — recusa
// legítima contra falha de comunicação. É o mesmo arranjo do
// dominio.LogMemoria, que também é o dublê morando ao lado do original.
//
// Os testes do cliente HTTP continuam usando servidor de verdade
// (httptest.NewServer): esta rede substitui o **transporte** nos testes de
// coordenação, não o protocolo nos testes da borda.
//
// Toda a configuração tem mutex próprio, que nada tem a ver com o mutex do
// estado (D04): aqui não há estado de domínio nenhum, só os botões de falha.
type RedeEmMemoria struct {
	mu            sync.Mutex
	participantes map[string]*transacao.Participante
	atraso        map[string]time.Duration
	particoes     map[parDeEmpresas]bool
	perdas        map[chaveDePerda]int
}

// parDeEmpresas identifica um corte de comunicação. A partição é simétrica,
// como a de uma rede de verdade, e por isso Particionar grava os dois sentidos.
type parDeEmpresas struct{ origem, destino string }

// chaveDePerda identifica as próximas chamadas a descartar: por quem chama, por
// quem recebe e por operação. A granularidade é essa porque é a que os cenários
// de falha pedem — "o CONFIRMAR para esta empresa se perde uma vez" —, e não
// "esta empresa some", que é partição.
type chaveDePerda struct{ origem, destino, operacao string }

// Nomes das operações, usados nas chaves de perda.
const (
	OperacaoPreparar  = "preparar"
	OperacaoConfirmar = "confirmar"
	OperacaoAbortar   = "abortar"
)

// NovaRedeEmMemoria cria um hub vazio, sem falha nenhuma injetada.
func NovaRedeEmMemoria() *RedeEmMemoria {
	return &RedeEmMemoria{
		participantes: make(map[string]*transacao.Participante),
		atraso:        make(map[string]time.Duration),
		particoes:     make(map[parDeEmpresas]bool),
		perdas:        make(map[chaveDePerda]int),
	}
}

// Registrar liga uma empresa ao seu participante. Empresa não registrada é
// ErrParDesconhecido, como uma empresa fora de --pares (D20).
func (h *RedeEmMemoria) Registrar(empresa string, p *transacao.Participante) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.participantes[empresa] = p
}

// De devolve a rede do ponto de vista de uma empresa, equivalente ao Cliente
// dela.
//
// A visão é por empresa de origem, e não um hub chamado por todos, porque é
// assim que o original funciona: cada empresa tem o seu cliente, que já sabe
// quem ela é. Sem isso, partição e perda não teriam de quem partir.
func (h *RedeEmMemoria) De(empresa string) transacao.Rede {
	return redeDe{hub: h, empresa: empresa}
}

// Atrasar faz toda chamada a uma empresa esperar antes de ser entregue. O
// atraso respeita o ctx de quem chama, e é assim que um teste força o prazo da
// D41 a estourar sem depender de temporização real de rede.
func (h *RedeEmMemoria) Atrasar(empresa string, espera time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.atraso[empresa] = espera
}

// Particionar corta a comunicação entre duas empresas, nos dois sentidos. Para
// quem chama é indistinguível de um par fora do ar (D30): ErrParIndisponivel.
func (h *RedeEmMemoria) Particionar(a, b string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.particoes[parDeEmpresas{a, b}] = true
	h.particoes[parDeEmpresas{b, a}] = true
}

// Reconectar desfaz a partição.
func (h *RedeEmMemoria) Reconectar(a, b string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.particoes, parDeEmpresas{a, b})
	delete(h.particoes, parDeEmpresas{b, a})
}

// Perder descarta as próximas `quantas` chamadas de uma operação entre duas
// empresas, e deixa as seguintes passar.
//
// É perda de **mensagem**, não de servidor: o participante não é chamado, e
// quem chamou vê uma falha de comunicação. É o caso que a idempotência da D28
// existe para cobrir — a repetição pode ter sido só da resposta, e repetir é
// seguro.
func (h *RedeEmMemoria) Perder(origem, destino, operacao string, quantas int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.perdas[chaveDePerda{origem, destino, operacao}] = quantas
}

// redeDe é a visão de uma empresa sobre o hub: implementa transacao.Rede com a
// origem já fixada.
type redeDe struct {
	hub     *RedeEmMemoria
	empresa string
}

var _ transacao.Rede = redeDe{}

// Preparar entrega a fase 1 ao participante do destino e traduz o resultado
// pela mesma tabela que o handler HTTP usa.
func (r redeDe) Preparar(ctx context.Context, destino string, pedido dominio.PedidoPreparacao) (transacao.RespostaPreparar, error) {
	p, err := r.entregar(ctx, destino, OperacaoPreparar, pedido.TxID)
	if err != nil {
		return transacao.RespostaPreparar{}, err
	}

	voto, falha := votoDoParticipante(p.Preparar(pedido))
	if falha != nil {
		return transacao.RespostaPreparar{}, erroDeBorda(*falha)
	}
	return voto, nil
}

// Confirmar entrega a decisão de confirmar.
func (r redeDe) Confirmar(ctx context.Context, destino, txID string) (dominio.Situacao, error) {
	return r.decidir(ctx, destino, txID, OperacaoConfirmar)
}

// Abortar entrega a decisão de abortar.
func (r redeDe) Abortar(ctx context.Context, destino, txID string) (dominio.Situacao, error) {
	return r.decidir(ctx, destino, txID, OperacaoAbortar)
}

func (r redeDe) decidir(ctx context.Context, destino, txID, operacao string) (dominio.Situacao, error) {
	p, err := r.entregar(ctx, destino, operacao, txID)
	if err != nil {
		return "", err
	}

	aplicar := p.Confirmar
	if operacao == OperacaoAbortar {
		aplicar = p.Abortar
	}
	situacao, erroDominio := aplicar(txID)
	if erroDominio != nil {
		return "", erroDeBorda(erroHTTPDeDominio(erroDominio))
	}
	return situacao, nil
}

// entregar aplica os botões de falha e devolve o participante do destino.
//
// A ordem reproduz a de uma rede: primeiro não existe rota (par desconhecido),
// depois o enlace está cortado (partição), depois a mensagem se perde, e só
// então ela viaja — com atraso, se houver. O papel é conferido no fim, porque
// numa rede de verdade quem o confere é o servidor que recebeu.
//
// Nada é feito com o mutex preso além de ler e atualizar os contadores: a
// espera do atraso e a chamada ao participante acontecem fora dele, pelo mesmo
// motivo da regra 2 do CLAUDE.md.
func (r redeDe) entregar(ctx context.Context, destino, operacao, txID string) (*transacao.Participante, error) {
	r.hub.mu.Lock()
	p, conhecido := r.hub.participantes[destino]
	cortada := r.hub.particoes[parDeEmpresas{r.empresa, destino}]
	chave := chaveDePerda{r.empresa, destino, operacao}
	perder := r.hub.perdas[chave] > 0
	if perder {
		r.hub.perdas[chave]--
	}
	espera := r.hub.atraso[destino]
	r.hub.mu.Unlock()

	switch {
	case !conhecido:
		return nil, fmt.Errorf("%w: %q", ErrParDesconhecido, destino)
	case cortada:
		return nil, fmt.Errorf("%w: %s: partição com %s", ErrParIndisponivel, destino, r.empresa)
	case perder:
		return nil, fmt.Errorf("%w: %s: %s perdido", ErrParIndisponivel, destino, operacao)
	}

	if espera > 0 {
		select {
		case <-time.After(espera):
		case <-ctx.Done():
			// Prazo estourado é exatamente o que a D30 trata como falha
			// detectada por timeout, e o participante nunca foi chamado.
			return nil, fmt.Errorf("%w: %s: %v", ErrParIndisponivel, destino, ctx.Err())
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrParIndisponivel, destino, err)
	}

	// Mesma verificação de papel da borda REST (seção 1.2): preparar, confirmar
	// e abortar só são aceitos do coordenador, que é a empresa do prefixo do
	// txID. Conferi-la aqui também é o que impede um teste de passar por um
	// caminho que o servidor de verdade recusaria.
	coordenador, err := dominio.EmpresaDoID(txID)
	if err != nil {
		return nil, erroDeBorda(erroHTTP{http.StatusBadRequest, codigoCorpoInvalido,
			"O identificador de transação precisa do prefixo da empresa coordenadora."})
	}
	if coordenador != r.empresa {
		return nil, erroDeBorda(erroHTTP{http.StatusForbidden, codigoPapelInvalido,
			"Só o coordenador da transação pode decidi-la."})
	}
	return p, nil
}

// erroDeBorda devolve a falha no mesmo formato em que ela chegaria pelo cliente
// HTTP: um *ErroHTTP com status e código.
//
// É o que permite ao coordenador aplicar a política de repetição da seção 1.3
// sem saber por qual rede está falando — em particular, parar no 409.
func erroDeBorda(e erroHTTP) error {
	return &ErroHTTP{Status: e.Status, Codigo: e.Codigo, Mensagem: e.Mensagem}
}
