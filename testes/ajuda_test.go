package testes

import (
	"context"
	"os"
	"path/filepath"
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
}

// empresa é uma empresa do consórcio no ar, com as peças que o boot liga
// (cmd/servidor/main.go).
type empresa struct {
	nome         string
	endereco     string // porta TCP dos clientes; vazio quando tcp é falso
	estado       *dominio.Estado
	participante *transacao.Participante
	coordenador  *transacao.Coordenador
	rede         transacao.Rede // o cliente HTTP desta empresa
	rest         *federacao.Servidor
	pares        map[string]string
	registro     *saidaSegura
	wal          *persistencia.Arquivo // nil quando o log é só em memória
	relogio      *transacao.Relogio
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

	prazoChamada := op.prazoChamada
	if prazoChamada == 0 {
		prazoChamada = prazoNosTestes
	}

	consorcio := make(map[string]*empresa, len(op.caronas))
	configs := make(map[string]federacao.Config, len(op.caronas))

	for nome, caronas := range op.caronas {
		// Os arquivos de usuários das empresas são o mesmo: os nomes de usuário
		// são únicos **dentro** da empresa (seção 12.1), então maria pode
		// existir nas três sem conflito, e é a empresa a que ela se conecta que
		// a autentica.
		estado, err := dominio.CarregarEstado(nome, usuarios, caronas)
		if err != nil {
			t.Fatalf("empresa %s: carga inicial: %v", nome, err)
		}

		e := &empresa{
			nome:         nome,
			estado:       estado,
			participante: transacao.NovoParticipante(estado),
			pares:        make(map[string]string),
			registro:     &saidaSegura{},
			relogio:      transacao.NovoRelogio(),
		}
		estado.UsarRelogio(e.relogio.Agora)
		if op.dirLogs != "" {
			e.wal = instalarWAL(t, estado, filepath.Join(op.dirLogs, nome+".wal"))
		}
		for outra, url := range op.paresExtra {
			e.pares[outra] = url
		}

		cfg := federacao.Config{
			Empresa:      nome,
			Pares:        e.pares,
			Relogio:      e.relogio,
			Registro:     federacao.NovoRegistro(e.registro, nome),
			Estado:       estado,
			Participante: e.participante,
			PrazoChamada: prazoChamada,
			PrazoBusca:   prazoBuscaNosTestes,
			Auditoria:    op.auditoria,
		}

		rest, err := federacao.Escutar("127.0.0.1:0", cfg)
		if err != nil {
			t.Fatalf("empresa %s: escutar porta dos pares: %v", nome, err)
		}
		e.rest = rest
		consorcio[nome] = e
		configs[nome] = cfg
	}

	// Segunda passada: agora que todos os endereços existem, cruza os pares e
	// só então começa a servir.
	for nome, e := range consorcio {
		for outro, o := range consorcio {
			if outro != nome {
				e.pares[outro] = o.urlBase()
			}
		}

		rest := e.rest
		go func() { _ = rest.Servir() }()
		t.Cleanup(func() { _ = rest.Fechar() })

		cliente := federacao.NovoCliente(configs[nome])
		e.rede = cliente
		e.coordenador = transacao.NovoCoordenador(transacao.ConfigCoordenador{
			Empresa:      nome,
			Estado:       e.estado,
			Relogio:      e.relogio,
			Rede:         cliente,
			Participante: federacao.NovoVotanteLocal(e.participante),
			Consorcio:    empresasDe(consorcio, op.paresExtra),
			Registro:     configs[nome].Registro,
			PrazoVotacao: op.prazoVotacao,
		})
		// Antes de fechar os servidores: as tarefas de segundo plano repetem a
		// cada 2 s sem limite (D41), e sem isto continuariam batendo em portas
		// já fechadas depois do fim do teste.
		coordenador := e.coordenador
		t.Cleanup(coordenador.Parar)

		if op.tcp {
			var busca servidor.BuscaFederada = buscaQueNaoAcha{}
			if op.busca {
				busca = federacao.NovaBusca(configs[nome], cliente)
			}
			s, err := servidor.Escutar("127.0.0.1:0", e.estado, busca, e.coordenador)
			if err != nil {
				t.Fatalf("empresa %s: escutar porta dos clientes: %v", nome, err)
			}
			e.endereco = s.Endereco()
			go func() { _ = s.Aceitar() }()
			t.Cleanup(func() { _ = s.Fechar() })
		}
	}
	return consorcio
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
