package dominio

import (
	"errors"
	"fmt"
	"time"
)

// Erros sentinela do domínio (PROJETO.md, seção 5.3).
//
// São valores tipados **sem string de protocolo**: o domínio sabe qual erro
// aconteceu, mas não conhece a rede e por isso não pode conhecer os códigos
// do PROTOCOL.md. Quem traduz sentinela em código é a tabela única de
// internal/servidor. Comparar com errors.Is, nunca por texto da mensagem.
var (
	// ErrCidadeDesconhecida — cidade fora do conjunto de cidades atendidas
	// (D09).
	ErrCidadeDesconhecida = errors.New("dominio: cidade não atendida")

	// ErrRotaInvalida — as paradas não formam uma carona (D09): menos de duas,
	// cidade repetida, horários que não crescem estritamente, ou quantidade de
	// preços diferente do número de trechos. Também é a recusa da busca com
	// origem igual ao destino.
	ErrRotaInvalida = errors.New("dominio: rota inválida")

	// ErrPartidaInvalida — instante de partida que não está no futuro.
	ErrPartidaInvalida = errors.New("dominio: partida não está no futuro")

	// ErrAssentosInvalidos — capacidade menor que 1.
	ErrAssentosInvalidos = errors.New("dominio: quantidade de assentos inválida")

	// ErrPrecoInvalido — preço de trecho negativo.
	ErrPrecoInvalido = errors.New("dominio: preço de trecho inválido")

	// ErrCredenciaisInvalidas — usuário inexistente ou senha incorreta (D12).
	ErrCredenciaisInvalidas = errors.New("dominio: usuário ou senha incorretos")

	// ErrCaronaNaoEncontrada — identificador de carona inexistente.
	ErrCaronaNaoEncontrada = errors.New("dominio: carona não encontrada")

	// ErrNaoEDono — o recurso pertence a outro usuário.
	ErrNaoEDono = errors.New("dominio: recurso pertence a outro usuário")

	// ErrCaronaCancelada — a carona já foi cancelada pelo motorista. Vale
	// tanto para quem tenta cancelá-la de novo quanto para quem tenta
	// reservar nela.
	ErrCaronaCancelada = errors.New("dominio: carona cancelada")

	// ErrItemInvalido — um item da reserva está fora de faixa: lista vazia,
	// De >= Ate, ou índice além da rota da carona. É o passo 1 da seção 7 do
	// PROJETO.md na parte que fala de faixa de valores, e não de encadeamento.
	ErrItemInvalido = errors.New("dominio: item de reserva fora de faixa")

	// ErrItinerarioInvalido — os trechos não formam um itinerário: repetem a
	// mesma carona, não encadeiam no espaço, ou não encadeiam no tempo dentro
	// da janela de baldeação (D13).
	ErrItinerarioInvalido = errors.New("dominio: trechos não formam um itinerário")

	// ErrSemAssento — algum trecho do itinerário não tem assento livre.
	// Detalhes de qual trecho vêm em ErroSemAssento.
	ErrSemAssento = errors.New("dominio: trecho sem assento livre")

	// ErrConflitoHorario — o passageiro já tem reserva ativa que se sobrepõe
	// no tempo ao itinerário pedido (D14). Detalhes em ErroConflitoHorario.
	ErrConflitoHorario = errors.New("dominio: reserva sobreposta no tempo")

	// ErrReservaNaoEncontrada — identificador de reserva inexistente.
	ErrReservaNaoEncontrada = errors.New("dominio: reserva não encontrada")

	// ErrReservaJaCancelada — a reserva já está inativa.
	ErrReservaJaCancelada = errors.New("dominio: reserva já cancelada")

	// ErrPrazoCancelamentoExpirado — o cancelamento chegou fora do prazo
	// (D13). Detalhes em ErroPrazoCancelamento.
	ErrPrazoCancelamentoExpirado = errors.New("dominio: prazo de cancelamento expirado")

	// ErrGeracaoDeID — a fonte de aleatoriedade falhou ao gerar um
	// identificador. Só existe para não engolir o erro de crypto/rand em
	// silêncio; na prática não acontece.
	ErrGeracaoDeID = errors.New("dominio: falha ao gerar identificador")
)

// TodosOsErros lista todos os sentinelas exportados do domínio.
//
// Existe para o teste de cobertura exigido pelo PROJETO.md (seção 5.3): a
// tabela de tradução de internal/servidor precisa ter entrada para cada um.
// Go não permite enumerar os símbolos de um pacote em tempo de execução, e
// sem esta lista um sentinela novo cairia calado em ERRO_INTERNO. Todo
// sentinela adicionado acima entra aqui.
func TodosOsErros() []error {
	return []error{
		ErrCidadeDesconhecida,
		ErrRotaInvalida,
		ErrPartidaInvalida,
		ErrAssentosInvalidos,
		ErrPrecoInvalido,
		ErrCredenciaisInvalidas,
		ErrCaronaNaoEncontrada,
		ErrNaoEDono,
		ErrCaronaCancelada,
		ErrItemInvalido,
		ErrItinerarioInvalido,
		ErrSemAssento,
		ErrConflitoHorario,
		ErrReservaNaoEncontrada,
		ErrReservaJaCancelada,
		ErrPrazoCancelamentoExpirado,
		ErrGeracaoDeID,
	}
}

// Três erros do protocolo não se resolvem só com um código: a seção 5.9 do
// PROTOCOL.md manda SEM_ASSENTO dizer *qual* trecho esgotou, CONFLITO_HORARIO
// dizer *qual* reserva conflita, e a 5.11 manda PRAZO_CANCELAMENTO_EXPIRADO
// dizer de que partida se fala. Quem tem esses dados é o domínio.
//
// A saída são erros tipados que embrulham o sentinela correspondente: errors.Is
// continua reconhecendo o sentinela — a tabela de tradução funciona sem saber
// que eles existem —, e o handler que quiser o detalhe o extrai com errors.As.
// Nenhum deles carrega código nem texto de protocolo; cidade e identificador
// são vocabulário do domínio.

// ErroSemAssento identifica o trecho que esgotou.
type ErroSemAssento struct {
	CaronaID     string
	IndiceTrecho int
	Origem       string
	Destino      string
}

func (e *ErroSemAssento) Error() string {
	return fmt.Sprintf("dominio: carona %s, trecho %d (%s → %s) sem assento livre",
		e.CaronaID, e.IndiceTrecho, e.Origem, e.Destino)
}

func (e *ErroSemAssento) Unwrap() error { return ErrSemAssento }

// ErroConflitoHorario identifica a reserva ativa que ocupa o período pedido.
type ErroConflitoHorario struct {
	ReservaID string
}

func (e *ErroConflitoHorario) Error() string {
	return fmt.Sprintf("dominio: reserva %s já ocupa o período", e.ReservaID)
}

func (e *ErroConflitoHorario) Unwrap() error { return ErrConflitoHorario }

// ErroPrazoCancelamento identifica a partida da qual o prazo foi contado.
type ErroPrazoCancelamento struct {
	Partida time.Time
}

func (e *ErroPrazoCancelamento) Error() string {
	return fmt.Sprintf("dominio: prazo de cancelamento da partida %s expirado", e.Partida)
}

func (e *ErroPrazoCancelamento) Unwrap() error { return ErrPrazoCancelamentoExpirado }
