package transacao

import (
	"context"

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

// RespostaPreparar é o voto de um participante, já na forma em que o
// coordenador o consome.
//
// Motivo é o código da seção 5.2 da API. Ele é string, e não um tipo desta
// camada, porque quem o produz é a borda: a tradução de erro de domínio para
// vocabulário de protocolo acontece numa tabela única em internal/federacao
// (seção 5.5), e as duas implementações da Rede passam por ela. Quando o
// coordenador da fase 5 precisar distinguir PRECEDENCIA das demais recusas para
// decidir nova tentativa (D36), a constante correspondente nasce aqui.
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
