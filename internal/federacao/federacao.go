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

	// PrazoChamada é o prazo de cada chamada a um par (D41, 2 s por padrão).
	// Só o cliente o usa.
	PrazoChamada time.Duration
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
