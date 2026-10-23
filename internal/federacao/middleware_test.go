package federacao

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/transacao"
)

// configDeTeste monta a Config de uma empresa "vaijunto" com um par conhecido.
// O relógio começa em zero; quem precisa de outro valor chama Restaurar.
func configDeTeste() Config {
	// Estado e participante saem do mesmo Estado, como no boot (cmd/servidor):
	// um participante ligado a outro estado responderia sobre assentos que não
	// são os que a busca enxerga.
	estado := dominio.NovoEstado("vaijunto")
	return Config{
		Empresa:      "vaijunto",
		Pares:        map[string]string{"rotacapixaba": "http://rotacapixaba.invalido:8080"},
		Relogio:      transacao.NovoRelogio(),
		Estado:       estado,
		Participante: transacao.NovoParticipante(estado),
		PrazoChamada: 50 * time.Millisecond,
		PrazoBusca:   50 * time.Millisecond,
	}
}

// pedir roda uma requisição pelo handler completo (middleware + rotas).
func pedir(cfg Config, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	NovoHandler(cfg).ServeHTTP(rec, req)
	return rec
}

// comCabecalhos devolve a requisição com os dois cabeçalhos da seção 1.1.
func comCabecalhos(req *http.Request, empresa string, lamport uint64) *http.Request {
	req.Header.Set(cabecalhoEmpresa, empresa)
	req.Header.Set(cabecalhoLamport, strconv.FormatUint(lamport, 10))
	return req
}

func codigoDoErro(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var corpo corpoDeErro
	if err := json.Unmarshal(rec.Body.Bytes(), &corpo); err != nil {
		t.Fatalf("corpo de erro ilegível (%q): %v", rec.Body.String(), err)
	}
	if corpo.Mensagem == "" {
		t.Errorf("erro %q sem mensagem legível", corpo.Codigo)
	}
	return corpo.Codigo
}

// Recurso protegido sem os cabeçalhos: 400 CABECALHO_INVALIDO (seção 5.1).
//
// O caminho é desconhecido de propósito: o envelope é conferido ANTES do
// roteamento, como na pilha TCP, então a resposta é 400 e não 404.
func TestMiddleware_SemCabecalhosEm400(t *testing.T) {
	rec := pedir(configDeTeste(), httptest.NewRequest(http.MethodPost, "/transacoes/vaijunto:res-1/preparar", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := codigoDoErro(t, rec); got != codigoCabecalhoInvalido {
		t.Errorf("codigo = %q, want %q", got, codigoCabecalhoInvalido)
	}
}

// Os dois cabeçalhos vão sempre juntos: meio caminho é malformado, não
// "chamada sem cabeçalhos".
func TestMiddleware_CabecalhoPelaMetadeEm400(t *testing.T) {
	casos := map[string]func(*http.Request){
		"só empresa": func(r *http.Request) { r.Header.Set(cabecalhoEmpresa, "rotacapixaba") },
		"só lamport": func(r *http.Request) { r.Header.Set(cabecalhoLamport, "7") },
	}

	for nome, preparar := range casos {
		t.Run(nome, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/saude", nil)
			preparar(req)

			rec := pedir(configDeTeste(), req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if got := codigoDoErro(t, rec); got != codigoCabecalhoInvalido {
				t.Errorf("codigo = %q, want %q", got, codigoCabecalhoInvalido)
			}
		})
	}
}

// X-VaiJunto-Lamport é "inteiro decimal ≥ 0" (seção 1.1): sinal, espaço,
// ponto e texto são malformados.
func TestMiddleware_LamportMalformadoEm400(t *testing.T) {
	for _, valor := range []string{"abc", "-1", "+5", "7.0", " 7", "0x10", "99999999999999999999999"} {
		t.Run(valor, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/saude", nil)
			req.Header.Set(cabecalhoEmpresa, "rotacapixaba")
			req.Header.Set(cabecalhoLamport, valor)

			rec := pedir(configDeTeste(), req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d para lamport %q, want 400", rec.Code, valor)
			}
			if got := codigoDoErro(t, rec); got != codigoCabecalhoInvalido {
				t.Errorf("codigo = %q, want %q", got, codigoCabecalhoInvalido)
			}
		})
	}
}

// Empresa fora de --pares: 403 EMPRESA_DESCONHECIDA (D20).
//
// A própria empresa também é desconhecida: ParsearPares a recusa na lista, e
// um servidor nunca se chama por HTTP.
func TestMiddleware_EmpresaDesconhecidaEm403(t *testing.T) {
	for _, empresa := range []string{"rotamineira", "vaijunto"} {
		t.Run(empresa, func(t *testing.T) {
			rec := pedir(configDeTeste(), comCabecalhos(httptest.NewRequest(http.MethodGet, "/saude", nil), empresa, 7))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d para empresa %q, want 403", rec.Code, empresa)
			}
			if got := codigoDoErro(t, rec); got != codigoEmpresaDesconhecida {
				t.Errorf("codigo = %q, want %q", got, codigoEmpresaDesconhecida)
			}
		})
	}
}

// /saude sem cabeçalhos responde 200 (seção 1.1): é o recurso que o
// simulador, o teste de sistema e uma pessoa com curl precisam alcançar.
func TestMiddleware_SaudeSemCabecalhos(t *testing.T) {
	rec := pedir(configDeTeste(), httptest.NewRequest(http.MethodGet, "/saude", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (corpo: %s)", rec.Code, rec.Body)
	}
}

// /auditoria é isento de cabeçalho (seção 1.1) e **só existe com a flag**
// (seção 4.9): sem ela, 404, e não 400.
//
// A isenção e a ausência são duas regras diferentes, e este teste prova as
// duas de uma vez: a requisição chega sem X-VaiJunto-Empresa e, ainda assim, a
// recusa é por o recurso não existir — não por cabeçalho faltando.
//
// O padrão desligado importa: a auditoria expõe de uma vez tudo o que a empresa
// sabe, inclusive as participações, que nenhum par tem o direito de ver (D32).
// Ela é instrumento de teste, não parte da API do consórcio.
func TestMiddleware_AuditoriaIsentaMasDesligadaPorPadrao(t *testing.T) {
	rec := pedir(configDeTeste(), httptest.NewRequest(http.MethodGet, "/auditoria", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := codigoDoErro(t, rec); got != codigoRecursoInexistente {
		t.Errorf("codigo = %q, want %q", got, codigoRecursoInexistente)
	}
}

// Com a flag, o recurso responde 200 e continua isento de cabeçalho: o
// verificador não é um par do consórcio e não tem empresa para declarar.
func TestMiddleware_AuditoriaComAFlagResponde(t *testing.T) {
	cfg := configDeTeste()
	cfg.Auditoria = true

	rec := pedir(cfg, httptest.NewRequest(http.MethodGet, "/auditoria", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (corpo: %s)", rec.Code, rec.Body)
	}
}

// TODA resposta leva os dois cabeçalhos (seção 1.1), inclusive as de erro e as
// dos recursos isentos. Sem isso, o par que recebe um erro não atualiza o
// relógio e a ordem total da seção 8.1 fica com buracos.
func TestMiddleware_TodaRespostaLevaOsCabecalhos(t *testing.T) {
	casos := map[string]*http.Request{
		"sucesso":              httptest.NewRequest(http.MethodGet, "/saude", nil),
		"erro de cabeçalho":    httptest.NewRequest(http.MethodPost, "/transacoes/vaijunto:res-1/preparar", nil),
		"empresa desconhecida": comCabecalhos(httptest.NewRequest(http.MethodGet, "/saude", nil), "rotamineira", 3),
		"recurso inexistente":  comCabecalhos(httptest.NewRequest(http.MethodGet, "/inventado", nil), "rotacapixaba", 3),
	}

	for nome, req := range casos {
		t.Run(nome, func(t *testing.T) {
			rec := pedir(configDeTeste(), req)

			if got := rec.Header().Get(cabecalhoEmpresa); got != "vaijunto" {
				t.Errorf("%s = %q, want %q", cabecalhoEmpresa, got, "vaijunto")
			}
			bruto := rec.Header().Get(cabecalhoLamport)
			if _, err := strconv.ParseUint(bruto, 10, 64); err != nil {
				t.Errorf("%s = %q, que não é inteiro ≥ 0", cabecalhoLamport, bruto)
			}
		})
	}
}

// O relógio é atualizado no recebimento, em um ponto só: max(L, recebido) + 1
// (seção 8.1), e o valor da resposta é esse mesmo L.
func TestMiddleware_RelogioAvancaNoRecebimento(t *testing.T) {
	cfg := configDeTeste()
	cfg.Relogio.Restaurar(3)

	rec := pedir(cfg, comCabecalhos(httptest.NewRequest(http.MethodGet, "/saude", nil), "rotacapixaba", 10))

	if got := cfg.Relogio.Agora(); got != 11 {
		t.Errorf("L depois do recebimento = %d, want 11", got)
	}
	if got := rec.Header().Get(cabecalhoLamport); got != "11" {
		t.Errorf("%s da resposta = %q, want \"11\"", cabecalhoLamport, got)
	}
}

// Requisição recusada não move o relógio.
//
// É a parte do critério que protege a precedência (seção 8.2): se um valor de
// fora da lista de pares entrasse no relógio, qualquer um poderia empurrar o L
// desta empresa para o alto e fazer todas as suas transações nascerem mais
// novas — e perderem toda disputa por assento.
func TestMiddleware_RequisicaoRecusadaNaoMoveORelogio(t *testing.T) {
	casos := map[string]*http.Request{
		"empresa desconhecida": comCabecalhos(httptest.NewRequest(http.MethodGet, "/saude", nil), "rotamineira", 999),
		"lamport malformado":   httptest.NewRequest(http.MethodGet, "/saude", nil),
	}
	casos["lamport malformado"].Header.Set(cabecalhoEmpresa, "rotacapixaba")
	casos["lamport malformado"].Header.Set(cabecalhoLamport, "999a")

	for nome, req := range casos {
		t.Run(nome, func(t *testing.T) {
			cfg := configDeTeste()
			cfg.Relogio.Restaurar(5)

			rec := pedir(cfg, req)

			if got := cfg.Relogio.Agora(); got != 5 {
				t.Errorf("L = %d depois de requisição recusada, want 5", got)
			}
			if got := rec.Header().Get(cabecalhoLamport); got != "5" {
				t.Errorf("%s da resposta = %q, want \"5\"", cabecalhoLamport, got)
			}
		})
	}
}

// Corpo acima de 64 KB: 413 CORPO_GRANDE (seção 1).
//
// Os dois casos existem porque a decisão NÃO pode vir do Content-Length: o
// segundo caso não tem esse cabeçalho, e uma implementação que o conferisse
// deixaria passar exatamente o corpo que o limite deve barrar.
func TestMiddleware_CorpoGrandeEm413(t *testing.T) {
	grande := bytes.Repeat([]byte("a"), limiteCorpo+1)

	casos := map[string]io.Reader{
		"com Content-Length": bytes.NewReader(grande),
		"em chunked":         io.NopCloser(bytes.NewReader(grande)),
	}

	for nome, corpo := range casos {
		t.Run(nome, func(t *testing.T) {
			req := comCabecalhos(httptest.NewRequest(http.MethodPost, "/transacoes/vaijunto:res-1/preparar", corpo), "rotacapixaba", 7)

			rec := pedir(configDeTeste(), req)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413", rec.Code)
			}
			if got := codigoDoErro(t, rec); got != codigoCorpoGrande {
				t.Errorf("codigo = %q, want %q", got, codigoCorpoGrande)
			}
		})
	}
}

// Exatamente 64 KB passa: o limite é "acima de 64 KB".
func TestMiddleware_CorpoNoLimitePassa(t *testing.T) {
	corpo := bytes.NewReader(bytes.Repeat([]byte("a"), limiteCorpo))
	req := comCabecalhos(httptest.NewRequest(http.MethodPost, "/transacoes/rotacapixaba:res-1/preparar", corpo), "rotacapixaba", 7)

	rec := pedir(configDeTeste(), req)
	// O que se afirma aqui é que o corpo passou do limite de tamanho e chegou
	// ao handler: 64 KB de "a" não são um JSON de preparar, e a recusa vem do
	// handler (CORPO_INVALIDO), não do middleware (CORPO_GRANDE).
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (corpo de 64 KB exatos deve passar)", rec.Code)
	}
	if got := codigoDoErro(t, rec); got != codigoCorpoInvalido {
		t.Errorf("codigo = %q, want %q", got, codigoCorpoInvalido)
	}
}

// Método errado num recurso existente vira RECURSO_INEXISTENTE (seção 5.1).
//
// Este teste fixa o motivo do padrão "/" de captura em NovoHandler: sem ele, o
// ServeMux responderia 405 em texto puro, que não é um status da API e tem
// corpo que nenhum par sabe ler.
func TestMiddleware_MetodoErradoEm404(t *testing.T) {
	casos := map[string]*http.Request{
		"método errado":       comCabecalhos(httptest.NewRequest(http.MethodPost, "/saude", nil), "rotacapixaba", 7),
		"caminho inexistente": comCabecalhos(httptest.NewRequest(http.MethodGet, "/inventado", nil), "rotacapixaba", 7),
	}

	for nome, req := range casos {
		t.Run(nome, func(t *testing.T) {
			rec := pedir(configDeTeste(), req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (corpo: %s)", rec.Code, rec.Body)
			}
			if got := codigoDoErro(t, rec); got != codigoRecursoInexistente {
				t.Errorf("codigo = %q, want %q", got, codigoRecursoInexistente)
			}
			if tipo := rec.Header().Get("Content-Type"); !strings.HasPrefix(tipo, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", tipo)
			}
		})
	}
}

// O registro de operações (D19) escreve uma linha por requisição REST
// recebida, no formato da seção 5.2: é por ela que o protocolo fica visível na
// demonstração.
func TestMiddleware_RegistraRequisicaoRecebida(t *testing.T) {
	var saida bytes.Buffer
	cfg := configDeTeste()
	cfg.Registro = NovoRegistro(&saida, cfg.Empresa)

	pedir(cfg, comCabecalhos(httptest.NewRequest(http.MethodGet, "/saude", nil), "rotacapixaba", 40))

	linha := saida.String()
	for _, parte := range []string{"[vaijunto]", "← rotacapixaba", "GET /saude", "L=41", "→ 200", "ms"} {
		if !strings.Contains(linha, parte) {
			t.Errorf("linha do registro sem %q: %s", parte, linha)
		}
	}
}

// Nome de empresa é texto de fora: um "\n" no cabeçalho não pode forjar linhas
// no registro de outra empresa.
func TestMiddleware_RegistroNaoAceitaNomeForjado(t *testing.T) {
	var saida bytes.Buffer
	cfg := configDeTeste()
	cfg.Registro = NovoRegistro(&saida, cfg.Empresa)

	req := httptest.NewRequest(http.MethodGet, "/saude", nil)
	req.Header.Set(cabecalhoEmpresa, "rotacapixaba\n2026/01/01 00:00:00.000 [vaijunto] ← rotacapixaba FORJADA")
	req.Header.Set(cabecalhoLamport, "7")

	pedir(cfg, req)

	if linhas := strings.Count(strings.TrimRight(saida.String(), "\n"), "\n"); linhas != 0 {
		t.Errorf("registro ganhou %d linha(s) a mais: %s", linhas, saida.String())
	}
}
