package testes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/federacao"
	"vaijunto/internal/persistencia"
	"vaijunto/internal/servidor"
	"vaijunto/internal/transacao"
)

// Montagem de consórcios para os testes multiempresa.
//
// As três variantes que existiam antes da fase 5 — só REST, REST com porta TCP,
// e REST com participante — repetiam o mesmo laço duplo e divergiam só nas
// peças que ligavam. Com o coordenador, TR1, TR2 e TR5 passaram a precisar de
// **todas** as peças ao mesmo tempo (porta TCP, participante, coordenador e
// WAL), e uma quarta cópia do laço seria a hora de errar uma. Daí este arquivo.
//
// O laço é duplo de propósito, e é o detalhe que mais custa quando se erra: os
// endereços HTTP só existem depois do Escutar com porta 0, então as listas de
// pares só podem ser cruzadas na segunda passada. E têm de ser cruzadas **antes**
// de qualquer Servir, porque o mapa Pares é compartilhado por referência com a
// Config: escrevê-lo depois de uma goroutine de servidor começar a lê-lo é
// corrida de verdade, que o -race acusa.

// prazoNosTestes é o prazo de cada chamada a um par (D41) encurtado para os
// testes, mas largo o bastante para 50 chamadas simultâneas num processo
// carregado pelo -race.
const prazoNosTestes = 2 * time.Second

// prazoBuscaNosTestes é o prazo do leque da busca federada, bem curto porque o
// TB2 espera por um par que não vai responder.
const prazoBuscaNosTestes = 300 * time.Millisecond

// opcoesConsorcio diz quais peças cada empresa do consórcio recebe.
//
// O padrão é o mínimo: estado, porta HTTP, participante e coordenador, com o
// log em memória. Quem precisa de mais liga o que precisa — é o que mantém a
// falha de um teste restrita ao que ele de fato exercita.
type opcoesConsorcio struct {
	// caronas é o conteúdo de caronas.json de cada empresa. As chaves definem
	// quais empresas sobem.
	caronas map[string][]byte
	// tcp abre também a porta dos clientes, necessária para quem fala o
	// PROTOCOL.md (RESERVAR, BUSCAR_ITINERARIOS).
	tcp bool
	// busca liga a busca federada de verdade na porta TCP. Sem ela, a empresa
	// sobe com uma busca que não acha nada — basta para os testes de reserva.
	busca bool
	// dirLogs vazio deixa o log em memória; com diretório, cada empresa ganha
	// o seu arquivo e passa pelo caminho completo do boot (D37).
	dirLogs string
	// auditoria liga GET /auditoria (seção 11.2), de que o verificador precisa.
	auditoria bool
	// paresExtra entra na lista de pares de todas as empresas sem subir
	// servidor nenhum: é como o teste do passo 5 da busca põe um par falso no
	// consórcio.
	paresExtra map[string]string
	// prazoChamada e prazoVotacao sobrescrevem os padrões quando o teste precisa
	// de um prazo curto para provar que ele decide algo.
	prazoChamada time.Duration
	prazoVotacao time.Duration
	// prazoAguarde é o intervalo entre repetições do PREPARAR após um AGUARDE
	// (D41, seção 8.2). Nos 200 ms de produção, um teste de disputa gasta
	// quase todo o seu tempo dormindo entre repetições; os testes do wait-die
	// o encurtam para milissegundos.
	prazoAguarde time.Duration
	// prazoDuvida e prazoConsulta são os da terminação cooperativa (D41,
	// seção 9.4). Nos testes vão para a casa dos milissegundos: os 10 s e 3 s
	// de produção existem para que, num sistema saudável, consulta nenhuma
	// chegue a acontecer — e um teste que os respeitasse levaria minutos.
	prazoDuvida   time.Duration
	prazoConsulta time.Duration
	// prazoFase2 é o intervalo entre repetições de CONFIRMAR e de ABORTAR
	// (D41). Importa aos testes que esperam uma repetição acontecer, como o TF6
	// e o TF7: com os 2 s de produção, cada um levaria segundos sob -count=20.
	prazoFase2 time.Duration
	// redeEmMemoria troca o cliente HTTP pela rede em memória da seção 5.4 em
	// todas as empresas do consórcio. As portas continuam no ar — é só o
	// **transporte** do 2PC que muda.
	//
	// Serve aos cenários que dependem de ordem de chegada, como a disputa
	// cruzada do TR3 (seção 8.3): com ela o teste decide em que ordem cada
	// PREPARAR chega a cada dono, pelo gancho de Interceptar, em vez de torcer
	// para o escalonador produzir a ordem certa.
	//
	// Duas consequências a lembrar ao escrever um teste assim: a rede em
	// memória não passa pelo middleware REST, então **o relógio de Lamport não
	// se propaga** entre as empresas — quem quiser carimbos ordenados os fixa
	// com Relogio.Restaurar — e o registro de operações não ganha as linhas de
	// requisição, porque não houve requisição.
	redeEmMemoria *federacao.RedeEmMemoria
	// falhaEm arma um ponto de falha por empresa (D31, seção 9.5): empresa →
	// valor de FALHAR_EM. Empresa ausente, ou ponto vazio, sobe sem ponto
	// nenhum — que é o caso de todos os testes que não são da fase 6B.
	//
	// É por empresa, e não um valor só, porque as três sobem no mesmo processo:
	// um ponto global armaria a queda nas três ao mesmo tempo, e o cenário
	// deixaria de ser "a empresa X caiu no ponto Y".
	falhaEm map[string]string
}

// empresa é uma empresa do consórcio no ar, com as peças que o boot liga
// (cmd/servidor/main.go).
type empresa struct {
	nome         string
	endereco     string // porta TCP dos clientes; vazio quando tcp é falso
	estado       *dominio.Estado
	participante *transacao.Participante
	coordenador  *transacao.Coordenador
	terminador   *transacao.Terminador
	rede         transacao.Rede // o cliente HTTP desta empresa
	rest         *federacao.Servidor
	tcp          *servidor.Servidor // nil quando a porta dos clientes não sobe
	pares        map[string]string
	registro     *saidaSegura
	wal          *persistencia.Arquivo // nil quando o log é só em memória
	relogio      *transacao.Relogio

	// O que um religamento precisa repetir: o endereço, que é fixo porque a
	// lista de pares é estática (D20), e a semente, que é carregada antes do
	// log em todo boot (D37, regra 3).
	enderecoPares string
	usuariosJSON  []byte
	caronasJSON   []byte

	// falha é o injetor de pontos desta instância (D31); nil quando o teste não
	// armou nenhum.
	falha *transacao.Injetor

	// quedas recebe o ponto em que esta instância caiu, para o teste saber que
	// a queda aconteceu antes de religar. Tem capacidade 1 e o envio não
	// bloqueia: quem cai não pode ficar preso esperando alguém escutar.
	quedas chan transacao.Ponto

	// umaQueda garante que fechar portas, tarefas e log aconteça uma vez só.
	// O caso normal de um teste é derrubar, observar o consórcio sem a empresa
	// e religar — e o religamento começa derrubando, para que também sirva a
	// quem ainda não derrubou. Uma queda é uma queda: repeti-la não é erro, é
	// nada.
	//
	// Precisa ser sincronizado, e não um booleano: com um ponto de falha
	// armado, quem derruba é uma goroutine do próprio servidor, concorrente com
	// a do teste.
	umaQueda sync.Once
}

// urlBase é o endereço base da porta dos pares desta empresa.
func (e *empresa) urlBase() string { return "http://" + e.rest.Endereco() }

// subirConsorcioP2 sobe uma empresa por entrada de op.caronas, liga as peças
// pedidas e cruza as listas de pares com os endereços efetivos.
func subirConsorcioP2(t *testing.T, op opcoesConsorcio) map[string]*empresa {
	t.Helper()

	usuarios, err := os.ReadFile(filepath.Join(diretorioSemente, "usuarios.json"))
	if err != nil {
		t.Fatalf("ler usuários do fixture: %v", err)
	}

	consorcio := make(map[string]*empresa, len(op.caronas))
	for nome, caronas := range op.caronas {
		// Porta 0: o endereço efetivo só existe depois do Escutar, e é por isso
		// que as listas de pares só podem ser cruzadas na segunda passada.
		consorcio[nome] = montarEmpresa(t, nome, usuarios, caronas, op, "127.0.0.1:0")
	}

	// Segunda passada: agora que todos os endereços existem, cruza os pares e
	// só então começa a servir.
	for _, e := range consorcio {
		ligarEmpresa(t, e, consorcio, op)
	}
	return consorcio
}

// montarEmpresa é a metade do boot que acontece antes de uma empresa conhecer
// as outras: semente, relógio, log com replay, participante e a porta dos
// pares aberta (cmd/servidor/main.go, até o Escutar).
//
// É função à parte, e não código solto dentro do laço, porque o religamento da
// seção 9.3 precisa repetir exatamente estes passos — e repeti-los **na mesma
// ordem** é metade do que a fase 6A entrega: semente, depois log, e só então o
// arquivo instalado como destino (D37).
//
// endereco é "127.0.0.1:0" na subida normal e o endereço anterior no
// religamento, porque a lista de pares é estática (D20): uma empresa que volta
// noutra porta simplesmente não é mais encontrada pelos pares.
func montarEmpresa(t *testing.T, nome string, usuarios, caronas []byte, op opcoesConsorcio, endereco string) *empresa {
	t.Helper()

	// Os arquivos de usuários das empresas são o mesmo: os nomes de usuário
	// são únicos **dentro** da empresa (seção 12.1), então maria pode existir
	// nas três sem conflito, e é a empresa a que ela se conecta que a
	// autentica.
	estado, err := dominio.CarregarEstado(nome, usuarios, caronas)
	if err != nil {
		t.Fatalf("empresa %s: carga inicial: %v", nome, err)
	}

	e := &empresa{
		nome:         nome,
		estado:       estado,
		pares:        make(map[string]string),
		registro:     &saidaSegura{},
		relogio:      transacao.NovoRelogio(),
		usuariosJSON: usuarios,
		caronasJSON:  caronas,
		quedas:       make(chan transacao.Ponto, 1),
	}
	// O injetor nasce antes do participante porque o participante o recebe
	// (D31): os pontos part-* vivem na borda do estado, e não no domínio
	// (regra 8).
	if ponto := op.falhaEm[nome]; ponto != "" {
		injetor, err := transacao.NovoInjetor(ponto, e.cair)
		if err != nil {
			t.Fatalf("empresa %s: ponto de falha: %v", nome, err)
		}
		e.falha = injetor
	}
	e.participante = transacao.NovoParticipante(estado, e.falha)
	estado.UsarRelogio(e.relogio.Agora)
	if op.dirLogs != "" {
		e.wal = instalarWAL(t, estado, filepath.Join(op.dirLogs, nome+".wal"))
	}

	rest, err := escutarPares(t, nome, endereco, e.configREST(op))
	if err != nil {
		t.Fatalf("empresa %s: escutar porta dos pares: %v", nome, err)
	}
	e.rest = rest
	e.enderecoPares = rest.Endereco()
	return e
}

// configREST monta a Config desta empresa. O mapa de pares vai por referência,
// e é preenchido por ligarEmpresa antes de qualquer Servir.
func (e *empresa) configREST(op opcoesConsorcio) federacao.Config {
	prazoChamada := op.prazoChamada
	if prazoChamada == 0 {
		prazoChamada = prazoNosTestes
	}
	return federacao.Config{
		Empresa:      e.nome,
		Pares:        e.pares,
		Relogio:      e.relogio,
		Registro:     federacao.NovoRegistro(e.registro, e.nome),
		Estado:       e.estado,
		Participante: e.participante,
		PrazoChamada: prazoChamada,
		PrazoBusca:   prazoBuscaNosTestes,
		Falha:        e.falha,
		Auditoria:    op.auditoria,
	}
}

// escutarPares abre a porta dos pares, insistindo por um instante quando o
// endereço é fixo.
//
// A insistência é só para o religamento: o Fechar anterior já derrubou o
// listener e as conexões, mas o sistema operacional pode levar alguns
// milissegundos para liberar o bind, e o teste falharia por um detalhe que
// nada tem a ver com o que ele afirma.
func escutarPares(t *testing.T, nome, endereco string, cfg federacao.Config) (*federacao.Servidor, error) {
	t.Helper()
	limite := time.Now().Add(2 * time.Second)
	for {
		rest, err := federacao.Escutar(endereco, cfg)
		if err == nil || strings.HasSuffix(endereco, ":0") || !time.Now().Before(limite) {
			return rest, err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ligarEmpresa é a metade do boot que depende de a empresa conhecer as outras:
// lista de pares, cliente, coordenador, terminador, as portas servindo e as
// tarefas de recuperação da seção 9.3.
//
// A lista de pares é preenchida **antes** do Servir de propósito: o mapa é
// compartilhado por referência com a Config, e escrevê-lo depois de a goroutine
// do servidor começar a lê-lo é corrida de verdade, que o -race acusa.
func ligarEmpresa(t *testing.T, e *empresa, consorcio map[string]*empresa, op opcoesConsorcio) {
	t.Helper()

	for outro, o := range consorcio {
		if outro != e.nome {
			e.pares[outro] = o.urlBase()
		}
	}
	for outra, url := range op.paresExtra {
		e.pares[outra] = url
	}

	cfg := e.configREST(op)
	rest := e.rest
	go func() { _ = rest.Servir() }()
	t.Cleanup(func() { _ = rest.Fechar() })

	cliente := federacao.NovoCliente(cfg)
	e.rede = cliente
	// A rede em memória substitui o transporte, e só ele: o participante é o
	// mesmo, o coordenador é o mesmo, e a tradução de erro em voto passa pela
	// mesma tabela da borda (seção 5.4).
	if op.redeEmMemoria != nil {
		op.redeEmMemoria.Registrar(e.nome, e.participante)
		e.rede = op.redeEmMemoria.De(e.nome)
	}
	e.coordenador = transacao.NovoCoordenador(transacao.ConfigCoordenador{
		Empresa:      e.nome,
		Estado:       e.estado,
		Relogio:      e.relogio,
		Rede:         e.rede,
		Participante: federacao.NovoVotanteLocal(e.participante),
		Consorcio:    empresasDe(consorcio, op.paresExtra),
		Registro:     cfg.Registro,
		Falha:        e.falha,
		PrazoVotacao: op.prazoVotacao,
		PrazoAguarde: op.prazoAguarde,
		PrazoFase2:   op.prazoFase2,
	})
	e.terminador = transacao.NovoTerminador(transacao.ConfigTerminador{
		Empresa:       e.nome,
		Estado:        e.estado,
		Rede:          e.rede,
		Registro:      cfg.Registro,
		PrazoDuvida:   op.prazoDuvida,
		PrazoConsulta: op.prazoConsulta,
	})
	// Antes de fechar os servidores: as tarefas de segundo plano repetem sem
	// limite (D41), e sem isto continuariam batendo em portas já fechadas
	// depois do fim do teste.
	coordenador, terminador := e.coordenador, e.terminador
	t.Cleanup(coordenador.Parar)
	t.Cleanup(terminador.Parar)

	if op.tcp {
		var busca servidor.BuscaFederada = buscaQueNaoAcha{}
		if op.busca {
			busca = federacao.NovaBusca(cfg, cliente)
		}
		s, err := servidor.Escutar("127.0.0.1:0", e.estado, busca, e.coordenador)
		if err != nil {
			t.Fatalf("empresa %s: escutar porta dos clientes: %v", e.nome, err)
		}
		e.tcp = s
		e.endereco = s.Endereco()
		go func() { _ = s.Aceitar() }()
		t.Cleanup(func() { _ = s.Fechar() })
	}

	// Passo 5 da seção 9.2: as tarefas de recuperação da seção 9.3 vêm depois
	// de as portas estarem no ar, porque as duas falam com os pares. Numa
	// subida limpa as duas listas vêm vazias; é o religamento que lhes dá
	// trabalho.
	e.coordenador.Recuperar()
	e.terminador.Recuperar()
}

// derrubarEmpresa simula a queda de uma empresa: as portas param de responder,
// as tarefas de segundo plano morrem e o arquivo de log é fechado.
//
// Para os pares é indistinguível de um `docker kill`: a conexão passa a ser
// recusada, que é o que a D30 trata por timeout. O que **não** acontece é
// desligamento limpo: o servidor não tem rotina de desligamento (seção 9.1), e
// nada aqui avisa ninguém nem termina transação alguma.
//
// O estado em memória é abandonado de propósito, sem ser zerado: o que
// sobrevive é só o que está no log, e é dele que o religamento parte.
func derrubarEmpresa(t *testing.T, e *empresa) {
	t.Helper()
	if err := fecharEmpresa(e); err != nil {
		t.Fatalf("empresa %s: %v", e.nome, err)
	}
}

// fecharEmpresa é a queda em si, sem *testing.T.
//
// Existe separada de derrubarEmpresa porque, com um ponto de falha armado (D31),
// quem derruba é uma goroutine do próprio servidor — o handler do PREPARAR, a
// conexão do RESERVAR ou a fase 2 —, e chamar t.Fatalf de fora da goroutine do
// teste não encerra o teste: só a goroutine, deixando o resto correr sem saber.
// Então o erro volta como valor, e quem está na goroutine do teste é que o
// transforma em falha.
func fecharEmpresa(e *empresa) error {
	var err error
	e.umaQueda.Do(func() {
		e.coordenador.Parar()
		e.terminador.Parar()
		if e.tcp != nil {
			_ = e.tcp.Fechar()
		}
		if erroPares := e.rest.Fechar(); erroPares != nil {
			err = fmt.Errorf("fechar porta dos pares: %w", erroPares)
			return
		}
		if e.wal != nil {
			if erroLog := e.wal.Fechar(); erroLog != nil {
				err = fmt.Errorf("fechar log: %w", erroLog)
			}
		}
	})
	return err
}

// cair é o que um ponto de falha faz nos testes em processo (D31, seção 9.5):
// fecha esta instância, avisa o teste e abandona a goroutine que estava
// atendendo.
//
// É o análogo local do os.Exit de produção, e o runtime.Goexit é a metade que
// importa: sem ele, a goroutine continuaria o protocolo depois da "queda" —
// responderia o PREPARAR que o ponto part-apos-gravar manda não responder,
// mandaria os CONFIRMAR que o coord-apos-decidir manda não mandar — e o ponto
// não significaria nada.
//
// Goexit, e não panic, porque serve aos três tipos de chamador sem exceção: no
// handler HTTP, os defer do net/http fecham a conexão sem resposta; na conexão
// TCP, o defer conn.Close() de atenderConexao dá EOF ao passageiro; e na
// goroutine da fase 2, que não tem recover nenhum, um panic derrubaria o
// processo de teste inteiro em vez de uma empresa.
//
// O estado em memória é abandonado sem ser zerado, como em derrubarEmpresa: o
// que sobrevive é só o que está no log, e é dele que o religamento parte.
func (e *empresa) cair(ponto transacao.Ponto) {
	_ = fecharEmpresa(e)
	// Sem bloquear: quem cai não espera por quem escuta.
	select {
	case e.quedas <- ponto:
	default:
	}
	runtime.Goexit()
}

// esperarQueda espera a queda desta instância num ponto de falha e devolve o
// ponto.
//
// É espera observada, como esperarSituacao: a queda acontece numa goroutine do
// servidor, e um sleep calibrado seria ou lento ou instável sob -count=20.
func esperarQueda(t *testing.T, e *empresa) transacao.Ponto {
	t.Helper()
	select {
	case ponto := <-e.quedas:
		return ponto
	case <-time.After(prazoDeQuiescencia):
		t.Fatalf("empresa %s: não caiu em %s; o ponto %s não foi alcançado",
			e.nome, prazoDeQuiescencia, e.falha.Ponto())
		return ""
	}
}

// religarEmpresa derruba uma empresa e a reabre a partir do mesmo log, no
// mesmo endereço, pelo caminho completo do boot (seção 9.2).
//
// É a mesma ferramenta que os pontos de FALHAR_EM usam (D31): o que o ponto
// muda é **quando** a queda acontece, não o que acontece depois dela.
// Substitui a entrada do consórcio, porque tudo é novo — estado,
// participante, coordenador, terminador e cliente —, exatamente como num
// processo que reinicia.
//
// Um detalhe que vale conhecer ao ler um teste que usa isto: logo depois do
// religamento, a primeira chamada de um par pode falhar, porque o http.Client
// dele ainda tem no pool uma conexão para a porta antiga e o net/http não
// repete POST por conta própria. O protocolo se cura sozinho — a consulta
// repete a cada PrazoConsulta e o CONFIRMAR a cada PrazoFase2 —, e é
// justamente essa cura que os testes desta fase observam.
func religarEmpresa(t *testing.T, consorcio map[string]*empresa, nome string, op opcoesConsorcio) *empresa {
	t.Helper()
	antiga, existe := consorcio[nome]
	if !existe {
		t.Fatalf("religarEmpresa: %q não está no consórcio", nome)
	}
	derrubarEmpresa(t, antiga)

	// Religa **sem** ponto de falha armado (D31), que é o equivalente a tirar
	// FALHAR_EM do comando antes de subir de novo. Com o ponto rearmado, um
	// coord-durante-confirmar cairia outra vez na retomada da fase 2, e o
	// servidor nunca terminaria de se recuperar.
	semPontos := op
	semPontos.falhaEm = nil

	nova := montarEmpresa(t, nome, antiga.usuariosJSON, antiga.caronasJSON, semPontos, antiga.enderecoPares)
	consorcio[nome] = nova
	ligarEmpresa(t, nova, consorcio, semPontos)
	return nova
}

// empresasDe lista as empresas que o coordenador reconhece: as que subiram mais
// as dos pares falsos.
func empresasDe(consorcio map[string]*empresa, paresExtra map[string]string) []string {
	lista := make([]string, 0, len(consorcio)+len(paresExtra))
	for nome := range consorcio {
		lista = append(lista, nome)
	}
	for nome := range paresExtra {
		lista = append(lista, nome)
	}
	return lista
}

// buscaQueNaoAcha é a busca dos testes de reserva: eles montam o itinerário à
// mão, com os identificadores que conhecem, e não dependem da busca federada.
type buscaQueNaoAcha struct{}

func (buscaQueNaoAcha) Buscar(_ context.Context, _, _ string, _ time.Time) (federacao.Resultado, error) {
	return federacao.Resultado{
		Itinerarios:           []dominio.Itinerario{},
		EmpresasIndisponiveis: []string{},
	}, nil
}

// instalarWAL repete o caminho do boot: abre o arquivo, reaplica o que está
// nele e só então o instala como destino do log, para que a reaplicação nunca
// grave de volta o que está lendo (D37).
//
// Devolve o arquivo para que o teste de reinício possa fechá-lo e reabrir o
// mesmo caminho — por isso não há Cleanup aqui: quem reinicia é quem gerencia
// o ciclo de vida do arquivo, e um Fechar automático competiria com ele.
func instalarWAL(t *testing.T, estado *dominio.Estado, caminho string) *persistencia.Arquivo {
	t.Helper()
	arquivo, registros, err := persistencia.Abrir(caminho)
	if err != nil {
		t.Fatalf("abrir log %s: %v", caminho, err)
	}
	if _, err := estado.Reaplicar(registros); err != nil {
		t.Fatalf("reaplicar log %s: %v", caminho, err)
	}
	estado.UsarLog(arquivo)
	return arquivo
}
