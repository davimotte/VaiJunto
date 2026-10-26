package simulador

import (
	"testing"
	"time"

	"vaijunto/internal/protocolo"
)

func amostras(operacao string, codigo string, latencias ...time.Duration) []amostra {
	lista := make([]amostra, 0, len(latencias))
	for _, l := range latencias {
		lista = append(lista, amostra{operacao: operacao, codigo: codigo, latencia: l})
	}
	return lista
}

// TestPercentilEOPostoMaisProximo fixa o método do P1 (PROJETO.md, seção 8.3):
// o p95 é a menor latência que cobre 95% das requisições, sem interpolação.
func TestPercentilEOPostoMaisProximo(t *testing.T) {
	ordenadas := make([]time.Duration, 0, 100)
	for i := 1; i <= 100; i++ {
		ordenadas = append(ordenadas, time.Duration(i)*time.Millisecond)
	}

	casos := []struct {
		q        float64
		esperado time.Duration
	}{
		{0.50, 50 * time.Millisecond},
		{0.95, 95 * time.Millisecond},
		{0.99, 99 * time.Millisecond},
		{1.00, 100 * time.Millisecond},
	}
	for _, caso := range casos {
		if obtido := percentil(ordenadas, caso.q); obtido != caso.esperado {
			t.Errorf("percentil(%v) = %v, queria %v", caso.q, obtido, caso.esperado)
		}
	}
}

// TestPercentilNasBordas garante que o posto nunca sai da fatia: com uma
// amostra só, todo percentil é ela, e com fatia vazia o resultado é zero.
func TestPercentilNasBordas(t *testing.T) {
	uma := []time.Duration{7 * time.Millisecond}
	for _, q := range []float64{0.50, 0.95, 0.99} {
		if obtido := percentil(uma, q); obtido != 7*time.Millisecond {
			t.Errorf("percentil(%v) com uma amostra = %v, queria 7ms", q, obtido)
		}
	}
	if obtido := percentil(nil, 0.95); obtido != 0 {
		t.Errorf("percentil de fatia vazia = %v, queria 0", obtido)
	}
}

// TestResumirSeparaPorOperacao é a diferença central em relação ao teste de
// carga do P1, que juntava todas as operações num pool só: a seção 11.4 pede
// p50, p95 e p99 **por operação**.
func TestResumirSeparaPorOperacao(t *testing.T) {
	primeiro := &coletor{}
	primeiro.amostras = append(primeiro.amostras,
		amostras(OpBuscar, protocolo.StatusOK, 1*time.Millisecond, 3*time.Millisecond)...)
	segundo := &coletor{}
	segundo.amostras = append(segundo.amostras,
		amostras(OpReservar, protocolo.StatusOK, 20*time.Millisecond)...)
	segundo.amostras = append(segundo.amostras,
		amostras(OpReservar, protocolo.CodigoSemAssento, 40*time.Millisecond)...)

	ponto := resumir(2, map[string]int{"vaijunto": 1, "rotacapixaba": 1}, time.Second,
		[]*coletor{primeiro, segundo})

	if len(ponto.Operacoes) != len(operacoesMedidas) {
		t.Fatalf("resumo com %d operações, queria %d", len(ponto.Operacoes), len(operacoesMedidas))
	}
	porNome := make(map[string]ResumoOperacao, len(ponto.Operacoes))
	for _, resumo := range ponto.Operacoes {
		porNome[resumo.Operacao] = resumo
	}

	if busca := porNome[OpBuscar]; busca.Requisicoes != 2 || busca.Media != 2*time.Millisecond {
		t.Errorf("buscar: %d requisições e média %v, queria 2 e 2ms", busca.Requisicoes, busca.Media)
	}
	if reserva := porNome[OpReservar]; reserva.Requisicoes != 2 || reserva.P50 != 20*time.Millisecond {
		t.Errorf("reservar: %d requisições e p50 %v, queria 2 e 20ms", reserva.Requisicoes, reserva.P50)
	}
	// Uma operação que não aconteceu no ponto sai com zero, e não some da
	// tabela: a linha vazia também é informação.
	if vazia := porNome[OpBuscarVazia]; vazia.Requisicoes != 0 || vazia.P95 != 0 {
		t.Errorf("buscar_vazia: %d requisições e p95 %v, queria 0 e 0", vazia.Requisicoes, vazia.P95)
	}

	if ponto.Total.Requisicoes != 4 {
		t.Errorf("total com %d requisições, queria 4", ponto.Total.Requisicoes)
	}
	if ponto.Vazao() != 4 {
		t.Errorf("vazão %v, queria 4 req/s em 1 s", ponto.Vazao())
	}
}

// TestResumirContaRespostaAceitaComoCodigo fixa que a contagem por código é
// uma distribuição sobre **todas** as respostas, e não só sobre as recusas:
// sem a linha do OK, a tabela não diz quantas reservas de fato confirmaram.
func TestResumirContaRespostaAceitaComoCodigo(t *testing.T) {
	c := &coletor{}
	c.registrar(OpReservar, protocolo.Resposta{Status: protocolo.StatusOK}, time.Millisecond)
	c.registrar(OpReservar, protocolo.Resposta{
		Status: protocolo.StatusErro,
		Codigo: protocolo.CodigoDisputaPerdida,
	}, 2*time.Millisecond)
	c.registrar(OpReservar, protocolo.Resposta{
		Status: protocolo.StatusErro,
		Codigo: protocolo.CodigoDisputaPerdida,
	}, 3*time.Millisecond)

	ponto := resumir(1, map[string]int{"vaijunto": 1}, time.Second, []*coletor{c})
	esperado := map[string]int{
		protocolo.StatusOK:             1,
		protocolo.CodigoDisputaPerdida: 2,
	}
	for codigo, quantidade := range esperado {
		if obtido := ponto.Total.PorCodigo[codigo]; obtido != quantidade {
			t.Errorf("código %s contado %d vez(es), queria %d", codigo, obtido, quantidade)
		}
	}
	if len(ponto.Total.PorCodigo) != len(esperado) {
		t.Errorf("contagem com %d códigos, queria %d: %v", len(ponto.Total.PorCodigo), len(esperado), ponto.Total.PorCodigo)
	}
}

// TestResumosNaoEscreveEmOperacoes protege o detalhe que faria a tabela e o
// CSV divergirem: anexar o total à própria fatia de operações escreveria no
// array dela na segunda chamada.
func TestResumosNaoEscreveEmOperacoes(t *testing.T) {
	ponto := resumir(1, map[string]int{"vaijunto": 1}, time.Second, []*coletor{{}})

	primeira := ponto.resumos()
	segunda := ponto.resumos()
	if len(primeira) != len(operacoesMedidas)+1 || len(segunda) != len(operacoesMedidas)+1 {
		t.Fatalf("resumos com %d e %d entradas, queria %d nas duas",
			len(primeira), len(segunda), len(operacoesMedidas)+1)
	}
	for i, resumo := range ponto.Operacoes {
		if resumo.Operacao != operacoesMedidas[i] {
			t.Errorf("operação %d virou %q, queria %q", i, resumo.Operacao, operacoesMedidas[i])
		}
	}
}
