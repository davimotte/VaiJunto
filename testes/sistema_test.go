package testes

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"vaijunto/internal/simulador"
	"vaijunto/internal/transacao"
)

// TS1 — teste de sistema sob carga e com injeção de falhas (E5; PROJETO_P2.md,
// seção 11.5).
//
// É o único teste da suíte que sai do processo: as três empresas sobem em
// contêineres de verdade, com estado em arquivo, `os.Exit` de verdade e a rede
// no meio. Os testes multiempresa das outras fases sobem as três no mesmo
// processo (seção 11.3), e lá "derrubar uma empresa" é fechar um listener e
// abandonar uma goroutine. O que o TS1 acrescenta é justamente o que aquele
// atalho não prova: que o estado que sobrevive é o do **disco**, que a
// recuperação é o **boot** (seção 9.1) e que a precedência e os prazos
// continuam valendo quando o atraso é o da pilha de rede e não o de um canal.
//
// Três decisões desta fase, porque as três têm alternativa plausível:
//
//   - **o simulador roda em processo**, e não como um quarto contêiner. É o uso
//     que a seção 11.4 já previa ("é o que permite ao TS1 chamar
//     simulador.Rodar com as empresas no próprio processo"): ele fala só TCP,
//     e TCP contra uma porta publicada é igual de dentro ou de fora. Em troca,
//     o teste pode cronometrar a queda no meio da carga sem combinar nada com
//     um contêiner. O -race fica ligado, e aqui isso é inofensivo: a medição
//     da seção 11.6 é que não pode tê-lo, e esta rodada não mede nada —
//     `Saida` é vazia e nenhum CSV sai daqui;
//   - **`docker run` por empresa, e não `docker compose`.** É o comando da
//     seção 13.2, o do laboratório. O compose é de desenvolvimento e aponta
//     para `./estado/`, que é do usuário e não do teste; e um teste que
//     dependesse do compose passaria a falhar quando ele mudasse de porta;
//   - **religar é sempre sem `FALHAR_EM`.** A variável é do contêiner, não da
//     execução (limitação registrada na seção 14): religar com ela armada
//     mataria a empresa de novo no `RESERVAR` seguinte, que a carga ainda em
//     curso produziria em milissegundos. Por isso a rodada sabotada recria o
//     contêiner em vez de dar `docker start` nele — com o **mesmo diretório de
//     estado**, que é onde o estado mora.

// variavelSistema é a porteira do TS1. Sem ela, o teste pula: ele precisa do
// Docker, constrói uma imagem e leva dezenas de segundos, e `go test ./...`
// roda a toda hora.
const variavelSistema = "VAIJUNTO_SISTEMA"

// variavelDuracaoSistema encurta ou alonga a carga de cada rodada, como
// VAIJUNTO_T4_DURACAO faz com o T4.
const variavelDuracaoSistema = "VAIJUNTO_SISTEMA_DURACAO"

const (
	// imagemTS1 tem tag própria para nunca colidir com as do compose: um
	// `docker compose up` depois do teste não pode pegar a imagem dele, nem o
	// contrário.
	imagemTS1 = "vaijunto-ts1-servidor"

	// redeTS1 é a rede própria da rodada. É ela que dá o DNS pelo nome do
	// contêiner, do mesmo jeito que o compose dá pelo nome do serviço: os pares
	// se encontram em http://ts1-<empresa>:8080 (D20).
	redeTS1 = "vaijunto-ts1"

	// prefixoTS1 separa os contêineres do teste dos do compose, que o
	// desenvolvedor pode ter no ar ao mesmo tempo.
	prefixoTS1 = "ts1-"
)

const (
	// janelaDeQueda é quanto tempo a empresa fica fora do ar. Precisa ser maior
	// que --prazo-duvida para que o participante em PREPARADO chegue a duvidar
	// e a terminação cooperativa (D38) rode de verdade; se a empresa voltasse
	// antes disso, o teste exercitaria só a repetição da fase 2.
	janelaDeQueda = 3 * time.Second

	// tetoNoAr é a espera pela subida de um contêiner.
	tetoNoAr = 60 * time.Second

	// tetoDeQuiescencia é mais curto que o prazoDeQuiescenciaNoAmbiente de 90 s
	// porque aqui os prazos são os curtos de prazosDoTS1, e não os da D41.
	tetoDeQuiescencia = 30 * time.Second

	// intervaloDeEspera é o passo de todos os laços de espera deste arquivo.
	// Mais largo que os 5 ms do esperarQuiescenciaEm: cada volta aqui é uma
	// requisição HTTP a um contêiner, ou um `docker inspect`, que custa um
	// processo.
	intervaloDeEspera = 100 * time.Millisecond

	// prazoDeComando é o teto de um comando do Docker. Generoso para o
	// `docker build`, que no pior caso recompila tudo.
	prazoDeComando = 10 * time.Minute
)

// prazosDoTS1 encurta os prazos da D41, uma flag por prazo, como a seção 13.1
// prevê. Com os padrões (votação de 5 s, dúvida de 10 s), uma rodada do TS1
// gastaria mais tempo esperando prazo do que exercitando o protocolo.
//
// Nenhum deles é tão curto que vire falha onde não há: entre contêineres na
// mesma máquina, uma chamada a um par custa a pilha de rede local mais um
// fsync, e 500 ms são três ordens de grandeza disso.
var prazosDoTS1 = []string{
	"--prazo-chamada=500ms",
	"--prazo-busca=500ms",
	"--prazo-votacao=1s",
	"--prazo-aguarde=50ms",
	"--prazo-fase2=250ms",
	"--prazo-duvida=1s",
	"--prazo-consulta=250ms",
}

// empresaTS1 é uma empresa do consórcio em contêiner: o nome, e as duas portas
// que ela publica no host.
//
// As portas são **fixas**, e não efêmeras (`-p 0:9000`). Com porta efêmera, o
// `docker start` da empresa religada escolheria outra, e o verificador
// perderia justamente a empresa cuja recuperação o teste existe para observar.
// A faixa 29000/28000 fica longe da do compose (9001-9003 e 8081-8083) para
// que um ambiente de desenvolvimento no ar não atrapalhe.
type empresaTS1 struct {
	nome      string
	portaTCP  int
	portaHTTP int
}

var empresasTS1 = []empresaTS1{
	{nome: "vaijunto", portaTCP: 29001, portaHTTP: 28081},
	{nome: "rotacapixaba", portaTCP: 29002, portaHTTP: 28082},
	{nome: "rotafluminense", portaTCP: 29003, portaHTTP: 28083},
}

// motoristasDoTS1 é o motorista que cada empresa usa na preparação do cenário
// (seção 12.1).
//
// A tabela aparece aqui de novo, e não importada de cmd/simulador, porque
// package main não é importável. É a mesma duplicação que `go vet` não pega e
// que um teste pega: se a semente de uma empresa trocar de motorista, a
// preparação falha no login e a mensagem diz qual.
var motoristasDoTS1 = map[string]string{
	"vaijunto":       "joao",
	"rotacapixaba":   "rafael",
	"rotafluminense": "marcos",
}

// Credenciais da semente (seção 12.1), iguais às de cmd/simulador.
const (
	senhaDoMotoristaTS1  = "1234"
	senhaDoPassageiroTS1 = "teste"
)

func (e empresaTS1) conteiner() string { return prefixoTS1 + e.nome }

func (e empresaTS1) urlBase() string { return fmt.Sprintf("http://localhost:%d", e.portaHTTP) }

func (e empresaTS1) endereco() string { return fmt.Sprintf("localhost:%d", e.portaTCP) }

// rodadaTS1 é uma das duas formas de derrubar uma empresa que a seção 11.5
// exige.
//
// Os dois alvos são diferentes de propósito. O `docker kill` cai na
// rotafluminense, que é **participante** das transações coordenadas pelas
// outras duas: é a queda do dono de um trecho, detectada por timeout (D30). O
// ponto de falha cai na vaijunto, que é **coordenadora** das suas: é a queda
// com decisão já gravada, que é o caso em que o 2PC bloqueia de verdade
// (seção 9.6) e o único que deixa assento em espera em duas empresas ao mesmo
// tempo.
type rodadaTS1 struct {
	nome string
	alvo string
	// ponto vazio significa `docker kill`; preenchido, a empresa sobe com
	// FALHAR_EM e morre sozinha (D31).
	ponto transacao.Ponto
}

// ambienteTS1 é o consórcio em contêineres de uma rodada.
type ambienteTS1 struct {
	raiz   string
	estado string
}

// TestSistemaSobCargaEFalhas é o TS1 da seção 11.5.
//
// Duas rodadas, em sequência e não em paralelo: as portas publicadas são fixas,
// e duas rodadas ao mesmo tempo disputariam as mesmas.
func TestSistemaSobCargaEFalhas(t *testing.T) {
	if os.Getenv(variavelSistema) != "1" {
		t.Skipf("teste de sistema (TS1, seção 11.5): habilite com %s=1; precisa do Docker", variavelSistema)
	}

	raiz := raizDoRepositorio(t)
	exigirDocker(t)
	construirImagem(t, raiz)

	for _, rodada := range []rodadaTS1{
		{nome: "docker-kill", alvo: "rotafluminense"},
		{nome: "falhar-em", alvo: "vaijunto", ponto: transacao.PontoCoordAposDecidir},
	} {
		t.Run(rodada.nome, func(t *testing.T) { rodarTS1(t, raiz, rodada) })
	}
}

// rodarTS1 executa os passos da seção 11.5 numa rodada.
//
// O passo 5 — a carga limpa depois de religar — não está na lista da seção, e
// foi acrescentado por um motivo: as invariantes são afirmações sobre o estado
// parado, e uma empresa que religasse e recusasse **tudo** passaria em todas
// elas. A carga limpa é o que afirma que a empresa voltou a *servir*, e que um
// itinerário federado novo fecha depois da recuperação.
func rodarTS1(t *testing.T, raiz string, rodada rodadaTS1) {
	amb := subirConsorcioEmConteineres(t, raiz, rodada)
	duracao := duracaoDoAmbiente(t, variavelDuracaoSistema, 15*time.Second)
	urls := urlsDoTS1()

	// Passo 2: a carga. Vai para uma goroutine porque a queda tem de acontecer
	// no meio dela, e Rodar só volta quando o ponto termina.
	andamento := &registroDaCarga{}
	carga := make(chan error, 1)
	go func() {
		_, err := simulador.Rodar(configDoTS1(duracao, diaBaseDaCarga, 1, andamento))
		carga <- err
	}()

	// Passos 3 e 4: derrubar no meio da carga e religar com o mesmo estado.
	derrubar(t, rodada, duracao)
	t.Logf("a %s fica fora do ar por %s, mais que o --prazo-duvida de 1s", rodada.alvo, janelaDeQueda)
	time.Sleep(janelaDeQueda)
	religar(t, amb, rodada)

	err := esperarCarga(t, carga, duracao)
	andamento.despejar(t, "carga sob falha")
	conferirErroDaCarga(t, err, rodada.alvo)

	// Passo 5: a carga limpa, com as três no ar. Outro dia base, porque as
	// caronas da carga anterior continuam no log (D22) e o simulador recusa um
	// cenário sujo (seção 11.4).
	limpo := &registroDaCarga{}
	resultado, err := simulador.Rodar(configDoTS1(duracaoDaCargaLimpa, diaBaseDaCargaLimpa, 2, limpo))
	limpo.despejar(t, "carga limpa depois da recuperação")
	if err != nil {
		t.Fatalf("carga limpa depois de religar a %s: %v", rodada.alvo, err)
	}
	conferirCodigosDaCarga(t, resultado)
	conferirOperacoesDaCarga(t, resultado)

	// Passo 6: quiescência e verificação, com o verificador da fase 5.
	esperarQuiescenciaEm(t, tetoDeQuiescencia, urls)
	relatarPropriedades(t, rodada.nome, auditoriasDeURLs(t, urls))
}

const (
	// diaBaseDaCarga e diaBaseDaCargaLimpa são fixos, e não derivados do
	// relógio: cada rodada nasce com um diretório de estado novo, então não há
	// rodada anterior de quem fugir, e um dia fixo mantém o cenário igual entre
	// execuções. Os dois estão longe um do outro porque a carga limpa roda
	// sobre o log que a primeira deixou.
	diaBaseDaCarga      = 90
	diaBaseDaCargaLimpa = 300

	// duracaoDaCargaLimpa é curta: ela não mede nada, só precisa de ciclos
	// suficientes para que busca, reserva e cancelamento respondam OK ao menos
	// uma vez (conferirOperacoesDaCarga).
	duracaoDaCargaLimpa = 5 * time.Second

	// passageirosPorEmpresaNoTS1 com assentos escassos é o que põe os
	// passageiros em disputa de verdade pelos mesmos trechos.
	passageirosPorEmpresaNoTS1 = 5
	assentosNoTS1              = 2
)

// configDoTS1 aponta o simulador para as portas publicadas dos contêineres.
//
// Cenário federado: o itinerário tem uma perna em cada empresa, que é o único
// que faz o 2PC ter dois donos remotos — e sem isso a queda de uma empresa não
// atingiria transação nenhuma das outras.
func configDoTS1(duracao time.Duration, diaBase int, semente int64, registro *registroDaCarga) simulador.Config {
	empresas := make([]simulador.Empresa, 0, len(empresasTS1))
	for _, e := range empresasTS1 {
		empresas = append(empresas, simulador.Empresa{
			Nome:            e.nome,
			Endereco:        e.endereco(),
			Motorista:       motoristasDoTS1[e.nome],
			SenhaMotorista:  senhaDoMotoristaTS1,
			SenhaPassageiro: senhaDoPassageiroTS1,
		})
	}
	return simulador.Config{
		Empresas:                  empresas,
		Cenario:                   simulador.CenarioFederado,
		Passageiros:               passageirosPorEmpresaNoTS1,
		Duracao:                   duracao,
		ProbabilidadeCancelamento: 0.5,
		FracaoBuscaVazia:          0.1,
		Assentos:                  assentosNoTS1,
		DiaBase:                   diaBase,
		Semente:                   semente,
		Prazo:                     prazoLeitura,
		// Vazia: o TS1 não é medição, e um CSV saindo daqui iria para o mesmo
		// diretório dos números da seção 11.6, com os quais ele não se compara.
		Saida:    "",
		Registro: registro,
	}
}

func urlsDoTS1() map[string]string {
	urls := make(map[string]string, len(empresasTS1))
	for _, e := range empresasTS1 {
		urls[e.nome] = e.urlBase()
	}
	return urls
}

func empresaDoTS1(t *testing.T, nome string) empresaTS1 {
	t.Helper()
	for _, e := range empresasTS1 {
		if e.nome == nome {
			return e
		}
	}
	t.Fatalf("empresa %q não está em empresasTS1", nome)
	return empresaTS1{}
}

// derrubar executa o passo 3 da seção 11.5, de um jeito em cada rodada.
func derrubar(t *testing.T, rodada rodadaTS1, duracao time.Duration) {
	t.Helper()
	alvo := empresaDoTS1(t, rodada.alvo)

	if rodada.ponto == "" {
		// A queda tem de ser **no meio** da carga, e a carga começa publicando
		// dezenas de caronas: derrubar logo depois de disparar a goroutine
		// cairia na preparação, e o teste mediria a preparação falhando, não o
		// 2PC. Esperar a auditoria da alvo mostrar transação é o que torna
		// "no meio da carga" verdade sem cronômetro.
		esperarCargaChegar(t, alvo, duracao)
		t.Logf("derrubando a %s com docker kill, com a carga em curso", alvo.nome)
		docker(t, "kill", alvo.conteiner())
		esperarQuedaDoConteiner(t, alvo.conteiner(), 30*time.Second)
		return
	}

	// Com FALHAR_EM armado, quem derruba é o próprio servidor, no ponto exato
	// do protocolo (D31): não há `docker kill` cronometrado que acerte o
	// instante entre a DECISAO_CONFIRMAR gravada e o primeiro CONFIRMAR.
	//
	// Aqui **não** se espera a carga chegar antes: o ponto dispara dentro do
	// primeiro RESERVAR que a alvo coordena (internal/transacao/coordenador.go,
	// passo 8), e nessa hora ela já morreu — a auditoria dela não responde mais,
	// e esperá-la seria esperar para sempre. A morte é a própria prova de que a
	// carga chegou: nenhum outro caminho do servidor passa por este ponto.
	t.Logf("esperando a %s morrer sozinha em %s=%s (D31)", alvo.nome, transacao.VariavelFalharEm, rodada.ponto)
	esperarQuedaDoConteiner(t, alvo.conteiner(), duracao+tetoNoAr)
	t.Logf("a %s morreu no ponto armado; os participantes remotos ficaram em PREPARADO", alvo.nome)
}

// religar executa o passo 4: a empresa volta com o mesmo diretório de estado,
// e o boot é a recuperação (seção 9.1).
func religar(t *testing.T, amb ambienteTS1, rodada rodadaTS1) {
	t.Helper()
	alvo := empresaDoTS1(t, rodada.alvo)

	if rodada.ponto == "" {
		t.Logf("religando a %s com docker start, sobre o mesmo log", alvo.nome)
		docker(t, "start", alvo.conteiner())
	} else {
		// D31 e a limitação da fase 6B: a variável é do contêiner, não da
		// execução. Um `docker start` religaria com o ponto rearmado, e o
		// RESERVAR seguinte — que a carga em curso produz na hora — mataria a
		// empresa outra vez, agora sem ninguém esperando por isso. Por isso o
		// contêiner é recriado; o estado não é dele, é do diretório montado.
		t.Logf("recriando a %s sem %s, sobre o mesmo log", alvo.nome, transacao.VariavelFalharEm)
		dockerSilencioso("rm", "-f", alvo.conteiner())
		subirEmpresa(t, amb, alvo, "")
	}
	esperarNoAr(t, alvo)
	t.Logf("a %s voltou: o boot reaplicou o log e retomou as tarefas da seção 9.3", alvo.nome)
}

// esperarCarga espera o fim da rodada do simulador.
//
// O teto é generoso porque o que ele protege não é o caso normal: é o
// travamento. Um passageiro virtual preso num read sem resposta ficaria aí para
// sempre, e sem este select o sintoma seria o timeout do `go test`, sem dizer
// onde.
func esperarCarga(t *testing.T, carga <-chan error, duracao time.Duration) error {
	t.Helper()
	teto := duracao + 3*time.Minute
	select {
	case err := <-carga:
		return err
	case <-time.After(teto):
		t.Fatalf("a carga não terminou em %s", teto)
		return nil
	}
}

// conferirErroDaCarga é a decisão desta fase sobre o erro do simulador.
//
// Derrubar uma empresa derruba as sessões TCP dos passageiros **dela**: o ciclo
// devolve erro de transporte e medirPonto desiste do ponto. Os passageiros das
// outras duas continuam, e para eles a queda aparece como EMPRESA_INDISPONIVEL
// (D30), que é código de resposta e não erro. Logo, o erro é **esperado**, e é
// observação da falha e não falha do teste.
//
// O que não pode é tolerá-lo em branco: a verificação exige o prefixo
// "<empresa>:<conta>: " com que medirPonto identifica o passageiro que falhou.
// Assim, um erro da preparação, ou de um passageiro de uma empresa que nunca
// foi derrubada, continua reprovando — e esses seriam defeito de verdade.
func conferirErroDaCarga(t *testing.T, err error, derrubada string) {
	t.Helper()
	if err == nil {
		// Possível, e não é problema: se nenhum passageiro da empresa
		// derrubada tinha requisição em curso na janela, nenhuma sessão caiu.
		t.Logf("a carga terminou sem erro: nenhuma sessão da %s foi atingida na janela", derrubada)
		return
	}
	if !strings.HasPrefix(err.Error(), derrubada+":") {
		t.Fatalf("a carga falhou fora da empresa derrubada (%s): %v", derrubada, err)
	}
	t.Logf("a carga perdeu os passageiros da %s, como esperado: %v", derrubada, err)
}

// relatarPropriedades é o relatório final do TS1: uma linha por propriedade do
// item 5 do Produto do enunciado, com a invariante que a prova.
//
// Os quatro subtestes chamam as cinco funções do verificador da fase 5 — é
// exatamente o que verificarInvariantesP2 faz, e nada é verificado duas vezes.
// Estão separados só para que cada propriedade tenha o seu veredito no log: um
// "FALHOU" numa linha diz, sem ler mais nada, qual das três garantias do
// enunciado o consórcio quebrou.
func relatarPropriedades(t *testing.T, rodada string, todas map[string]auditoria) {
	t.Helper()
	propriedades := []struct {
		subteste    string
		invariantes string
		propriedade string
		conferir    func(*testing.T, map[string]auditoria)
	}{
		{"I-P2-1", "I-P2-1", "nenhum assento confirmado para dois passageiros", verificarIP21},
		{"I-P2-2-e-I-P2-5", "I-P2-2, I-P2-5", "nenhum itinerário confirmado pela metade",
			func(t *testing.T, todas map[string]auditoria) {
				verificarIP22(t, todas)
				verificarIP25(t, todas)
			}},
		{"I-P2-3", "I-P2-3", "nenhum assento bloqueado após a queda e a recuperação", verificarIP23},
		// A I-P2-4 não é uma das três propriedades do item 5, mas é parte do
		// verificador: deixá-la calada seria verificar menos do que a fase 5 já
		// verifica em processo.
		{"I-P2-4", "I-P2-4", "nenhuma empresa guarda dado de outra (D32)", verificarIP24},
	}
	for _, p := range propriedades {
		situacao := "FALHOU"
		if t.Run(p.subteste, func(t *testing.T) { p.conferir(t, todas) }) {
			situacao = "ok"
		}
		t.Logf("TS1 %s: %s (%s): %s", rodada, p.propriedade, p.invariantes, situacao)
	}
}

// registroDaCarga acumula o andamento que o simulador imprime, para despejá-lo
// no log do teste de uma vez.
//
// Não escreve direto com t.Logf porque a carga roda numa goroutine que pode
// sobreviver ao fim do teste quando uma asserção falha antes, e t.Logf depois
// do fim do teste é panic.
type registroDaCarga struct {
	mu    sync.Mutex
	texto strings.Builder
}

func (r *registroDaCarga) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.texto.Write(p)
}

func (r *registroDaCarga) despejar(t *testing.T, titulo string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.texto.Len() == 0 {
		return
	}
	t.Logf("%s:\n%s", titulo, strings.TrimRight(r.texto.String(), "\n"))
}

// --- Ambiente em contêineres --------------------------------------------------

// subirConsorcioEmConteineres põe as três empresas no ar, cada uma com os seus dois
// diretórios.
//
// Nenhum volume é compartilhado entre empresas (RD07, RD11): cada uma monta só
// o seu `dados/<empresa>` (somente leitura, é a semente) e só o seu diretório
// de estado. Dois diretórios de estado não têm como se misturar porque são dois
// subdiretórios distintos do t.TempDir desta rodada, que o testing apaga no
// fim.
func subirConsorcioEmConteineres(t *testing.T, raiz string, rodada rodadaTS1) ambienteTS1 {
	t.Helper()
	amb := ambienteTS1{raiz: raiz, estado: t.TempDir()}

	// Restos de uma execução interrompida com Ctrl-C envenenariam esta: o
	// `docker run` falharia no nome em uso, ou pior, a empresa subiria com o
	// log de outra rodada.
	limparRestos()

	docker(t, "network", "create", redeTS1)
	t.Cleanup(func() { dockerSilencioso("network", "rm", redeTS1) })

	for _, e := range empresasTS1 {
		ponto := transacao.Ponto("")
		if e.nome == rodada.alvo {
			ponto = rodada.ponto
		}
		subirEmpresa(t, amb, e, ponto)
	}

	// Registrado por último, e por isso o primeiro a rodar (os cleanups são
	// LIFO): o despejo precisa dos contêineres ainda existindo.
	t.Cleanup(func() {
		if t.Failed() {
			despejarLogs(t)
		}
	})

	for _, e := range empresasTS1 {
		esperarNoAr(t, e)
	}
	t.Logf("as três empresas estão no ar em contêineres, com --auditoria e os prazos curtos")
	return amb
}

// subirEmpresa é o `docker run` da seção 13.2, com as diferenças que o teste
// precisa: sem --rm, porque `docker kill` apagaria o contêiner e não haveria o
// que religar; e com a rede própria, que dá o DNS pelo nome do contêiner.
func subirEmpresa(t *testing.T, amb ambienteTS1, e empresaTS1, ponto transacao.Ponto) {
	t.Helper()

	dirEstado := filepath.Join(amb.estado, e.nome)
	if err := os.MkdirAll(dirEstado, 0o755); err != nil {
		t.Fatalf("diretório de estado de %s: %v", e.nome, err)
	}

	args := []string{
		"run", "--detach",
		"--name", e.conteiner(),
		"--network", redeTS1,
		"--publish", fmt.Sprintf("127.0.0.1:%d:9000", e.portaTCP),
		"--publish", fmt.Sprintf("127.0.0.1:%d:8080", e.portaHTTP),
		"--env", "TZ=America/Bahia",
		// --user, como no compose: sem ele o WAL nasceria com dono root no
		// diretório do host, e nem o teste nem o t.TempDir conseguiriam apagá-lo.
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--volume", filepath.Join(amb.raiz, "dados", e.nome) + ":/dados:ro",
		"--volume", dirEstado + ":/estado",
	}
	if ponto != "" {
		args = append(args, "--env", fmt.Sprintf("%s=%s", transacao.VariavelFalharEm, ponto))
	}
	args = append(args, imagemTS1,
		"--empresa="+e.nome,
		"--endereco=0.0.0.0:9000",
		"--endereco-pares=0.0.0.0:8080",
		"--dados=/dados",
		"--log=/estado/"+e.nome+".wal",
		"--pares="+paresDe(e.nome),
		"--auditoria",
		// Desligado porque o registro do D19 é uma escrita síncrona por
		// requisição que disputa o mutex do estado (PROJETO.md, seção 8.3): com
		// prazos de 500 ms, ele produziria timeout onde não há falha. As linhas
		// que importam aqui — boot, replay, retomada e a do ponto de falha — não
		// passam por esta flag, e despejarLogs as mostra quando a rodada falha.
		"--log-operacoes=false",
	)
	args = append(args, prazosDoTS1...)

	docker(t, args...)
	t.Cleanup(func() { dockerSilencioso("rm", "-f", e.conteiner()) })
}

// paresDe é a lista estática da D20 na visão de uma empresa: as outras duas,
// pelo nome do contêiner na porta interna 8080 — e não pela porta publicada,
// que só existe para o teste falar com elas de fora.
func paresDe(nome string) string {
	var pares []string
	for _, e := range empresasTS1 {
		if e.nome == nome {
			continue
		}
		pares = append(pares, fmt.Sprintf("%s=http://%s:8080", e.nome, e.conteiner()))
	}
	return strings.Join(pares, ",")
}

// esperarNoAr espera a empresa responder à auditoria.
//
// Usa tentarAuditoria, do verificador da fase 5, por dois motivos: é o mesmo
// caminho que o teste vai usar depois, então "no ar" quer dizer exatamente "o
// verificador consegue ler"; e a mensagem de erro dela já pergunta se a empresa
// subiu sem --auditoria, que é o engano mais provável aqui.
func esperarNoAr(t *testing.T, e empresaTS1) {
	t.Helper()
	limite := time.Now().Add(tetoNoAr)
	var ultimo error
	for time.Now().Before(limite) {
		if _, ultimo = tentarAuditoria(e.urlBase()); ultimo == nil {
			return
		}
		// Um contêiner que já saiu não vai responder nunca: falhar agora, com
		// o log dele, é muito melhor que esperar o teto inteiro.
		if !estaNoAr(t, e.conteiner()) {
			t.Fatalf("o contêiner %s saiu antes de atender:\n%s", e.conteiner(), logsDe(e.conteiner()))
		}
		time.Sleep(intervaloDeEspera)
	}
	t.Fatalf("a %s não atendeu /auditoria em %s: %v", e.nome, tetoNoAr, ultimo)
}

// esperarCargaChegar espera a carga produzir transação na empresa alvo.
//
// É o que faz "no meio da carga" ser verdade sem cronômetro: a preparação
// publica dezenas de caronas antes da primeira reserva, e esperar um tempo
// fixo acertaria o meio da carga por sorte. Participação é a visão de dono e
// reserva é a de coordenador; qualquer uma das duas prova que o 2PC já está
// passando por aqui.
func esperarCargaChegar(t *testing.T, e empresaTS1, duracao time.Duration) {
	t.Helper()
	teto := duracao + tetoNoAr
	limite := time.Now().Add(teto)
	for time.Now().Before(limite) {
		a, err := tentarAuditoria(e.urlBase())
		if err == nil && (len(a.Participacoes) > 0 || len(a.Reservas) > 0) {
			return
		}
		time.Sleep(intervaloDeEspera)
	}
	t.Fatalf("a carga não produziu transação nenhuma na %s em %s: a queda cairia fora da carga", e.nome, teto)
}

// esperarQuedaDoConteiner espera o contêiner sair.
func esperarQuedaDoConteiner(t *testing.T, conteiner string, teto time.Duration) {
	t.Helper()
	limite := time.Now().Add(teto)
	for time.Now().Before(limite) {
		if !estaNoAr(t, conteiner) {
			return
		}
		time.Sleep(intervaloDeEspera)
	}
	t.Fatalf("o contêiner %s não saiu em %s: o ponto de falha não foi alcançado pela carga", conteiner, teto)
}

func estaNoAr(t *testing.T, conteiner string) bool {
	t.Helper()
	return strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.Running}}", conteiner)) == "true"
}

// despejarLogs põe o terminal das três empresas no log do teste. Só é chamado
// quando a rodada falha: é lá que estão o boot, o replay, a retomada da fase 2
// e a linha do ponto de falha.
func despejarLogs(t *testing.T) {
	t.Helper()
	for _, e := range empresasTS1 {
		t.Logf("docker logs %s:\n%s", e.conteiner(), logsDe(e.conteiner()))
	}
}

func logsDe(conteiner string) string {
	saida, _ := rodarDocker("logs", "--tail=120", conteiner)
	return strings.TrimRight(saida, "\n")
}

// limparRestos apaga, em melhor esforço, o que uma execução anterior possa ter
// deixado.
func limparRestos() {
	for _, e := range empresasTS1 {
		dockerSilencioso("rm", "-f", e.conteiner())
	}
	dockerSilencioso("network", "rm", redeTS1)
}

// --- A CLI do Docker ----------------------------------------------------------

func exigirDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("o TS1 precisa da CLI do Docker no PATH: %v", err)
	}
	if saida, err := rodarDocker("version", "--format", "{{.Server.Version}}"); err != nil {
		t.Fatalf("o daemon do Docker não respondeu: %v\n%s", err, saida)
	}
}

// construirImagem constrói a imagem do servidor com a tag do teste.
//
// Constrói em vez de exigir uma imagem pronta para que o TS1 teste o código
// desta árvore, e não o que sobrou do último `docker compose up`.
func construirImagem(t *testing.T, raiz string) {
	t.Helper()
	t.Logf("construindo a imagem %s a partir de Dockerfile.servidor", imagemTS1)
	docker(t, "build", "--file", filepath.Join(raiz, "Dockerfile.servidor"), "--tag", imagemTS1, raiz)
}

// docker roda um comando da CLI e falha o teste com a saída combinada.
func docker(t *testing.T, args ...string) string {
	t.Helper()
	saida, err := rodarDocker(args...)
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, saida)
	}
	return saida
}

// dockerSilencioso é a versão de melhor esforço, para a limpeza: apagar o que
// já não existe não é erro.
func dockerSilencioso(args ...string) {
	_, _ = rodarDocker(args...)
}

func rodarDocker(args ...string) (string, error) {
	ctx, cancelar := context.WithTimeout(context.Background(), prazoDeComando)
	defer cancelar()
	saida, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	return string(saida), err
}

// raizDoRepositorio é o diretório que o `docker build` usa como contexto e de
// onde saem os `dados/<empresa>`. Os testes rodam com o diretório corrente em
// testes/, então a raiz é o pai.
func raizDoRepositorio(t *testing.T) string {
	t.Helper()
	raiz, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("raiz do repositório: %v", err)
	}
	if _, err := os.Stat(filepath.Join(raiz, "Dockerfile.servidor")); err != nil {
		t.Fatalf("raiz do repositório %s sem Dockerfile.servidor: %v", raiz, err)
	}
	return raiz
}
