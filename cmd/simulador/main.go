// Comando simulador gera carga de passageiros sobre as empresas do consórcio,
// falando o protocolo TCP como os clientes reais. É o entregável E4 do
// enunciado (PROJETO_P2.md, seção 11.4).
//
// Roda **sem** -race: o detector multiplica o custo de cada acesso à memória, e
// o número medido seria o dele, e não o do servidor. A corretude sob
// concorrência é dos testes de TR1 a TR5 e TF1 a TF10, que rodam com ele.
//
// Exemplos:
//
//	simulador --cenario=federado --passageiros=20 --duracao=60s
//	simulador --cenario=local --curva=1,10,50,100 --duracao=5s --rotulo=rede
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	_ "time/tzdata" // fusos embutidos no binário: a imagem Alpine não traz tzdata, e sem eles dominio.FusoDasCidades cairia no deslocamento fixo.

	"vaijunto/internal/simulador"
)

// Credenciais da semente (seção 12.1). São as mesmas nas três empresas, e por
// isso não viraram flag: o que muda de empresa para empresa é o nome do
// motorista, que --motoristas resolve.
const (
	senhaDoMotorista  = "1234"
	senhaDoPassageiro = "teste"
)

// motoristasDaSemente é o motorista que cada empresa usa na preparação
// (seção 12.1). Uma empresa fora desta tabela precisa de --motoristas.
var motoristasDaSemente = map[string]string{
	"vaijunto":       "joao",
	"rotacapixaba":   "rafael",
	"rotafluminense": "marcos",
}

func main() {
	listaEmpresas := flag.String("empresas", "vaijunto=localhost:9001,rotacapixaba=localhost:9002,rotafluminense=localhost:9003",
		"empresas e portas TCP, no formato empresa=host:porta,empresa=host:porta; o padrão é o docker-compose da seção 13.3")
	listaMotoristas := flag.String("motoristas", "", "motorista de cada empresa para a preparação, no formato empresa=login,empresa=login; vazio usa os da semente (seção 12.1)")
	cenario := flag.String("cenario", simulador.CenarioFederado,
		"local (itinerário de uma perna, da empresa do próprio passageiro) ou federado (uma perna em cada empresa); a diferença é o custo da coordenação distribuída (seção 11.6)")
	passageiros := flag.Int("passageiros", 20, "passageiros simultâneos por empresa, no máximo 50 (contas teste01..teste50 da semente)")
	listaCurva := flag.String("curva", "", "pontos da curva, em total de passageiros no consórcio, ex.: 1,10,50,100 (seção 11.6); vazio usa --passageiros")
	duracao := flag.Duration("duracao", 60*time.Second, "duração de cada ponto da curva")
	probabilidadeCancelamento := flag.Float64("probabilidade-cancelamento", 0.5,
		"chance de o passageiro cancelar a reserva no próprio ciclo; a que ele não cancela é cancelada no início do ciclo seguinte")
	fracaoBuscaVazia := flag.Float64("fracao-busca-vazia", 0.1,
		"fração das buscas com um par de cidades sorteado do catálogo inteiro, que consulta os pares e não acha nada")
	assentos := flag.Int("assentos", 2, "assentos de cada carona sintética; poucos, para forçar disputa")
	dias := flag.Int("dias", 0, "dias que as caronas sintéticas ocupam; 0 deriva de --assentos e do número de passageiros")
	diaBase := flag.Int("dia-base", 0, "distância em dias entre hoje e o primeiro dia do cenário; 0 deriva do relógio, para que duas rodadas não disputem os mesmos dias")
	semente := flag.Int64("semente", 1, "semente do sorteio; com a mesma semente, cada passageiro virtual faz as mesmas escolhas")
	prazo := flag.Duration("prazo", 5*time.Second, "prazo de cada requisição TCP; estourá-lo encerra o passageiro virtual")
	saida := flag.String("saida", "resultados", "diretório dos CSVs; vazio não grava nada")
	rotulo := flag.String("rotulo", "", "identifica a rodada no nome do arquivo; vazio usa o cenário")
	flag.Parse()

	empresas, err := montarEmpresas(*listaEmpresas, *listaMotoristas)
	if err != nil {
		log.Fatalf("simulador: --empresas/--motoristas: %v", err)
	}
	curva, err := analisarCurva(*listaCurva)
	if err != nil {
		log.Fatalf("simulador: --curva: %v", err)
	}

	resultado, err := simulador.Rodar(simulador.Config{
		Empresas:                  empresas,
		Cenario:                   *cenario,
		Passageiros:               *passageiros,
		Curva:                     curva,
		Duracao:                   *duracao,
		ProbabilidadeCancelamento: *probabilidadeCancelamento,
		FracaoBuscaVazia:          *fracaoBuscaVazia,
		Assentos:                  *assentos,
		Dias:                      *dias,
		DiaBase:                   *diaBase,
		Semente:                   *semente,
		Prazo:                     *prazo,
		Saida:                     *saida,
		Rotulo:                    *rotulo,
		Registro:                  os.Stdout,
	})
	if err != nil {
		log.Fatalf("simulador: %v", err)
	}
	if len(resultado.Pontos) == 0 {
		log.Fatalf("simulador: nenhum ponto medido")
	}
}

// montarEmpresas cruza a lista de endereços com a de motoristas.
//
// A ordem da lista é preservada, e importa duas vezes: ela decide como o total
// de passageiros de um ponto é repartido e, no cenário federado, qual perna da
// corrente cada empresa publica.
func montarEmpresas(listaEmpresas, listaMotoristas string) ([]simulador.Empresa, error) {
	enderecos, err := analisarAtribuicoes(listaEmpresas)
	if err != nil {
		return nil, err
	}
	if len(enderecos) == 0 {
		return nil, fmt.Errorf("nenhuma empresa informada")
	}
	motoristas, err := analisarAtribuicoes(listaMotoristas)
	if err != nil {
		return nil, err
	}

	empresas := make([]simulador.Empresa, 0, len(enderecos))
	for _, atribuicao := range enderecos {
		motorista := motoristasDaSemente[atribuicao.chave]
		for _, m := range motoristas {
			if m.chave == atribuicao.chave {
				motorista = m.valor
			}
		}
		if motorista == "" {
			return nil, fmt.Errorf("empresa %s não está na tabela da semente: informe o motorista em --motoristas", atribuicao.chave)
		}
		empresas = append(empresas, simulador.Empresa{
			Nome:            atribuicao.chave,
			Endereco:        atribuicao.valor,
			Motorista:       motorista,
			SenhaMotorista:  senhaDoMotorista,
			SenhaPassageiro: senhaDoPassageiro,
		})
	}
	return empresas, nil
}

// atribuicao é um par chave=valor de uma lista de flag, no mesmo formato de
// --pares do servidor (D20).
type atribuicao struct{ chave, valor string }

func analisarAtribuicoes(lista string) ([]atribuicao, error) {
	if strings.TrimSpace(lista) == "" {
		return nil, nil
	}
	var atribuicoes []atribuicao
	for _, item := range strings.Split(lista, ",") {
		chave, valor, achou := strings.Cut(strings.TrimSpace(item), "=")
		if !achou || strings.TrimSpace(chave) == "" || strings.TrimSpace(valor) == "" {
			return nil, fmt.Errorf("item %q: use o formato chave=valor", item)
		}
		atribuicoes = append(atribuicoes, atribuicao{
			chave: strings.TrimSpace(chave),
			valor: strings.TrimSpace(valor),
		})
	}
	return atribuicoes, nil
}

func analisarCurva(lista string) ([]int, error) {
	if strings.TrimSpace(lista) == "" {
		return nil, nil
	}
	var curva []int
	for _, item := range strings.Split(lista, ",") {
		ponto, err := strconv.Atoi(strings.TrimSpace(item))
		if err != nil {
			return nil, fmt.Errorf("ponto %q: %w", item, err)
		}
		curva = append(curva, ponto)
	}
	return curva, nil
}
