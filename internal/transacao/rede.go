package transacao

import (
	"context"
	"errors"

	"vaijunto/internal/dominio"
)

// Rede é como o coordenador e o participante falam com os pares
// (PROJETO_P2.md, seção 5.4): uma operação por recurso de transação da API,
// nenhuma menção a HTTP.
//
// Existem duas implementações, as duas em internal/federacao: o Cliente, que
// fala HTTP de verdade, e a RedeEmMemoria, que entrega a chamada direto ao
// participante do destino e permite **injetar atraso, perda de mensagem e
// partição** de forma determinística. É isso que torna testáveis os cenários
// que dependem de ordem de chegada, como a disputa cruzada da seção 8.3: o
// teste decide em que ordem cada PREPARAR chega a cada dono, em vez de torcer
// para o escalonador produzir a ordem certa.
//
// A interface é declarada aqui, no consumidor, e não no pacote que a
// implementa: é a camada 5 que diz do que precisa, e é por isso que ela não
// importa internal/federacao (seção 5.3).
//
// Os prazos da D41 são de quem chama, e entram pelo ctx. `destino` é o
// identificador da empresa, nunca uma URL: quem resolve empresa → endereço é a
// implementação, com a lista estática da D20.
//
// consultar (fase 6A) e cancelar (fase 8) entram aqui quando houver quem as
// chame. Declará-las antes obrigaria as duas implementações a devolver "não
// implementado", que é um estado que nenhum teste sabe interpretar.
type Rede interface {
	// Preparar é a fase 1 do 2PC (seção 4.3 da API). O txID vai dentro do
	// pedido; é a implementação HTTP que o põe no caminho da URL.
	Preparar(ctx context.Context, destino string, pedido dominio.PedidoPreparacao) (RespostaPreparar, error)

	// Confirmar é a fase 2 com decisão de confirmar (seção 4.4 da API).
	Confirmar(ctx context.Context, destino, txID string) (dominio.Situacao, error)

	// Abortar é a fase 2 com decisão de abortar (seção 4.5 da API).
	Abortar(ctx context.Context, destino, txID string) (dominio.Situacao, error)
}

// Voto é a resposta de um participante ao PREPARAR (seção 4.3 da API).
//
// As três formas voltam num 200: um voto NÃO é uma requisição processada, e não
// um erro (seção 2 da API). A distinção importa para o coordenador, que só
// precisa separar "o participante respondeu" de "o participante não respondeu
// direito" — se o NÃO fosse código de erro, ele confundiria recusa legítima com
// falha e repetiria à toa.
type Voto string

const (
	VotoSim     Voto = "SIM"
	VotoNao     Voto = "NAO"
	VotoAguarde Voto = "AGUARDE"
)

// Motivos do voto NÃO (seção 5.2 da API), na forma em que o coordenador os
// consome.
//
// Nascem aqui, e não na borda, porque é o coordenador quem **decide** a partir
// deles: PRECEDENCIA manda tentar de novo com o mesmo carimbo (D36), e os
// demais mandam responder ao passageiro. A borda de internal/federacao usa
// estas mesmas constantes, para que não existam duas listas do mesmo
// vocabulário que possam divergir.
const (
	MotivoSemAssento          = "SEM_ASSENTO"
	MotivoPrecedencia         = "PRECEDENCIA"
	MotivoCaronaNaoEncontrada = "CARONA_NAO_ENCONTRADA"
	MotivoCaronaCancelada     = "CARONA_CANCELADA"
	MotivoItinerarioInvalido  = "ITINERARIO_INVALIDO"
	MotivoTransacaoAbortada   = "TRANSACAO_ABORTADA"
)

// RespostaPreparar é o voto de um participante, já na forma em que o
// coordenador o consome.
//
// Motivo é um dos códigos acima, da seção 5.2 da API. É string, e não tipo
// próprio, porque atravessa o fio como string e a borda o traduz de sentinela
// de domínio numa tabela única (seção 5.5), pela qual as duas implementações da
// Rede passam.
//
// CaronaID e Trecho acompanham SEM_ASSENTO, PRECEDENCIA e AGUARDE, indicando o
// primeiro trecho esgotado. Servem ao diagnóstico e ao registro de operações.
type RespostaPreparar struct {
	Voto     Voto
	Pernas   []dominio.Perna // só no SIM: os dados autoritativos (D34)
	Motivo   string          // só no NÃO
	CaronaID string
	Trecho   int
}

// Votante é o participante local visto pelo coordenador: a empresa que também
// é dona de algum trecho da transação que ela coordena (D21).
//
// Existe para que o participante local seja chamado **direto**, sem passar pela
// rede — uma empresa não faz HTTP contra si mesma —, mas ainda assim pelo mesmo
// código e, principalmente, pela mesma tradução de erro em voto. Quem a
// implementa é internal/federacao, onde mora a tabela única da seção 5.5: se o
// caminho local traduzisse por conta própria, haveria duas tabelas, e elas
// divergiriam justamente na distinção que o coordenador mais precisa acertar —
// recusa legítima contra falha de comunicação.
//
// As assinaturas são as da Rede sem ctx e sem destino: não há rede, logo não há
// prazo de chamada nem par a resolver.
type Votante interface {
	Preparar(pedido dominio.PedidoPreparacao) (RespostaPreparar, error)
	Confirmar(txID string) (dominio.Situacao, error)
	Abortar(txID string) (dominio.Situacao, error)
}

// erroComStatus é implementado pelos erros que trazem o status HTTP com que um
// par respondeu.
//
// Declarada aqui, no consumidor, pelo mesmo motivo da Rede: o tipo concreto
// (federacao.ErroHTTP) está na camada que importa esta, e recebê-lo inverteria
// a dependência. O coordenador só precisa do número para aplicar a política de
// repetição da seção 1.3 da API.
type erroComStatus interface {
	StatusHTTP() int
}

// ehViolacao responde se o erro é o 409 VIOLACAO_PROTOCOLO.
//
// É o único status que **encerra** a repetição (seção 1.3 da API): ele indica
// transição impossível pela seção 7.4, que nenhuma repetição corrige. Repetir
// um 409 seria insistir num pedido que o outro lado já provou ser incoerente.
func ehViolacao(err error) bool {
	var comStatus erroComStatus
	if errors.As(err, &comStatus) {
		return comStatus.StatusHTTP() == 409
	}
	return errors.Is(err, dominio.ErrViolacaoProtocolo)
}
