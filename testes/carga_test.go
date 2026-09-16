package testes

// Teste de carga da seção 8.3 do PROJETO.md.
//
// Não é um teste de corretude, e por isso não roda na suíte normal: mede
// latência e vazão com N = 1, 10, 50 e 100 clientes simultâneos, e só executa
// quando pedido com VAIJUNTO_CARGA=1. Precisa rodar **sem** -race: o detector
// de corrida multiplica o custo de cada acesso à memória, e o número medido
// seria o do detector, e não o do servidor. A corretude sob concorrência é
// responsabilidade dos testes T1 a T8, que rodam com ele.
//
// Cada cliente repete a sessão de um passageiro do menu (D15): busca, reserva o
// itinerário encontrado, lista as reservas e cancela. São duas leituras e duas
// escritas por ciclo, todas passando pelo mesmo mutex global (D04).
//
// Configuração por variável de ambiente:
//
//	VAIJUNTO_CARGA=1               habilita o teste
//	VAIJUNTO_CARGA_ENDERECO=ip:porta mede um servidor já no ar (contêiner, outra
//	                               máquina), que precisa ser reiniciado antes de
//	                               cada rodada; vazio sobe um servidor novo no
//	                               próprio processo
//	VAIJUNTO_CARGA_DURACAO=5s      duração de cada ponto da curva
//	VAIJUNTO_CARGA_ROTULO=nome     identifica a rodada no CSV
//
// O resultado sai em tabela no log e em resultados/carga-<rotulo>-<instante>.csv.

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"vaijunto/internal/protocolo"
)

// clientesDaCurva são os pontos pedidos pela seção 8.3.
var clientesDaCurva = []int{1, 10, 50, 100}

// pontoDeCarga é o resultado da medição com um número de clientes.
type pontoDeCarga struct {
	clientes    int
	decorrido   time.Duration
	requisicoes int
	recusas     int
	media       time.Duration
	p50         time.Duration
	p95         time.Duration
	p99         time.Duration
}

func (p pontoDeCarga) vazao() float64 {
	return float64(p.requisicoes) / p.decorrido.Seconds()
}

// TestCarga mede a curva de latência e vazão da seção 8.3.
func TestCarga(t *testing.T) {
	if os.Getenv("VAIJUNTO_CARGA") != "1" {
		t.Skip("teste de carga: habilite com VAIJUNTO_CARGA=1 e rode sem -race (PROJETO.md, seção 8.3)")
	}

	remoto := os.Getenv("VAIJUNTO_CARGA_ENDERECO")
	duracao := duracaoDoAmbiente(t, "VAIJUNTO_CARGA_DURACAO", 5*time.Second)
	rotulo := os.Getenv("VAIJUNTO_CARGA_ROTULO")
	if rotulo == "" {
		rotulo = "processo"
		if remoto != "" {
			rotulo = "remoto"
		}
	}

	// Um servidor por rodada, com os pontos medidos em sequência sobre ele, nos
	// dois modos. A latência cresce com o histórico de reservas (seção 8.3), e
	// um servidor remoto não tem como ser zerado entre os pontos; subir um
	// servidor novo a cada ponto só no modo local faria as duas curvas serem
	// medidas de jeitos diferentes, e a comparação entre elas perderia sentido.
	endereco := remoto
	if endereco == "" {
		endereco = subirServidor(t)
	}

	var pontos []pontoDeCarga
	for indice, clientes := range clientesDaCurva {
		t.Run(fmt.Sprintf("N=%d", clientes), func(t *testing.T) {
			// Cada ponto usa uma faixa de dias só dela: o estado sobrevive entre
			// os pontos, e dias repetidos fariam a busca encontrar as caronas do
			// ponto anterior.
			primeiroDia := 60 + 200*indice
			pontos = append(pontos, medirCarga(t, endereco, clientes, duracao, primeiroDia))
		})
	}

	registrarCarga(t, rotulo, pontos)
}

// medirCarga mede um ponto da curva: publica uma carona por cliente, autentica
// as conexões, libera todas de uma vez e repete o ciclo até o fim da duração.
//
// Cada cliente tem a sua carona, num dia só dela, e isso é deliberado. O que se
// mede é a disputa pelo mutex global, e não pelo assento, que é o assunto do
// T1. Dias distintos também evitam CONFLITO_HORARIO quando, acima de 50
// clientes, duas conexões autenticam o mesmo passageiro de teste (a identidade
// é da conexão, D08).
func medirCarga(t *testing.T, endereco string, clientes int, duracao time.Duration, primeiroDia int) pontoDeCarga {
	t.Helper()

	fuso := time.FixedZone("-03:00", -3*60*60)
	hoje := time.Now().In(fuso)

	motorista := conectar(t, endereco)
	motorista.entrar("joao", "1234")
	caronas := make([]string, clientes)
	partidas := make([]time.Time, clientes)
	for i := range caronas {
		dia := hoje.AddDate(0, 0, primeiroDia+i)
		partida := time.Date(dia.Year(), dia.Month(), dia.Day(), 8, 0, 0, 0, fuso)
		var publicada protocolo.PublicarCaronaResposta
		motorista.exigirOK(protocolo.TipoPublicarCarona, publicacao(10, []int{3000, 4500},
			parada("Salvador", partida),
			parada("Feira de Santana", partida.Add(2*time.Hour)),
			parada("Jequié", partida.Add(5*time.Hour))), &publicada)
		caronas[i], partidas[i] = publicada.CaronaID, partida
	}

	passageiros := passageirosDaCarga()[3:]
	conexoes := make([]*cliente, clientes)
	for i := range conexoes {
		p := passageiros[i%len(passageiros)]
		conexoes[i] = conectar(t, endereco)
		conexoes[i].entrar(p.usuario, p.senha)
	}

	latencias := make([][]time.Duration, clientes)
	recusas := make([]int, clientes)
	largada := make(chan struct{})
	var espera sync.WaitGroup
	var fim time.Time

	for i, c := range conexoes {
		espera.Add(1)
		go func() {
			defer espera.Done()
			<-largada
			for time.Now().Before(fim) {
				falhas, err := cicloDePassageiro(c, caronas[i], partidas[i], &latencias[i])
				recusas[i] += falhas
				if err != nil {
					t.Errorf("cliente %d: %v", i, err)
					return
				}
			}
		}()
	}

	inicio := time.Now()
	fim = inicio.Add(duracao)
	close(largada)
	espera.Wait()

	ponto := resumirCarga(clientes, time.Since(inicio), latencias, recusas)
	if ponto.recusas > 0 {
		t.Errorf("%d requisições recusadas: nenhuma operação desta carga deveria ser recusada", ponto.recusas)
	}
	return ponto
}

// cicloDePassageiro faz uma sessão de passageiro sobre a carona do cliente e
// devolve quantas requisições o servidor recusou. O erro é reservado para falha
// de transporte, que encerra o cliente.
//
// Reserva o itinerário que a busca devolveu, e não um montado à mão, como faz o
// cliente de menu (D15). Um ciclo que falha no meio para ali: sem reserva, não
// há o que listar nem cancelar.
func cicloDePassageiro(c *cliente, carona string, partida time.Time, latencias *[]time.Duration) (int, error) {
	var busca protocolo.BuscarItinerariosResposta
	ok, err := medir(c, protocolo.TipoBuscarItinerarios, protocolo.BuscarItinerariosRequisicao{
		Origem: "Salvador", Destino: "Jequié", Data: partida.Format("2006-01-02"),
	}, &busca, latencias)
	if err != nil || !ok {
		return 1, err
	}

	var itens []protocolo.ItemReserva
	for _, it := range busca.Itinerarios {
		if len(it.Trechos) == 1 && it.Trechos[0].CaronaID == carona {
			itens = []protocolo.ItemReserva{{CaronaID: carona, De: it.Trechos[0].De, Ate: it.Trechos[0].Ate}}
			break
		}
	}
	if itens == nil {
		return 0, fmt.Errorf("a busca não devolveu a carona %s do próprio cliente", carona)
	}

	var confirmada protocolo.ReservarResposta
	ok, err = medir(c, protocolo.TipoReservar, protocolo.ReservarRequisicao{Trechos: itens}, &confirmada, latencias)
	if err != nil || !ok {
		return 1, err
	}

	falhas := 0
	ok, err = medir(c, protocolo.TipoListarMinhasReservas, protocolo.ListarMinhasReservasRequisicao{}, nil, latencias)
	if err != nil {
		return falhas, err
	}
	if !ok {
		falhas++
	}
	ok, err = medir(c, protocolo.TipoCancelarReserva,
		protocolo.CancelarReservaRequisicao{ReservaID: confirmada.ReservaID}, nil, latencias)
	if err != nil {
		return falhas, err
	}
	if !ok {
		falhas++
	}
	return falhas, nil
}

// medir envia uma requisição, registra a latência e decodifica a resposta.
// Devolve se ela foi aceita; o erro é só para falha de transporte.
//
// O relógio envolve apenas a escrita no socket e a leitura da resposta: montar
// o JSON antes e decodificá-lo depois é custo do cliente, e não do servidor. O
// id ecoado é conferido em toda requisição (seção 8.3): é ele que garante que a
// latência registrada é a desta requisição, e não a de outra.
func medir(c *cliente, tipo string, dados any, destino any, latencias *[]time.Duration) (bool, error) {
	c.seq++
	id := fmt.Sprintf("carga-%d", c.seq)

	corpo, err := json.Marshal(dados)
	if err != nil {
		return false, fmt.Errorf("montar dados de %s: %w", tipo, err)
	}
	linha, err := json.Marshal(protocolo.Requisicao{ID: id, Tipo: tipo, Dados: corpo})
	if err != nil {
		return false, fmt.Errorf("montar envelope de %s: %w", tipo, err)
	}
	linha = append(linha, '\n')
	if err := c.conn.SetDeadline(time.Now().Add(prazoLeitura)); err != nil {
		return false, fmt.Errorf("prazo de %s: %w", tipo, err)
	}

	envio := time.Now()
	if _, err := c.conn.Write(linha); err != nil {
		return false, fmt.Errorf("enviar %s: %w", tipo, err)
	}
	bruta, err := c.leitor.LerLinha()
	recebimento := time.Now()
	if err != nil {
		return false, fmt.Errorf("ler resposta de %s: %w", tipo, err)
	}
	*latencias = append(*latencias, recebimento.Sub(envio))

	resp, err := protocolo.DecodificarResposta(bruta)
	if err != nil || resp.ID != id {
		return false, fmt.Errorf("resposta %q não corresponde à requisição %s", bruta, id)
	}
	if resp.Status != protocolo.StatusOK {
		return false, nil
	}
	if destino != nil {
		if err := json.Unmarshal(resp.Dados, destino); err != nil {
			return false, fmt.Errorf("decodificar dados de %s: %w", tipo, err)
		}
	}
	return true, nil
}

// resumirCarga junta as latências de todos os clientes e calcula média e
// percentis. O percentil é o do posto mais próximo: o p95 é a menor latência
// que cobre 95% das requisições.
func resumirCarga(clientes int, decorrido time.Duration, latencias [][]time.Duration, recusas []int) pontoDeCarga {
	ponto := pontoDeCarga{clientes: clientes, decorrido: decorrido}
	for _, r := range recusas {
		ponto.recusas += r
	}

	var todas []time.Duration
	for _, l := range latencias {
		todas = append(todas, l...)
	}
	ponto.requisicoes = len(todas)
	if len(todas) == 0 {
		return ponto
	}

	sort.Slice(todas, func(i, j int) bool { return todas[i] < todas[j] })
	var soma time.Duration
	for _, l := range todas {
		soma += l
	}
	percentil := func(q float64) time.Duration {
		return todas[int(math.Ceil(q*float64(len(todas))))-1]
	}

	ponto.media = soma / time.Duration(len(todas))
	ponto.p50 = percentil(0.50)
	ponto.p95 = percentil(0.95)
	ponto.p99 = percentil(0.99)
	return ponto
}

// registrarCarga mostra a curva no log e a grava em CSV, para os gráficos do
// relatório e a comparação entre rodadas.
func registrarCarga(t *testing.T, rotulo string, pontos []pontoDeCarga) {
	t.Helper()

	ms := func(d time.Duration) string {
		return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 3, 64)
	}

	var tabela strings.Builder
	fmt.Fprintf(&tabela, "\n%8s %12s %14s %11s %9s %9s %9s %8s\n",
		"clientes", "requisições", "vazão (req/s)", "média (ms)", "p50 (ms)", "p95 (ms)", "p99 (ms)", "recusas")
	for _, p := range pontos {
		fmt.Fprintf(&tabela, "%8d %12d %14.0f %11s %9s %9s %9s %8d\n",
			p.clientes, p.requisicoes, p.vazao(), ms(p.media), ms(p.p50), ms(p.p95), ms(p.p99), p.recusas)
	}
	t.Log(tabela.String())

	diretorio := filepath.Join("..", "resultados")
	if err := os.MkdirAll(diretorio, 0o755); err != nil {
		t.Fatalf("criar %s: %v", diretorio, err)
	}
	caminho := filepath.Join(diretorio, fmt.Sprintf("carga-%s-%s.csv", rotulo, time.Now().Format("20060102-150405")))
	arquivo, err := os.Create(caminho)
	if err != nil {
		t.Fatalf("criar %s: %v", caminho, err)
	}
	defer arquivo.Close()

	escritor := csv.NewWriter(arquivo)
	linhas := [][]string{{"rotulo", "clientes", "duracao_s", "requisicoes", "recusas",
		"vazao_req_s", "media_ms", "p50_ms", "p95_ms", "p99_ms"}}
	for _, p := range pontos {
		linhas = append(linhas, []string{
			rotulo,
			strconv.Itoa(p.clientes),
			strconv.FormatFloat(p.decorrido.Seconds(), 'f', 3, 64),
			strconv.Itoa(p.requisicoes),
			strconv.Itoa(p.recusas),
			strconv.FormatFloat(p.vazao(), 'f', 1, 64),
			ms(p.media), ms(p.p50), ms(p.p95), ms(p.p99),
		})
	}
	if err := escritor.WriteAll(linhas); err != nil {
		t.Fatalf("gravar %s: %v", caminho, err)
	}
	t.Logf("resultados gravados em %s", caminho)
}
