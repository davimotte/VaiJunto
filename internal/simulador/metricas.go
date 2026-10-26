package simulador

import (
	"math"
	"sort"
	"time"

	"vaijunto/internal/protocolo"
)

// Nomes das operações medidas. São a chave da agregação e saem no CSV e na
// tabela, então ficam num lugar só.
//
// A busca aparece em duas linhas porque mede dois caminhos diferentes do mesmo
// tipo de requisição: OpBuscar é a busca por um par de cidades que a preparação
// publicou, e OpBuscarVazia é a busca por um par sorteado do catálogo, que
// consulta os mesmos pares e não acha nada. Misturá-las esconderia a diferença.
const (
	OpBuscar      = "buscar"
	OpBuscarVazia = "buscar_vazia"
	OpReservar    = "reservar"
	OpCancelar    = "cancelar"

	// OpTotal é o pool com todas as amostras do ponto. Existe porque o CSV do
	// P1 tem uma linha por ponto, sem separar operação (PROJETO.md, seção
	// 8.3): é esta linha que se compara diretamente com as curvas de lá.
	OpTotal = "total"
)

// operacoesMedidas é a ordem fixa das linhas da tabela e do CSV. Fixa para que
// duas rodadas saiam na mesma ordem e o diff entre dois CSVs seja legível.
var operacoesMedidas = []string{OpBuscar, OpBuscarVazia, OpReservar, OpCancelar}

// amostra é uma resposta medida: o que ela custou e o que ela respondeu.
type amostra struct {
	operacao string
	codigo   string
	latencia time.Duration
}

// coletor acumula as amostras de um passageiro virtual.
//
// Não tem sincronização, e isso é deliberado: cada passageiro tem o seu
// coletor, e o resumo só acontece depois do Wait. Um coletor compartilhado
// precisaria de mutex no caminho de cada requisição, e o número medido passaria
// a incluir a contenção do próprio instrumento.
type coletor struct{ amostras []amostra }

// registrar guarda uma resposta. O código de uma resposta aceita é o próprio
// StatusOK: a contagem por código da seção 11.4 é uma distribuição sobre todas
// as respostas, e não só sobre as recusas.
func (c *coletor) registrar(operacao string, resposta protocolo.Resposta, latencia time.Duration) {
	codigo := resposta.Codigo
	if resposta.Status == protocolo.StatusOK {
		codigo = protocolo.StatusOK
	}
	c.amostras = append(c.amostras, amostra{operacao: operacao, codigo: codigo, latencia: latencia})
}

// ResumoOperacao é a latência e a contagem de respostas de uma operação num
// ponto da curva.
type ResumoOperacao struct {
	Operacao    string
	Requisicoes int
	Media       time.Duration
	P50         time.Duration
	P95         time.Duration
	P99         time.Duration
	// PorCodigo conta as respostas por código, com StatusOK entre as chaves.
	PorCodigo map[string]int
}

// Ponto é o resultado da medição com um número de passageiros simultâneos.
type Ponto struct {
	// Passageiros é o total no consórcio, e PorEmpresa diz como ele foi
	// repartido (seção 11.6).
	Passageiros int
	PorEmpresa  map[string]int
	// Decorrido é o tempo real da medição, e não o pedido na flag.
	Decorrido time.Duration
	Operacoes []ResumoOperacao
	Total     ResumoOperacao
}

// Vazao é a vazão do ponto em requisições por segundo.
func (p Ponto) Vazao() float64 { return vazao(p.Total.Requisicoes, p.Decorrido) }

// resumos são as operações seguidas do total, na ordem em que a tabela e o CSV
// as mostram. Devolve uma fatia nova: anexar o total à própria Operacoes
// escreveria no array dela.
func (p Ponto) resumos() []ResumoOperacao {
	return append(append(make([]ResumoOperacao, 0, len(p.Operacoes)+1), p.Operacoes...), p.Total)
}

// resumir junta as amostras de todos os passageiros e calcula o ponto.
func resumir(passageiros int, porEmpresa map[string]int, decorrido time.Duration, coletores []*coletor) Ponto {
	ponto := Ponto{
		Passageiros: passageiros,
		PorEmpresa:  porEmpresa,
		Decorrido:   decorrido,
	}

	porOperacao := make(map[string][]amostra, len(operacoesMedidas))
	var todas []amostra
	for _, c := range coletores {
		for _, a := range c.amostras {
			porOperacao[a.operacao] = append(porOperacao[a.operacao], a)
			todas = append(todas, a)
		}
	}

	ponto.Operacoes = make([]ResumoOperacao, 0, len(operacoesMedidas))
	for _, operacao := range operacoesMedidas {
		ponto.Operacoes = append(ponto.Operacoes, resumirOperacao(operacao, porOperacao[operacao]))
	}
	ponto.Total = resumirOperacao(OpTotal, todas)
	return ponto
}

// resumirOperacao calcula média, percentis e contagem por código de um
// conjunto de amostras. Com zero amostras devolve o resumo zerado, e não um
// erro: uma operação que não aconteceu no ponto é informação, e a linha sai com
// requisições 0.
func resumirOperacao(operacao string, amostras []amostra) ResumoOperacao {
	resumo := ResumoOperacao{
		Operacao:    operacao,
		Requisicoes: len(amostras),
		PorCodigo:   make(map[string]int, 4),
	}
	if len(amostras) == 0 {
		return resumo
	}

	latencias := make([]time.Duration, 0, len(amostras))
	var soma time.Duration
	for _, a := range amostras {
		latencias = append(latencias, a.latencia)
		soma += a.latencia
		resumo.PorCodigo[a.codigo]++
	}
	sort.Slice(latencias, func(i, j int) bool { return latencias[i] < latencias[j] })

	resumo.Media = soma / time.Duration(len(latencias))
	resumo.P50 = percentil(latencias, 0.50)
	resumo.P95 = percentil(latencias, 0.95)
	resumo.P99 = percentil(latencias, 0.99)
	return resumo
}

// percentil é o do posto mais próximo, como no P1 (PROJETO.md, seção 8.3): o
// p95 é a menor latência que cobre 95% das requisições. Sem interpolação, para
// que o número do CSV seja sempre uma latência que de fato aconteceu.
func percentil(ordenadas []time.Duration, q float64) time.Duration {
	if len(ordenadas) == 0 {
		return 0
	}
	posto := int(math.Ceil(q*float64(len(ordenadas)))) - 1
	if posto < 0 {
		posto = 0
	}
	return ordenadas[posto]
}

func vazao(requisicoes int, decorrido time.Duration) float64 {
	if decorrido <= 0 {
		return 0
	}
	return float64(requisicoes) / decorrido.Seconds()
}
