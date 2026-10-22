package federacao

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Os dois cabeçalhos obrigatórios da seção 1.1 da API.
const (
	cabecalhoEmpresa = "X-VaiJunto-Empresa"
	cabecalhoLamport = "X-VaiJunto-Lamport"
)

// caminhosSemCabecalho são os recursos que aceitam chamada sem os cabeçalhos
// (seção 1.1), para que o simulador, o teste de sistema e uma pessoa com curl
// possam usá-los.
//
// /auditoria já está aqui embora só exista a partir da fase 5: a regra da
// isenção é da API, não da fase, e deixá-la escrita onde ela vale evita que a
// fase 5 a esqueça. Nesta fase o caminho responde 404 (seção 5.1).
var caminhosSemCabecalho = map[string]bool{
	"/saude":     true,
	"/auditoria": true,
}

// comMiddleware embrulha o roteador com o único middleware da pilha REST.
//
// Ele é um só, e não um por rota, porque tudo o que faz vale para todos os
// recursos: validar o envelope da requisição, atualizar o relógio, limitar o
// corpo, pôr os cabeçalhos na resposta e registrar a linha do D19. Cada uma
// dessas regras existe em exatamente um lugar do programa, e por isso nenhum
// handler pode esquecê-la (seção 5.7).
//
// A ordem importa:
//
//  1. conferir os cabeçalhos e, só se eles forem legítimos, atualizar o
//     relógio — "antes de processar a requisição", como manda a seção 1.1;
//  2. pôr os dois cabeçalhos na resposta, ANTES de qualquer escrita de corpo;
//     depois do WriteHeader eles seriam ignorados em silêncio;
//  3. limitar o corpo;
//  4. só então chamar o handler.
func comMiddleware(cfg Config, prox http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inicio := time.Now()

		lamport, falha := aceitarCabecalhos(cfg, r)
		definirCabecalhos(w, cfg.Empresa, lamport)

		// Status observado para o registro de operações: o handler escreve
		// através deste embrulho, que anota o que foi respondido.
		obs := &respostaObservada{ResponseWriter: w, status: http.StatusOK}
		switch {
		case falha != nil:
			responderErro(obs, *falha)
		default:
			if excesso := limitarCorpo(r); excesso != nil {
				responderErro(obs, *excesso)
			} else {
				prox.ServeHTTP(obs, r)
			}
		}

		cfg.Registro.recebida(r.Header.Get(cabecalhoEmpresa), r.Method,
			r.URL.EscapedPath(), lamport, obs.detalhe, obs.status, time.Since(inicio))

		// Seção 5.1 da API: a violação de protocolo é registrada. Ela nunca
		// deveria acontecer (seção 7.4), e por isso ganha uma linha própria, que
		// o teste procura e que numa demonstração não se perde no meio das
		// outras. Fica aqui, e não nos handlers, para que nenhum caminho novo
		// possa esquecê-la: quem responde 409 já disse tudo o que era preciso.
		if obs.status == http.StatusConflict {
			cfg.Registro.violacao(r.Header.Get(cabecalhoEmpresa), r.Method, r.URL.EscapedPath(), obs.detalhe)
		}
	})
}

// aceitarCabecalhos confere X-VaiJunto-Empresa e X-VaiJunto-Lamport e devolve
// o L a publicar na resposta, com a falha quando a requisição não é aceitável.
//
// É o ÚNICO ponto do servidor que chama Relogio.AoReceber (seção 5.7). Três
// decisões estão aqui:
//
//   - **O relógio só avança com cabeçalho válido de empresa conhecida.** Um
//     valor vindo de quem não é do consórcio não é informação causal: deixá-lo
//     entrar permitiria a um terceiro empurrar o L desta empresa para o alto e
//     fazer todas as nossas transações perderem a precedência (seção 8.2).
//   - **Nos recursos isentos, cabeçalho presente é processado normalmente**
//     (seção 1.1). Presente pela metade não é "sem cabeçalhos": é cabeçalho
//     malformado, e aceitar esconderia um par com defeito justamente no
//     recurso que existe para diagnóstico.
//   - **A própria empresa não está em --pares** (ParsearPares a recusa) e um
//     servidor nunca se chama por HTTP, então uma requisição que se diz nossa
//     é EMPRESA_DESCONHECIDA. Não substitui autenticação (seção 15).
func aceitarCabecalhos(cfg Config, r *http.Request) (uint64, *erroHTTP) {
	empresa := r.Header.Get(cabecalhoEmpresa)
	bruto := r.Header.Get(cabecalhoLamport)
	isento := caminhosSemCabecalho[r.URL.Path]

	if empresa == "" && bruto == "" {
		if isento {
			return cfg.Relogio.Agora(), nil
		}
		return cfg.Relogio.Agora(), &erroHTTP{http.StatusBadRequest, codigoCabecalhoInvalido,
			"Os cabeçalhos " + cabecalhoEmpresa + " e " + cabecalhoLamport + " são obrigatórios."}
	}

	if empresa == "" || bruto == "" {
		return cfg.Relogio.Agora(), &erroHTTP{http.StatusBadRequest, codigoCabecalhoInvalido,
			"Os cabeçalhos " + cabecalhoEmpresa + " e " + cabecalhoLamport + " vão sempre juntos."}
	}

	// ParseUint recusa sinal, espaço e vazio: o cabeçalho é "inteiro decimal
	// ≥ 0" (seção 1.1). 64 bits é a largura do relógio.
	recebido, err := strconv.ParseUint(bruto, 10, 64)
	if err != nil {
		return cfg.Relogio.Agora(), &erroHTTP{http.StatusBadRequest, codigoCabecalhoInvalido,
			cabecalhoLamport + " precisa ser um inteiro decimal maior ou igual a zero."}
	}

	if _, conhecida := cfg.Pares[empresa]; !conhecida {
		return cfg.Relogio.Agora(), &erroHTTP{http.StatusForbidden, codigoEmpresaDesconhecida,
			"Empresa fora da lista de pares deste servidor."}
	}

	return cfg.Relogio.AoReceber(recebido), nil
}

// definirCabecalhos põe os dois cabeçalhos em TODA resposta, inclusive nas de
// erro e nas dos recursos isentos (seção 1.1). É o único ponto que os escreve.
func definirCabecalhos(w http.ResponseWriter, empresa string, lamport uint64) {
	w.Header().Set(cabecalhoEmpresa, empresa)
	w.Header().Set(cabecalhoLamport, strconv.FormatUint(lamport, 10))
}

// limitarCorpo aplica o teto de 64 KB da seção 1 e devolve 413 quando ele é
// estourado.
//
// Lê o corpo inteiro aqui e o troca por um leitor em memória, em vez de
// embrulhá-lo com http.MaxBytesReader e deixar cada handler descobrir o
// estouro ao decodificar. Dois motivos:
//
//   - a decisão fica num ponto só, e um handler novo não precisa lembrar dela;
//   - Content-Length não serve para decidir: um corpo em chunked não tem esse
//     cabeçalho, e conferi-lo deixaria passar exatamente o caso que o limite
//     deve barrar.
//
// Guardar o corpo em memória não é custo: 64 KB é o teto que a própria API
// impõe, e é por requisição.
func limitarCorpo(r *http.Request) *erroHTTP {
	if r.Body == nil {
		return nil
	}

	corpo, err := io.ReadAll(io.LimitReader(r.Body, limiteCorpo+1))
	if err != nil {
		return &erroHTTP{http.StatusBadRequest, codigoCorpoInvalido, "Falha ao ler o corpo da requisição."}
	}
	if len(corpo) > limiteCorpo {
		return &erroHTTP{http.StatusRequestEntityTooLarge, codigoCorpoGrande, "Corpo acima de 64 KB."}
	}

	r.Body = io.NopCloser(bytes.NewReader(corpo))
	return nil
}

// respostaObservada anota o status respondido e o detalhe que só o handler
// conhece, para a linha do registro de operações (D19). Sem o embrulho, o
// middleware não tem como saber o que o handler respondeu: a ResponseWriter do
// net/http não conta.
//
// O valor inicial do status é 200 porque um handler que escreve o corpo sem
// chamar WriteHeader responde 200, exatamente como o net/http faz.
//
// O detalhe existe para que o registro continue tendo **um único escritor**. A
// seção 5.2 do PROJETO_P2.md quer uma linha por requisição, com o txID e o
// carimbo visíveis — dados que o middleware não tem, porque estão no corpo. Em
// vez de o handler escrever uma segunda linha, ele anota o que sabe e o
// middleware a compõe: o formato do registro fica em um lugar só.
type respostaObservada struct {
	http.ResponseWriter
	status  int
	detalhe string
}

func (r *respostaObservada) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// anotar guarda o detalhe da operação para a linha do registro.
//
// Aceita qualquer ResponseWriter e ignora quem não for a nossa: um teste de
// unidade que chame o handler direto com um httptest.Recorder continua
// funcionando, e o handler não precisa saber por onde está sendo servido.
func anotar(w http.ResponseWriter, formato string, args ...any) {
	if obs, nossa := w.(*respostaObservada); nossa {
		obs.detalhe = fmt.Sprintf(formato, args...)
	}
}
