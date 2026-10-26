package transacao

import (
	"context"
	"sync"
	"time"

	"vaijunto/internal/dominio"
)

// Entregador é o outbox da D39 (seção 4.7 e 10 do PROJETO_P2.md): entrega
// confiável dos avisos de cancelamento, repetindo sem limite até a
// confirmação (D41), no mesmo molde da fase 2 do Coordenador
// (confirmarTodos/abortarTodos).
//
// Cancelamento, diferente de confirmar, não precisa de votação — nenhuma
// empresa pode recusá-lo (D39) —, então não há disputa a resolver aqui: o
// único trabalho é garantir que a decisão, já gravada, chegue a quem falta,
// mesmo que o destino esteja fora do ar agora.
//
// É camada 5, como o Coordenador e o Terminador, e vale a mesma regra 2: toda
// a E/S acontece sobre cópias que o estado já devolveu, nunca com o lock
// preso.
type Entregador struct {
	empresa      string
	estado       *dominio.Estado
	falha        *Injetor
	prazoEntrega time.Duration

	// rede tem seu próprio mutex, independente do do estado (D04): aqui não há
	// estado de domínio nenhum, só a referência à rede. Existe porque, nos
	// testes multiempresa, o Entregador precisa nascer antes de a rede ficar
	// pronta — o cliente HTTP só existe depois que todas as portas dos pares
	// são conhecidas (seção 5.2) —, e UsarRede é quem a injeta depois, no
	// mesmo padrão de dominio.Estado.UsarLog/UsarRelogio.
	mu   sync.Mutex
	rede Rede

	// base encerra as entregas em voo. Ver Parar.
	base     context.Context
	encerrar context.CancelFunc
}

// ConfigEntregador são as peças que o boot liga ao entregador.
type ConfigEntregador struct {
	Empresa string
	Estado  *dominio.Estado
	// Rede pode ficar vazia na construção e ser preenchida depois com
	// UsarRede, para os casos em que a rede desta empresa só existe depois do
	// Entregador (seção 5.2).
	Rede Rede // pares; nos testes, a rede em memória

	// PrazoEntrega é o intervalo entre repetições de um aviso não confirmado
	// (D41: 2 s, o mesmo da fase 2 do Coordenador).
	PrazoEntrega time.Duration

	// Falha é o injetor de pontos de falha desta instância (D31, seção 9.5).
	// Nil desliga todos os pontos, que é o padrão de toda execução normal.
	Falha *Injetor
}

const prazoEntregaPadrao = 2 * time.Second

func NovoEntregador(cfg ConfigEntregador) *Entregador {
	en := &Entregador{
		empresa:      cfg.Empresa,
		estado:       cfg.Estado,
		rede:         cfg.Rede,
		falha:        cfg.Falha,
		prazoEntrega: cfg.PrazoEntrega,
	}
	if en.prazoEntrega <= 0 {
		en.prazoEntrega = prazoEntregaPadrao
	}
	en.base, en.encerrar = context.WithCancel(context.Background())
	return en
}

// UsarRede injeta a rede desta empresa depois da construção, para os casos em
// que o Entregador precisa existir antes de a rede estar pronta. O lock é só
// para que a troca seja visível a qualquer goroutine (-race), como em
// dominio.Estado.UsarLog.
func (en *Entregador) UsarRede(rede Rede) {
	en.mu.Lock()
	defer en.mu.Unlock()
	en.rede = rede
}

func (en *Entregador) redeAtual() Rede {
	en.mu.Lock()
	defer en.mu.Unlock()
	return en.rede
}

// Parar encerra as entregas em voo, como Coordenador.Parar e Terminador.Parar
// e pelo mesmo motivo: em produção o servidor não tem desligamento (seção
// 9.1), mas num `go test -race -count=20` uma goroutine que repete a cada 2 s
// continuaria batendo em servidores httptest já fechados.
func (en *Entregador) Parar() { en.encerrar() }

// Recuperar é a retomada do boot (seção 9.3: "aviso no outbox → retoma o
// envio"): relê os avisos ainda pendentes (AVISO_REGISTRADO sem
// AVISO_ENTREGUE) e dispara uma entrega para cada. Devolve quantos retomou.
func (en *Entregador) Recuperar() int {
	pendentes := en.estado.AvisosPendentes()
	for _, a := range pendentes {
		go en.entregar(a)
	}
	return len(pendentes)
}

// Enfileirar dispara a entrega de avisos recém-criados. Quem chama já soltou
// o lock do estado — o aviso em si já está gravado com fsync (regra 3) —, e
// esta função só começa a goroutine que vai repeti-lo (regra 2).
func (en *Entregador) Enfileirar(avisos []dominio.Aviso) {
	for _, a := range avisos {
		go en.entregar(a)
	}
}

// entregar repete o envio de um aviso a cada PrazoEntrega até confirmar, sem
// limite (D41) — mesmo padrão de abortarTodos: o único motivo para parar é o
// servidor terminar (ver Parar).
func (en *Entregador) entregar(a dominio.Aviso) {
	// D31: ponto aviso-apos-registrar (TF8). O AVISO_REGISTRADO já está no
	// log com fsync e nenhum HTTP saiu ainda — é o estado que a retomada do
	// boot (Recuperar) precisa resolver sozinha, entregando o aviso que
	// ficou pendente.
	en.falha.Em(PontoAvisoAposRegistrar)

	for {
		if en.base.Err() != nil {
			return
		}
		rede := en.redeAtual()
		ctx, cancelar := context.WithTimeout(en.base, en.prazoEntrega)
		var err error
		switch a.Tipo {
		case dominio.AvisoCancelarPernas:
			_, err = en.cancelar(ctx, rede, a.Destino, a.TxID)
		case dominio.AvisoCaronaCancelada:
			_, err = rede.AvisarCaronaCancelada(ctx, a.Destino, a.TxID, a.CaronaID)
		}
		cancelar()
		if err == nil {
			_ = en.estado.AvisoEntregue(a.ID)
			return
		}
		select {
		case <-en.base.Done():
			return
		case <-time.After(en.prazoEntrega):
		}
	}
}

// cancelar chama Cancelar pela rede ou localmente, como o Coordenador faz com
// Preparar/Confirmar/Abortar: uma empresa não faz HTTP contra si mesma.
func (en *Entregador) cancelar(ctx context.Context, rede Rede, destino, txID string) (dominio.Situacao, error) {
	if destino == en.empresa {
		// O coordenador nunca enfileira um aviso CANCELAR_PERNAS para a
		// própria empresa (decidirCancelamentoDaReserva pula e.empresa), mas
		// o caminho fica correto e simétrico ao do Coordenador, caso isso
		// mude um dia.
		return en.estado.CancelarParticipacao(txID)
	}
	return rede.Cancelar(ctx, destino, txID)
}
