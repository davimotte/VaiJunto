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

	// ErrEmpresaInvalida — identificador de empresa fora do formato (D27):
	// minúsculas, dígitos e hífen, começando por letra. Barra o boot do
	// servidor; não nasce de nenhuma requisição.
	ErrEmpresaInvalida = errors.New("dominio: identificador de empresa inválido")

	// ErrIDSemEmpresa — identificador de carona ou reserva sem o prefixo
	// "<empresa>:" (D27). Sem prefixo não há como saber quem é o dono, e o
	// domínio recusa em vez de presumir a empresa local.
	ErrIDSemEmpresa = errors.New("dominio: identificador sem prefixo de empresa")

	// ErrFalhaNoLog — o registro não chegou ao disco (D22). A operação NÃO foi
	// aplicada: sem write-ahead bem-sucedido o servidor não pode lembrar de
	// algo que o log não guardou.
	ErrFalhaNoLog = errors.New("dominio: falha ao gravar no log")

	// ErrPedidoInvalido — o pedido de PREPARAR não tem a forma da seção 4.3 do
	// API_CONSORCIO.md: txID que não combina com o coordenador, lista de
	// participantes sem esta empresa, carimbo sem empresa, passageiro sem
	// referência prefixada ou sem nome, nenhum item. É erro de quem chama, e
	// nenhuma repetição o conserta.
	ErrPedidoInvalido = errors.New("dominio: pedido de preparação inválido")

	// ErrItemDeOutraEmpresa — um item do PREPARAR cita carona que não é desta
	// empresa (D27). Não é voto NÃO: é erro do coordenador, que agrupou as
	// pernas por dono errado.
	ErrItemDeOutraEmpresa = errors.New("dominio: item de carona de outra empresa")

	// ErrTransacaoAbortada — a transação já foi abortada neste participante,
	// inclusive por veto (D38). Um PREPARAR que chegue depois disso é recusado,
	// e é essa recusa que torna seguro abortar sem falar com o coordenador.
	ErrTransacaoAbortada = errors.New("dominio: transação já abortada")

	// ErrViolacaoProtocolo — transição impossível pela seção 7.4 do
	// PROJETO_P2.md: CONFIRMAR de participação abortada ou desconhecida,
	// ABORTAR de participação confirmada.
	//
	// Pelo argumento de atomicidade, ela **nunca** deveria acontecer. O erro
	// existe para que, se acontecer, apareça registrada e verificável pelo
	// teste, em vez de corromper o estado em silêncio.
	ErrViolacaoProtocolo = errors.New("dominio: violação de protocolo do 2PC")

	// ErrReservaEmAndamento — o passageiro já tem uma reserva em andamento
	// (D35). Só o coordenador o produz, e só entre o RESERVAR e a decisão.
	ErrReservaEmAndamento = errors.New("dominio: reserva em andamento para o passageiro")

	// ErrEmpresaIndisponivel — alguma empresa dona de um trecho não respondeu
	// dentro do prazo de votação (D30, D41). Nenhum assento fica preso: o
	// coordenador aborta e quem não recebeu o ABORTAR descobre a decisão
	// consultando (D23).
	ErrEmpresaIndisponivel = errors.New("dominio: empresa indisponível")

	// ErrAguarde — wait-die: todo trecho esgotado está com portadores, e
	// nenhum deles é mais velho que esta transação (D24; seção 8.2).
	//
	// Não é recusa: é "volte depois". O participante não grava nada e não
	// segura o handler — quem espera é o coordenador, repetindo o PREPARAR a
	// cada 200 ms (D41). Detalhes do trecho vêm em ErroAguarde.
	ErrAguarde = errors.New("dominio: assento em espera por transação mais nova")

	// ErrPrecedencia — wait-die: algum portador de um trecho esgotado é mais
	// velho que esta transação, que por isso desiste (D24; seção 8.2).
	//
	// É voto NÃO, mas é o único cuja resposta certa é tentar de novo: o
	// coordenador refaz a reserva com o mesmo carimbo (D36). Detalhes do trecho
	// vêm em ErroPrecedencia.
	ErrPrecedencia = errors.New("dominio: transação mais nova que um portador do assento")

	// ErrDisputaPerdida — as três tentativas da D36 se esgotaram, ou o prazo de
	// votação acabou com um AGUARDE pendente (seções 8.5 e 5.2 da API).
	//
	// Nasce no **coordenador**, nunca num participante: é o resultado da
	// disputa inteira, e não a avaliação de um dono. Por isso não atravessa a
	// borda REST — o que atravessa é PRECEDENCIA, trecho a trecho.
	ErrDisputaPerdida = errors.New("dominio: disputa pelo assento perdida")

	// ErrOperacaoIndisponivel — operação prevista que ainda não está ligada
	// nesta versão.
	//
	// **Provisório.** Existe só da fase 5 à 7, para os dois cancelamentos que
	// dependem do outbox da D39: cancelar uma reserva e cancelar uma carona que
	// tenha reserva. Recusar por inteiro é honesto; devolver os assentos só no
	// coordenador deixaria os dos outros donos presos para sempre e violaria a
	// I-P2-5. Sai na fase 8, junto com a seção 10.
	ErrOperacaoIndisponivel = errors.New("dominio: operação indisponível nesta versão")
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
		ErrEmpresaInvalida,
		ErrIDSemEmpresa,
		ErrFalhaNoLog,
		ErrPedidoInvalido,
		ErrItemDeOutraEmpresa,
		ErrTransacaoAbortada,
		ErrViolacaoProtocolo,
		ErrReservaEmAndamento,
		ErrEmpresaIndisponivel,
		ErrAguarde,
		ErrPrecedencia,
		ErrDisputaPerdida,
		ErrOperacaoIndisponivel,
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

func (e *ErroSemAssento) TrechoEsgotado() (string, int) { return e.CaronaID, e.IndiceTrecho }

// TrechoDisputado é o que SEM_ASSENTO, AGUARDE e PRECEDENCIA têm em comum: os
// três falam do mesmo trecho esgotado, e a seção 4.3 da API manda os três
// levarem `carona_id` e `trecho` na resposta.
//
// A interface existe para que a borda extraia o trecho uma vez só, com um
// errors.As, em vez de um por tipo concreto. Fica no domínio, e não na borda,
// porque é uma propriedade dos erros — a borda só a consome.
type TrechoDisputado interface {
	error
	TrechoEsgotado() (caronaID string, indice int)
}

// Os dois erros do wait-die (seção 8.2). São tipos separados, e não um só com
// um campo dizendo qual é, porque o que a borda faz com cada um é diferente:
// AGUARDE é um voto próprio, PRECEDENCIA é voto NÃO com motivo. Distinguir por
// tipo deixa isso em errors.As, onde o compilador ajuda, em vez de num if sobre
// um campo.

// ErroAguarde identifica o primeiro trecho esgotado pelo qual esta transação
// vai esperar.
type ErroAguarde struct {
	CaronaID     string
	IndiceTrecho int
	Origem       string
	Destino      string
}

func (e *ErroAguarde) Error() string {
	return fmt.Sprintf("dominio: carona %s, trecho %d (%s → %s) em espera por transação mais nova",
		e.CaronaID, e.IndiceTrecho, e.Origem, e.Destino)
}

func (e *ErroAguarde) Unwrap() error { return ErrAguarde }

func (e *ErroAguarde) TrechoEsgotado() (string, int) { return e.CaronaID, e.IndiceTrecho }

// ErroPrecedencia identifica o trecho cujo portador é mais velho que esta
// transação, isto é, o trecho em que ela perdeu a precedência.
type ErroPrecedencia struct {
	CaronaID     string
	IndiceTrecho int
	Origem       string
	Destino      string
}

func (e *ErroPrecedencia) Error() string {
	return fmt.Sprintf("dominio: carona %s, trecho %d (%s → %s) com portador mais velho",
		e.CaronaID, e.IndiceTrecho, e.Origem, e.Destino)
}

func (e *ErroPrecedencia) Unwrap() error { return ErrPrecedencia }

func (e *ErroPrecedencia) TrechoEsgotado() (string, int) { return e.CaronaID, e.IndiceTrecho }

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
