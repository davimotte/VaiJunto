// Package federacao é a pilha REST entre os servidores do consórcio: os
// handlers HTTP que atendem os pares e o cliente HTTP que os chama (camada
// 1–4' do PROJETO_P2.md, seção 5.2; API_CONSORCIO.md, seções 1 e 2).
//
// É a borda do servidor para o lado dos outros servidores, assim como
// internal/servidor é a borda para o lado dos clientes TCP. Duas
// responsabilidades só existem aqui:
//
//   - a tradução de erro de domínio para status HTTP e motivo (seção 5.5);
//   - a atualização do relógio de Lamport (seção 5.7), num ponto único: o
//     middleware no recebimento, o cliente no envio e na resposta. Nenhum
//     handler mexe no relógio.
//
// O pacote importa internal/dominio e internal/transacao, e nunca o contrário
// (seção 5.3).
package federacao

import (
	"net"
	"net/http"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/transacao"
)

// VersaoAPI é a versão do API_CONSORCIO.md que este servidor fala, devolvida
// em GET /saude (seção 4.1). Divergência entre pares aparece no registro de
// operações.
const VersaoAPI = 1

// limiteCorpo é o teto de corpo de requisição da seção 1 da API: 64 KB. Corpo
// maior responde 413.
const limiteCorpo = 64 << 10

// prazoLeituraCabecalho limita o tempo entre a conexão e o fim dos cabeçalhos.
//
// Não é um prazo da D41 (aqueles são de quem chama, e configuráveis por
// flag): é a proteção do servidor contra uma conexão que abre e não fala,
// pelo mesmo motivo do prazoEscrita da borda TCP (D17). Sem ele, cada conexão
// pendurada consumiria uma goroutine e um descritor para sempre.
const prazoLeituraCabecalho = 10 * time.Second

// Config reúne o que as duas pontas da pilha REST precisam saber. O boot monta
// uma só e passa para NovoHandler e para NovoCliente, de modo que servidor e
// cliente de uma empresa compartilhem obrigatoriamente o mesmo relógio: dois
// relógios no mesmo processo quebrariam a ordem total da seção 8.1.
type Config struct {
	// Empresa é o identificador desta empresa (D27), que vai no cabeçalho
	// X-VaiJunto-Empresa de tudo o que sai daqui.
	Empresa string

	// Pares é empresa → URL base, da flag --pares (D20). Também é a lista de
	// quem pode nos chamar: empresa fora dela recebe 403.
	Pares map[string]string

	// Relogio é o L desta empresa (D24).
	Relogio *transacao.Relogio

	// Registro é o registro de operações (D19); nil desliga.
	Registro *Registro

	// Estado é o estado desta empresa, de onde saem as caronas de
	// GET /federacao/caronas e do passo 3 da busca (seção 6). Obrigatório da
	// fase 3B em diante.
	//
	// A pilha REST recebe o *dominio.Estado, e não uma interface: a direção de
	// importação da seção 5.3 já é federacao → dominio, e um tipo intermediário
	// aqui só acrescentaria uma peça para explicar. Quem continua garantindo a
	// disciplina do lock é o próprio Estado, cujos métodos são os únicos que o
	// adquirem (D05).
	Estado *dominio.Estado

	// Participante é o lado dono dos assentos do 2PC (D21), a quem os handlers
	// de preparar, confirmar e abortar entregam cada requisição. Obrigatório da
	// fase 4 em diante.
	//
	// É o **mesmo** Participante que o coordenador local chama direto, sem rede,
	// quando a empresa do passageiro também é dona de algum trecho (seção 7.1,
	// passo 5): dois caminhos de entrada, um código só.
	Participante *transacao.Participante

	// PrazoChamada é o prazo de cada chamada a um par (D41, 2 s por padrão).
	// Só o cliente o usa.
	PrazoChamada time.Duration

	// PrazoBusca é o prazo **único** da rodada de pares de uma busca federada
	// (D41, 2 s por padrão). É separado de PrazoChamada de propósito: aquele
	// limita uma chamada, este limita o leque inteiro, de modo que três pares
	// lentos não somem três prazos na espera do passageiro.
	PrazoBusca time.Duration

	// Falha é o injetor de pontos de falha desta instância (D31, seção 9.5).
	// Só um ponto vive nesta camada — part-apos-responder, que precisa da
	// resposta já entregue —; os outros estão em internal/transacao. Nil, que é
	// o padrão, desliga todos.
	Falha *transacao.Injetor

	// Entregador é o outbox da D39 (seção 4.7): quem recebe
	// POST /avisos/carona-cancelada decide o cancelamento da reserva e
	// precisa entregar os avisos recém-criados aos demais participantes, por
	// isso o handler precisa dele. Nunca enfileira nada sozinho além do que o
	// domínio acabou de registrar.
	Entregador *transacao.Entregador

	// Auditoria liga GET /auditoria, o estado completo desta empresa para o
	// verificador de invariantes (seção 11.2; flag --auditoria).
	//
	// Desligada por padrão, e é importante que seja: a auditoria expõe de uma
	// vez tudo o que a empresa sabe, inclusive as participações, que nenhum par
	// tem o direito de ver (D32). Ela existe para o teste, não para o
	// consórcio.
	//
	// Não é componente central: não participa de nenhuma decisão e é somente
	// leitura. É instrumento de observação, como o -race.
	Auditoria bool
}

// NovoHandler monta o roteador REST com o middleware único (seção 1 da API).
//
// Os padrões incluem o método ("GET /saude"), que é o que faz o ServeMux
// recusar por si o método errado num recurso existente. O padrão "/" de
// captura existe para que essa recusa saia no formato de erro da seção 2:
// sem ele, o ServeMux responderia 405 em texto puro, que não é um dos status
// da API, e um par receberia um corpo que não sabe ler.
func NovoHandler(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /saude", func(w http.ResponseWriter, r *http.Request) {
		responderJSON(w, http.StatusOK, saudeAtual(cfg.Empresa))
	})
	// Não entra em caminhosSemCabecalho: exigir X-VaiJunto-Empresa é o que faz
	// o middleware recusar com 403 quem não está na lista de pares (D20). As
	// caronas são dado da empresa, e /saude é diagnóstico — por isso só o
	// segundo é aberto.
	mux.HandleFunc("GET /federacao/caronas", func(w http.ResponseWriter, r *http.Request) {
		tratarCaronas(w, r, cfg.Estado, cfg.Empresa)
	})
	// Os recursos de transação levam o txID no caminho, sem codificação: o
	// caractere ":" é válido num segmento de URL (seção 1.4 da API). O curinga
	// {tx} casa exatamente um segmento, que é o identificador inteiro.
	mux.HandleFunc("POST /transacoes/{tx}/preparar", func(w http.ResponseWriter, r *http.Request) {
		tratarPreparar(w, r, cfg.Participante, cfg.Falha)
	})
	mux.HandleFunc("POST /transacoes/{tx}/confirmar", func(w http.ResponseWriter, r *http.Request) {
		tratarConfirmar(w, r, cfg.Participante)
	})
	mux.HandleFunc("POST /transacoes/{tx}/abortar", func(w http.ResponseWriter, r *http.Request) {
		tratarAbortar(w, r, cfg.Participante)
	})
	// D39: cancelamento não é 2PC, mas usa o mesmo recurso de transação do
	// coordenador para o participante — é por isso que leva o mesmo papel de
	// confirmar/abortar (seção 4.7 da API).
	mux.HandleFunc("POST /transacoes/{tx}/cancelar", func(w http.ResponseWriter, r *http.Request) {
		tratarCancelar(w, r, cfg.Participante)
	})
	// O único recurso de transação em que a direção é invertida: quem chama é
	// um participante avisando o coordenador, não o contrário (seção 4.8 da
	// API; seção 10.2 do PROJETO_P2.md). Por isso fica fora do namespace
	// /transacoes/{tx} e não confere papel de coordenador.
	mux.HandleFunc("POST /avisos/carona-cancelada", func(w http.ResponseWriter, r *http.Request) {
		tratarAvisoCaronaCancelada(w, r, cfg.Estado, cfg.Entregador)
	})
	// Único recurso de transação aceito de qualquer empresa do consórcio
	// (seção 1.2 da API), e único que pode gravar sem ser o coordenador quem
	// pediu: a consulta de uma transação desconhecida grava o veto da D38.
	mux.HandleFunc("POST /transacoes/{tx}/consultar", func(w http.ResponseWriter, r *http.Request) {
		tratarConsultar(w, r, cfg.Participante)
	})
	// Só existe com a flag (seção 4.9 da API). Sem ela, o caminho cai no "/" e
	// responde 404 RECURSO_INEXISTENTE, que é exatamente o que o documento
	// manda — e é o que o teste do middleware verifica.
	if cfg.Auditoria {
		mux.HandleFunc("GET /auditoria", func(w http.ResponseWriter, r *http.Request) {
			responderJSON(w, http.StatusOK, auditoriaDeFio(cfg.Estado.Auditoria()))
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		responderErro(w, erroHTTP{http.StatusNotFound, codigoRecursoInexistente, "Recurso inexistente."})
	})
	return comMiddleware(cfg, mux)
}

// Servidor é a porta HTTP dos pares (--endereco-pares).
//
// Tem a mesma forma do servidor TCP (internal/servidor): Escutar abre a porta,
// Servir entra no laço, Fechar o encerra. Abrir a porta fora do laço é o que
// permite ao boot falhar com mensagem clara quando a porta está ocupada, em
// vez de descobrir isso numa goroutine já solta.
type Servidor struct {
	ln   net.Listener
	http *http.Server
}

// Escutar abre a porta HTTP dos pares (ex.: "0.0.0.0:8080").
func Escutar(endereco string, cfg Config) (*Servidor, error) {
	ln, err := net.Listen("tcp", endereco)
	if err != nil {
		return nil, err
	}
	return &Servidor{
		ln: ln,
		http: &http.Server{
			Handler:           NovoHandler(cfg),
			ReadHeaderTimeout: prazoLeituraCabecalho,
		},
	}, nil
}

// Servir atende os pares até o servidor ser fechado.
func (s *Servidor) Servir() error {
	return s.http.Serve(s.ln)
}

// Endereco devolve o endereço em que o servidor HTTP está escutando. Com porta
// 0 (testes), é só aqui que se descobre a porta efetiva.
func (s *Servidor) Endereco() string {
	return s.ln.Addr().String()
}

// Fechar encerra o servidor HTTP, fazendo Servir retornar.
func (s *Servidor) Fechar() error {
	return s.http.Close()
}
