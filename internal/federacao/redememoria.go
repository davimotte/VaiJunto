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
	// estados e entregadores servem só ao aviso de carona cancelada (D39,
	// seção 10.2): é o único recurso cujo efeito mora no Estado do
	// COORDENADOR (as reservas), não no Participante, e cuja decisão gera
	// avisos que precisam ser entregues pelo Entregador do destino — o
	// equivalente, na rede em memória, do handler HTTP chamar
	// cfg.Entregador.Enfileirar depois de decidir.
	estados      map[string]*dominio.Estado
	entregadores map[string]*transacao.Entregador
	atraso       map[string]time.Duration
	particoes    map[parDeEmpresas]bool
	perdas       map[chaveDePerda]int
	gancho       func(Chamada)
}

// Chamada é o que o gancho de Interceptar vê de uma mensagem prestes a ser
// entregue.
//
// Carimbo só vem preenchido no preparar, porque é a única operação que carrega
// um — confirmar, abortar e consultar levam apenas o txID. É o campo que
// permite a um teste distinguir tentativas da mesma reserva, que têm txID
// diferente e carimbo igual (D36).
type Chamada struct {
	Origem   string
	Destino  string
	Operacao string
	TxID     string
	Carimbo  dominio.Carimbo
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
	OperacaoPreparar              = "preparar"
	OperacaoConfirmar             = "confirmar"
	OperacaoAbortar               = "abortar"
	OperacaoConsultar             = "consultar"
	OperacaoCancelar              = "cancelar"
	OperacaoAvisarCaronaCancelada = "avisar-carona-cancelada"
)

// NovaRedeEmMemoria cria um hub vazio, sem falha nenhuma injetada.
func NovaRedeEmMemoria() *RedeEmMemoria {
	return &RedeEmMemoria{
		participantes: make(map[string]*transacao.Participante),
		estados:       make(map[string]*dominio.Estado),
		entregadores:  make(map[string]*transacao.Entregador),
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

// RegistrarFederacao liga uma empresa ao seu Estado e ao seu Entregador, para
// que redeDe.AvisarCaronaCancelada (D39, seção 10.2) consiga decidir o
// cancelamento no coordenador certo e repassar os avisos ao outbox dele —
// exatamente o que a borda HTTP faz com cfg.Estado e cfg.Entregador.
func (h *RedeEmMemoria) RegistrarFederacao(empresa string, estado *dominio.Estado, entregador *transacao.Entregador) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.estados[empresa] = estado
	h.entregadores[empresa] = entregador
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

// Interceptar instala um gancho chamado antes de cada entrega, FORA do mutex
// do hub. Um gancho nil desliga a interceptação.
//
// É o botão que faltava para os cenários que dependem de **ordem de chegada**,
// e não de atraso: o gancho pode bloquear, e enquanto bloqueia a mensagem está
// parada na rede. É assim que o TR3 produz a ordem cruzada do instante 1 da
// seção 8.3 — T₁ chegando primeiro a um dono e T₂ primeiro ao outro — sem
// depender de o escalonador cooperar nem de um `sleep` calibrado.
//
// Fica fora do mutex de propósito: um gancho que bloqueia com o lock do hub
// preso travaria todas as outras mensagens junto, e o teste deixaria de
// atrasar uma mensagem para atrasar a rede inteira.
//
// O gancho **não** é interrompido pelo ctx de quem chama: segurar uma
// mensagem para sempre pendura o teste até o prazo do `go test`, e não até o
// prazo de votação. Quem quiser provar o que acontece quando uma mensagem não
// chega usa Atrasar ou Particionar, que são os botões feitos para isso.
func (h *RedeEmMemoria) Interceptar(gancho func(Chamada)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.gancho = gancho
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
	p, err := r.entregar(ctx, Chamada{
		Origem:   r.empresa,
		Destino:  destino,
		Operacao: OperacaoPreparar,
		TxID:     pedido.TxID,
		Carimbo:  pedido.Carimbo,
	}, soDoCoordenador)
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

// Cancelar entrega a decisão de cancelar (D39).
func (r redeDe) Cancelar(ctx context.Context, destino, txID string) (dominio.Situacao, error) {
	return r.decidir(ctx, destino, txID, OperacaoCancelar)
}

// AvisarCaronaCancelada entrega o aviso inverso da D39 (seção 10.2): quem
// chama é um participante, nunca o coordenador, então a exigência de papel é
// deTodoOConsorcio — a checagem de "participa da reserva" é inteiramente do
// domínio (estado.AvisarCaronaCancelada), não desta borda.
func (r redeDe) AvisarCaronaCancelada(ctx context.Context, destino, txID, caronaID string) (string, error) {
	// entregar aplica partição, perda, gancho e atraso como qualquer outra
	// chamada; o *Participante que ela devolve não serve a este recurso — o
	// efeito mora no Estado do coordenador (as reservas), por isso o estado
	// vem do mapa próprio logo abaixo.
	if _, err := r.entregar(ctx, Chamada{
		Origem:   r.empresa,
		Destino:  destino,
		Operacao: OperacaoAvisarCaronaCancelada,
		TxID:     txID,
	}, deTodoOConsorcio); err != nil {
		return "", err
	}

	r.hub.mu.Lock()
	estado := r.hub.estados[destino]
	r.hub.mu.Unlock()
	if estado == nil {
		return "", fmt.Errorf("%w: %q sem Estado registrado na rede em memória", ErrParDesconhecido, destino)
	}

	situacao, avisos, erroDominio := estado.AvisarCaronaCancelada(txID, caronaID, r.empresa)
	if erroDominio != nil {
		return "", erroDeBorda(erroHTTPDeDominio(erroDominio))
	}
	// A rede em memória entrega direto ao domínio do destino, sem passar por
	// internal/federacao — por isso é esta função, e não um handler HTTP,
	// que precisa repassar os avisos recém-criados ao entregador do destino,
	// exatamente como o handler tratarAvisoCaronaCancelada faz pelo HTTP.
	r.hub.mu.Lock()
	entregadorDoDestino := r.hub.entregadores[destino]
	r.hub.mu.Unlock()
	if entregadorDoDestino != nil && len(avisos) > 0 {
		entregadorDoDestino.Enfileirar(avisos)
	}
	return situacao, nil
}

// Consultar entrega a consulta da terminação cooperativa (seção 4.6 da API).
func (r redeDe) Consultar(ctx context.Context, destino, txID string) (transacao.RespostaConsulta, error) {
	// deTodoOConsorcio: consultar é o único recurso de transação que não exige
	// o papel de coordenador (seção 1.2 da API). Exigi-lo aqui faria a rede em
	// memória recusar o que o servidor de verdade aceita, e a terminação
	// cooperativa ficaria intestável justamente no caminho que ela usa.
	p, err := r.entregar(ctx, Chamada{
		Origem:   r.empresa,
		Destino:  destino,
		Operacao: OperacaoConsultar,
		TxID:     txID,
	}, deTodoOConsorcio)
	if err != nil {
		return transacao.RespostaConsulta{}, err
	}

	resposta, erroDominio := p.Consultar(txID)
	if erroDominio != nil {
		return transacao.RespostaConsulta{}, erroDeBorda(erroHTTPDeDominio(erroDominio))
	}
	return resposta, nil
}

func (r redeDe) decidir(ctx context.Context, destino, txID, operacao string) (dominio.Situacao, error) {
	p, err := r.entregar(ctx, Chamada{
		Origem:   r.empresa,
		Destino:  destino,
		Operacao: operacao,
		TxID:     txID,
	}, soDoCoordenador)
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

// exigenciaDePapel diz qual das duas linhas da tabela da seção 1.2 da API vale
// para a operação que está sendo entregue.
//
// É parâmetro, e não um if sobre o nome da operação dentro do entregar, para
// que acrescentar um recurso obrigue quem o escreve a dizer qual é o papel
// dele — que é exatamente a decisão fácil de esquecer.
type exigenciaDePapel bool

const (
	// soDoCoordenador: preparar, confirmar e abortar. Quem chama tem de ser a
	// empresa do prefixo do txID.
	soDoCoordenador exigenciaDePapel = false
	// deTodoOConsorcio: consultar. Quem é consultado sobre uma transação
	// desconhecida não tem como saber quem participa dela.
	deTodoOConsorcio exigenciaDePapel = true
)

// entregar aplica os botões de falha e devolve o participante do destino.
//
// A ordem reproduz a de uma rede: primeiro não existe rota (par desconhecido),
// depois o enlace está cortado (partição), depois a mensagem se perde, e só
// então ela viaja — passando pelo gancho de ordenação e pelo atraso, se
// houver. O papel é conferido no fim, porque numa rede de verdade quem o
// confere é o servidor que recebeu.
//
// Nada é feito com o mutex preso além de ler e atualizar os contadores: o
// gancho, a espera do atraso e a chamada ao participante acontecem fora dele,
// pelo mesmo motivo da regra 2 do CLAUDE.md.
func (r redeDe) entregar(ctx context.Context, chamada Chamada, papel exigenciaDePapel) (*transacao.Participante, error) {
	destino, operacao, txID := chamada.Destino, chamada.Operacao, chamada.TxID

	r.hub.mu.Lock()
	p, conhecido := r.hub.participantes[destino]
	cortada := r.hub.particoes[parDeEmpresas{r.empresa, destino}]
	chave := chaveDePerda{r.empresa, destino, operacao}
	perder := r.hub.perdas[chave] > 0
	if perder {
		r.hub.perdas[chave]--
	}
	espera := r.hub.atraso[destino]
	gancho := r.hub.gancho
	r.hub.mu.Unlock()

	switch {
	case !conhecido:
		return nil, fmt.Errorf("%w: %q", ErrParDesconhecido, destino)
	case cortada:
		return nil, fmt.Errorf("%w: %s: partição com %s", ErrParIndisponivel, destino, r.empresa)
	case perder:
		return nil, fmt.Errorf("%w: %s: %s perdido", ErrParIndisponivel, destino, operacao)
	}

	// A mensagem existe e vai ser entregue: é aqui que o teste pode segurá-la
	// para impor uma ordem de chegada (ver Interceptar).
	if gancho != nil {
		gancho(chamada)
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

	// Mesma verificação de papel da borda REST (seção 1.2), que é o que impede
	// um teste de passar por um caminho que o servidor de verdade recusaria. O
	// prefixo é conferido em toda operação; a exigência de **ser** o
	// coordenador vale para todas menos consultar.
	coordenador, err := dominio.EmpresaDoID(txID)
	if err != nil {
		return nil, erroDeBorda(erroHTTP{http.StatusBadRequest, codigoCorpoInvalido,
			"O identificador de transação precisa do prefixo da empresa coordenadora."})
	}
	if papel == soDoCoordenador && coordenador != r.empresa {
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
