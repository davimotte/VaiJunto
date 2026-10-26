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
type Rede interface {
	// Preparar é a fase 1 do 2PC (seção 4.3 da API). O txID vai dentro do
	// pedido; é a implementação HTTP que o põe no caminho da URL.
	Preparar(ctx context.Context, destino string, pedido dominio.PedidoPreparacao) (RespostaPreparar, error)

	// Confirmar é a fase 2 com decisão de confirmar (seção 4.4 da API).
	Confirmar(ctx context.Context, destino, txID string) (dominio.Situacao, error)

	// Abortar é a fase 2 com decisão de abortar (seção 4.5 da API).
	Abortar(ctx context.Context, destino, txID string) (dominio.Situacao, error)

	// Consultar pergunta a situação de uma transação (seção 4.6 da API), que é
	// a base da terminação cooperativa (D38). Quem a chama é o Terminador, não
	// o coordenador: é o participante em dúvida que precisa descobrir a
	// decisão, e não o contrário.
	//
	// É a única operação de transação aceita de qualquer empresa do consórcio
	// (seção 1.2 da API), porque quem é consultado sobre uma transação
	// desconhecida não tem como saber quem participa dela.
	Consultar(ctx context.Context, destino, txID string) (RespostaConsulta, error)

	// Cancelar é a decisão de cancelar da D39 (seção 4.7 da API): o
	// coordenador avisa um participante que a transação caiu. Diferente de
	// Confirmar/Abortar, não há disputa — o participante não pode recusar —,
	// mas a assinatura é a mesma porque o efeito do lado de quem recebe é
	// análogo: aplicar e devolver a situação resultante.
	Cancelar(ctx context.Context, destino, txID string) (dominio.Situacao, error)

	// AvisarCaronaCancelada é o aviso inverso da D39 (seção 4.8 da API; seção
	// 10.2 do PROJETO_P2.md): o dono de uma carona cancelada avisa o
	// coordenador de uma reserva atingida, que decide o cancelamento do
	// itinerário inteiro. Só existe entre empresas diferentes, por definição
	// — quem avisa nunca é o próprio coordenador —, e por isso não tem
	// equivalente em Votante. Devolve a situação da reserva ("CANCELADA").
	AvisarCaronaCancelada(ctx context.Context, destino, txID, caronaID string) (string, error)
}

// Papel diz em que qualidade um servidor respondeu à consulta (seção 4.6 da
// API). A empresa do prefixo do txID responde **sempre** como COORDENADOR,
// mesmo quando também é dona de algum trecho: é o papel que decide o destino
// da transação, e é esse que importa a quem perguntou.
type Papel string

const (
	PapelCoordenador  Papel = "COORDENADOR"
	PapelParticipante Papel = "PARTICIPANTE"
)

// SituacaoDesconhecida é o que um participante responde sobre uma transação
// que ele não guarda — depois de gravar o veto (D38).
//
// É string solta, e não uma constante de dominio.Situacao, de propósito: ela
// não é uma situação **guardada**, e sim uma resposta de protocolo. Nenhuma
// máquina de estados da seção 7.3 a produz, e inventá-la no domínio criaria um
// valor que o log nunca poderia conter.
const SituacaoDesconhecida = "DESCONHECIDA"

// RespostaConsulta é o que volta de um POST /transacoes/{tx}/consultar
// (seção 4.6 da API), já na forma em que o Terminador a consome.
//
// Os dois campos de conteúdo se excluem, e qual deles vale é dito pelo Papel:
// um coordenador preenche Decisao, um participante preenche Situacao. Ter os
// dois numa struct só, em vez de dois tipos, é o que permite ao Terminador
// fazer uma chamada e decidir depois — ele nem sempre sabe de antemão em que
// papel o destino vai responder.
type RespostaConsulta struct {
	Papel Papel

	// Decisao é o destino da transação, só quando Papel é COORDENADOR
	// (CONFIRMADA, EM_ANDAMENTO ou ABORTADA).
	Decisao dominio.Decisao

	// Situacao é a situação da participação, só quando Papel é PARTICIPANTE.
	// Vale SituacaoDesconhecida quando o destino não conhecia a transação e
	// acabou de vetá-la.
	Situacao string
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

	// Cancelar é o equivalente local de Rede.Cancelar — sem ctx e sem
	// destino, pelo mesmo motivo dos outros três: não há rede, logo não há
	// prazo de chamada nem par a resolver.
	Cancelar(txID string) (dominio.Situacao, error)
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
