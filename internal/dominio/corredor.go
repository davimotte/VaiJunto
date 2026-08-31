package dominio

import (
	"fmt"
	"time"
)

// Corredor fixo de cidades (D09, PROTOCOL.md seção 3.1). Caronas percorrem o
// corredor nos dois sentidos, com durações simétricas: viajar de uma cidade
// à vizinha gasta o mesmo tempo em qualquer direção.
var cidadesCorredor = []string{
	"Salvador",
	"Feira de Santana",
	"Jequié",
	"Vitória da Conquista",
}

// duracaoAteProxima[i] é a duração entre cidadesCorredor[i] e
// cidadesCorredor[i+1]. len(duracaoAteProxima) == len(cidadesCorredor)-1.
var duracaoAteProxima = []time.Duration{
	2 * time.Hour,
	3 * time.Hour,
	2*time.Hour + 30*time.Minute,
}

// IndiceCidade devolve a posição de nome no corredor. A comparação é por
// igualdade exata de string: os clientes oficiais escolhem a cidade em um
// menu enumerado (não digitam o nome), então a grafia que chega ao
// servidor é sempre a canônica do corredor.
func IndiceCidade(nome string) (int, bool) {
	for i, cidade := range cidadesCorredor {
		if cidade == nome {
			return i, true
		}
	}
	return 0, false
}

// ErroDominio é um erro de validação de domínio que carrega o código de erro
// do PROTOCOL.md (seção 6). O domínio não monta resposta de protocolo: a
// camada de roteamento em internal/servidor lê Codigo e traduz para o campo
// "codigo" da Resposta.
type ErroDominio struct {
	Codigo   string
	Mensagem string
}

func (e *ErroDominio) Error() string { return e.Mensagem }

// Códigos de erro produzidos pelo domínio (PROTOCOL.md, seção 6). Os valores
// coincidem com as constantes de internal/protocolo por definição — o
// domínio não importa esse pacote (não conhece a rede) e por isso mantém sua
// própria cópia.
const (
	CodigoCidadeDesconhecida = "CIDADE_DESCONHECIDA"
	CodigoRotaInvalida       = "ROTA_INVALIDA"
)

// DerivarRotaEHorarios calcula, a partir de origem, destino e do instante de
// partida, a sequência de cidades percorridas e o horário de chegada em cada
// uma (D09): o motorista informa só origem, destino e partida, e o servidor
// deriva o resto do corredor.
func DerivarRotaEHorarios(origem, destino string, partida time.Time) ([]string, []time.Time, error) {
	io, ok := IndiceCidade(origem)
	if !ok {
		return nil, nil, &ErroDominio{Codigo: CodigoCidadeDesconhecida, Mensagem: fmt.Sprintf("cidade desconhecida: %q", origem)}
	}
	id, ok := IndiceCidade(destino)
	if !ok {
		return nil, nil, &ErroDominio{Codigo: CodigoCidadeDesconhecida, Mensagem: fmt.Sprintf("cidade desconhecida: %q", destino)}
	}
	if io == id {
		return nil, nil, &ErroDominio{Codigo: CodigoRotaInvalida, Mensagem: "origem e destino não podem ser a mesma cidade"}
	}

	passo := 1
	n := id - io + 1
	if id < io {
		passo = -1
		n = io - id + 1
	}

	rota := make([]string, n)
	horarios := make([]time.Time, n)
	rota[0] = cidadesCorredor[io]
	horarios[0] = partida

	atual := io
	instante := partida
	for k := 1; k < n; k++ {
		proximo := atual + passo
		duracao := duracaoAteProxima[min(atual, proximo)]
		instante = instante.Add(duracao)
		rota[k] = cidadesCorredor[proximo]
		horarios[k] = instante
		atual = proximo
	}
	return rota, horarios, nil
}
