package federacao

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vaijunto/internal/dominio"
	"vaijunto/internal/transacao"
)

// Testes da borda REST do participante (API_CONSORCIO.md, seções 4.3 a 4.5).
//
// Aqui se afirma o que é responsabilidade **da borda**: papel de quem chama,
// forma do corpo, forma da resposta e tradução de erro de domínio em voto ou em
// status. As regras de significado — idempotência, assentos, veto — têm os seus
// testes em internal/dominio, sobre o estado, onde o instante é parâmetro.
//
// O coordenador é sempre `rotacapixaba`, a única empresa em --pares da
// configuração de teste, e o dono é `vaijunto`.

const txDeTeste = "rotacapixaba:res-3f0b11c4"

// configDeTransacoes é a configuração de vaijunto com o estado dado e o
// participante ligado a ele, como o boot monta (cmd/servidor/main.go).
func configDeTransacoes(estado *dominio.Estado) Config {
	cfg := configDeTeste()
	cfg.Estado = estado
	cfg.Participante = transacao.NovoParticipante(estado, nil)
	return cfg
}

// pedirTransacao roda um recurso de transação pelo handler completo.
func pedirTransacao(cfg Config, quemChama, txID, recurso string, corpo any) *httptest.ResponseRecorder {
	var leitor io.Reader
	if corpo != nil {
		bruto, err := json.Marshal(corpo)
		if err != nil {
			panic(err)
		}
		leitor = bytes.NewReader(bruto)
	}
	// O txID vai no caminho sem codificação (seção 1.4 da API).
	req := httptest.NewRequest(http.MethodPost, "/transacoes/"+txID+"/"+recurso, leitor)
	return pedir(cfg, comCabecalhos(req, quemChama, 7))
}

// corpoPreparar monta o corpo da seção 4.3 como map, para que cada teste possa
// remover ou trocar um campo sem precisar de um tipo por caso.
func corpoPreparar(itens ...map[string]any) map[string]any {
	return map[string]any{
		"coordenador":   "rotacapixaba",
		"participantes": []string{"rotacapixaba", "vaijunto"},
		"carimbo":       map[string]any{"lamport": 41, "empresa": "rotacapixaba"},
		"passageiro":    map[string]any{"ref": "rotacapixaba:maria", "nome": "Maria Souza"},
		"itens":         itens,
	}
}

func itemDeFio(carona string, de, ate int) map[string]any {
	return map[string]any{"carona_id": carona, "de": de, "ate": ate}
}

// votoDaResposta decodifica o corpo de um preparar que respondeu 200.
func votoDaResposta(t *testing.T, rec *httptest.ResponseRecorder) respostaVoto {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var voto respostaVoto
	if err := json.Unmarshal(rec.Body.Bytes(), &voto); err != nil {
		t.Fatalf("decodificar voto: %v (corpo: %s)", err, rec.Body)
	}
	return voto
}

// O voto SIM traz as pernas na forma da seção 3.3, com todas as paradas de De a
// Ate — é com elas que o coordenador verifica cidade repetida (D34).
func TestPreparar_VotoSimTrazAsPernas(t *testing.T) {
	cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))

	rec := pedirTransacao(cfg, "rotacapixaba", txDeTeste, "preparar",
		corpoPreparar(itemDeFio("vaijunto:car-1", 0, 2)))

	voto := votoDaResposta(t, rec)
	if voto.Voto != "SIM" || len(voto.Pernas) != 1 {
		t.Fatalf("voto = %+v, want SIM com 1 perna", voto)
	}
	p := voto.Pernas[0]
	if p.CaronaID != "vaijunto:car-1" || p.Empresa != "vaijunto" {
		t.Errorf("perna = %+v, want carona vaijunto:car-1 da vaijunto", p)
	}
	if p.MotoristaNome != "Rafael Mendonça Pires" {
		t.Errorf("motorista_nome = %q, want o nome resolvido pelo dono", p.MotoristaNome)
	}
	if got := strings.Join(p.Cidades, "|"); got != "Vitória da Conquista|Vitória|Cachoeiro de Itapemirim" {
		t.Errorf("cidades = %q, want as três paradas de 0 a 2", got)
	}
	if p.PrecoCentavos != 18000+3500 {
		t.Errorf("preco_centavos = %d, want 21500", p.PrecoCentavos)
	}
	if !p.Partida.Equal(instante(5, 14, 0)) || !p.Chegada.Equal(instante(6, 6, 30)) {
		t.Errorf("partida %v e chegada %v, want 05/11 14:00 e 06/11 06:30", p.Partida, p.Chegada)
	}
}

// Repetir o preparar devolve o mesmo SIM (D28): é o que torna seguro repetir
// depois de uma resposta perdida.
func TestPreparar_RepetidoDevolveOMesmoSim(t *testing.T) {
	cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))
	corpo := corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1))

	primeiro := pedirTransacao(cfg, "rotacapixaba", txDeTeste, "preparar", corpo).Body.String()
	segundo := pedirTransacao(cfg, "rotacapixaba", txDeTeste, "preparar", corpo).Body.String()

	if primeiro != segundo {
		t.Errorf("a repetição respondeu diferente:\n%s\n%s", primeiro, segundo)
	}
}

// O voto NÃO vem num 200, com motivo, carona e trecho (seções 2 e 4.3 da API).
//
// O trecho 0 tem de aparecer no JSON: é o primeiro trecho de toda carona, e um
// omitempty o apagaria justamente onde ele é mais comum. O teste olha o corpo
// cru, porque decodificar para a struct esconderia a ausência do campo.
func TestPreparar_VotoNaoTrazMotivoETrechoZero(t *testing.T) {
	cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))

	// A carona tem três assentos: os três primeiros levam o trecho 0.
	for _, tx := range []string{"rotacapixaba:res-00000001", "rotacapixaba:res-00000002", "rotacapixaba:res-00000003"} {
		if voto := votoDaResposta(t, pedirTransacao(cfg, "rotacapixaba", tx, "preparar",
			corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1)))); voto.Voto != "SIM" {
			t.Fatalf("%s: voto = %q, want SIM", tx, voto.Voto)
		}
	}

	rec := pedirTransacao(cfg, "rotacapixaba", "rotacapixaba:res-00000004", "preparar",
		corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1)))

	voto := votoDaResposta(t, rec)
	if voto.Voto != "NAO" || voto.Motivo != motivoSemAssento {
		t.Fatalf("voto = %+v, want NAO/SEM_ASSENTO", voto)
	}
	if voto.CaronaID != "vaijunto:car-1" || voto.Trecho == nil || *voto.Trecho != 0 {
		t.Fatalf("voto = %+v, want carona_id vaijunto:car-1 e trecho 0", voto)
	}
	if !strings.Contains(rec.Body.String(), `"trecho":0`) {
		t.Errorf("o corpo omitiu o trecho 0: %s", rec.Body)
	}
	if len(voto.Pernas) != 0 {
		t.Errorf("o voto NÃO veio com pernas: %+v", voto.Pernas)
	}
}

// Papel (seção 1.2): preparar, confirmar e abortar só do coordenador, que é a
// empresa do prefixo do txID. É a verificação que não pode viver no domínio,
// porque depende do cabeçalho e do caminho.
func TestTransacoes_PapelInvalidoEm403(t *testing.T) {
	cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))

	for _, recurso := range []string{"preparar", "confirmar", "abortar"} {
		t.Run(recurso, func(t *testing.T) {
			// Quem chama é rotacapixaba, mas a transação é coordenada por
			// rotafluminense: o prefixo é o que manda.
			rec := pedirTransacao(cfg, "rotacapixaba", "rotafluminense:res-1", recurso,
				corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1)))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
			}
			if got := codigoDoErro(t, rec); got != codigoPapelInvalido {
				t.Errorf("codigo = %q, want %q", got, codigoPapelInvalido)
			}
		})
	}
}

// Nada é aplicado quando o papel é recusado: a recusa vem antes de o corpo ser
// lido, e portanto antes de o participante ser chamado.
func TestTransacoes_PapelInvalidoNaoTocaOEstado(t *testing.T) {
	estado := parComUmaCarona(t, "vaijunto")
	cfg := configDeTransacoes(estado)

	pedirTransacao(cfg, "rotacapixaba", "rotafluminense:res-1", "preparar",
		corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1)))

	if n := len(estado.Participacoes()); n != 0 {
		t.Errorf("participações = %d, want 0", n)
	}
	for _, o := range estado.Ocupacao() {
		if o.Livres != o.Assentos {
			t.Errorf("%s trecho %d com livres = %d, want %d", o.CaronaID, o.Trecho, o.Livres, o.Assentos)
		}
	}
}

// Cada recusa de forma do corpo, da tabela da seção 4.3, e o txID sem prefixo,
// que não tem papel a comparar.
func TestPreparar_CorpoInvalidoEm400(t *testing.T) {
	semCampo := func(campo string) map[string]any {
		corpo := corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1))
		delete(corpo, campo)
		return corpo
	}

	casos := []struct {
		nome  string
		txID  string
		corpo any
	}{
		{"sem coordenador", txDeTeste, semCampo("coordenador")},
		{"sem participantes", txDeTeste, semCampo("participantes")},
		{"sem carimbo", txDeTeste, semCampo("carimbo")},
		{"sem passageiro", txDeTeste, semCampo("passageiro")},
		{"sem itens", txDeTeste, semCampo("itens")},
		{"carimbo sem lamport", txDeTeste, func() map[string]any {
			corpo := corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1))
			corpo["carimbo"] = map[string]any{"empresa": "rotacapixaba"}
			return corpo
		}()},
		{"passageiro sem nome", txDeTeste, func() map[string]any {
			corpo := corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1))
			corpo["passageiro"] = map[string]any{"ref": "rotacapixaba:maria"}
			return corpo
		}()},
		{"item sem ate", txDeTeste, corpoPreparar(map[string]any{"carona_id": "vaijunto:car-1", "de": 0})},
		{"coordenador divergente do caminho", txDeTeste, func() map[string]any {
			corpo := corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1))
			corpo["coordenador"] = "rotafluminense"
			return corpo
		}()},
		{"lista de participantes vazia", txDeTeste, func() map[string]any {
			corpo := corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1))
			corpo["participantes"] = []string{}
			return corpo
		}()},
		{"nenhum item", txDeTeste, corpoPreparar()},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			estado := parComUmaCarona(t, "vaijunto")
			rec := pedirTransacao(configDeTransacoes(estado), "rotacapixaba", caso.txID, "preparar", caso.corpo)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
			}
			if got := codigoDoErro(t, rec); got != codigoCorpoInvalido {
				t.Errorf("codigo = %q, want %q", got, codigoCorpoInvalido)
			}
			if n := len(estado.Participacoes()); n != 0 {
				t.Errorf("a recusa criou %d participação(ões)", n)
			}
		})
	}
}

// Corpo que não é JSON, e txID sem prefixo de empresa: os dois são 400, e o
// segundo não chega nem a ter papel a comparar.
func TestPreparar_CorpoIlegivelETxSemPrefixo(t *testing.T) {
	cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))

	req := httptest.NewRequest(http.MethodPost, "/transacoes/"+txDeTeste+"/preparar",
		strings.NewReader("{isto não é json"))
	rec := pedir(cfg, comCabecalhos(req, "rotacapixaba", 7))
	if rec.Code != http.StatusBadRequest || codigoDoErro(t, rec) != codigoCorpoInvalido {
		t.Errorf("corpo ilegível: status %d e código %q, want 400 CORPO_INVALIDO", rec.Code, codigoDoErro(t, rec))
	}

	rec = pedirTransacao(cfg, "rotacapixaba", "res-sem-empresa", "preparar",
		corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1)))
	if rec.Code != http.StatusBadRequest || codigoDoErro(t, rec) != codigoCorpoInvalido {
		t.Errorf("tx sem prefixo: status %d e código %q, want 400 CORPO_INVALIDO", rec.Code, codigoDoErro(t, rec))
	}
}

// Item de carona de outra empresa é erro do coordenador, que agrupou as pernas
// por dono errado: 400 ITEM_DE_OUTRA_EMPRESA (seção 4.3), e não voto NÃO.
func TestPreparar_ItemDeOutraEmpresaEm400(t *testing.T) {
	cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))

	rec := pedirTransacao(cfg, "rotacapixaba", txDeTeste, "preparar",
		corpoPreparar(itemDeFio("rotafluminense:car-1", 0, 1)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	if got := codigoDoErro(t, rec); got != codigoItemDeOutraEmpresa {
		t.Errorf("codigo = %q, want %q", got, codigoItemDeOutraEmpresa)
	}
}

// Cada motivo de voto NÃO da seção 5.2 que o participante produz nesta fase,
// com o status 200 que a seção 2 exige: voto não é erro.
func TestPreparar_MotivosDeVotoNaoEm200(t *testing.T) {
	for _, caso := range []struct {
		nome   string
		item   map[string]any
		motivo string
	}{
		{"carona inexistente", itemDeFio("vaijunto:car-99", 0, 1), motivoCaronaNaoEncontrada},
		{"índices fora da rota", itemDeFio("vaijunto:car-1", 0, 9), motivoItinerarioInvalido},
		{"de maior ou igual a ate", itemDeFio("vaijunto:car-1", 1, 1), motivoItinerarioInvalido},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))

			voto := votoDaResposta(t, pedirTransacao(cfg, "rotacapixaba", txDeTeste, "preparar",
				corpoPreparar(caso.item)))
			if voto.Voto != "NAO" || voto.Motivo != caso.motivo {
				t.Errorf("voto = %+v, want NAO/%s", voto, caso.motivo)
			}
		})
	}
}

// --- Fase 2: confirmar e abortar (seções 4.4 e 4.5) ---

// O caminho normal da fase 2, e a repetição, que responde a mesma situação.
func TestConfirmar_RespondeSituacaoERepeteIgual(t *testing.T) {
	cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))
	pedirTransacao(cfg, "rotacapixaba", txDeTeste, "preparar",
		corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1)))

	for i := 0; i < 2; i++ {
		rec := pedirTransacao(cfg, "rotacapixaba", txDeTeste, "confirmar", map[string]any{})
		if rec.Code != http.StatusOK {
			t.Fatalf("chamada %d: status = %d, want 200: %s", i+1, rec.Code, rec.Body)
		}
		var resposta respostaSituacao
		if err := json.Unmarshal(rec.Body.Bytes(), &resposta); err != nil {
			t.Fatalf("decodificar: %v", err)
		}
		if resposta.Situacao != "CONFIRMADO" {
			t.Errorf("chamada %d: situacao = %q, want CONFIRMADO", i+1, resposta.Situacao)
		}
	}
}

// Abortar transação desconhecida grava o veto (D38) e responde ABORTADO; o
// preparar que chegar depois recebe NÃO com TRANSACAO_ABORTADA (seção 5.2).
func TestAbortar_VetoERecusaDoPrepararAtrasado(t *testing.T) {
	cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))

	rec := pedirTransacao(cfg, "rotacapixaba", txDeTeste, "abortar", map[string]any{})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"situacao":"ABORTADO"`) {
		t.Fatalf("abortar: status %d, corpo %s", rec.Code, rec.Body)
	}

	voto := votoDaResposta(t, pedirTransacao(cfg, "rotacapixaba", txDeTeste, "preparar",
		corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1))))
	if voto.Voto != "NAO" || voto.Motivo != motivoTransacaoAbortada {
		t.Errorf("voto = %+v, want NAO/TRANSACAO_ABORTADA", voto)
	}
}

// --- CONSULTAR (seção 4.6) ---

// consultaDaResposta decodifica o corpo de um consultar que respondeu 200.
func consultaDaResposta(t *testing.T, rec *httptest.ResponseRecorder) respostaConsulta {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var corpo respostaConsulta
	if err := json.Unmarshal(rec.Body.Bytes(), &corpo); err != nil {
		t.Fatalf("decodificar consulta: %v (corpo: %s)", err, rec.Body)
	}
	return corpo
}

// A tabela do participante da seção 4.6 pela borda: o papel vem do prefixo do
// txID, não de quem chamou, e a situação é a da participação.
//
// Aqui o dono é vaijunto e a transação é coordenada por rotacapixaba, então
// vaijunto responde sempre como PARTICIPANTE.
func TestConsultar_ParticipanteRespondeASuaSituacao(t *testing.T) {
	for _, caso := range []struct {
		nome        string
		montar      func(Config)
		querSituaca string
	}{
		{
			nome: "preparado",
			montar: func(cfg Config) {
				pedirTransacao(cfg, "rotacapixaba", txDeTeste, "preparar",
					corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1)))
			},
			querSituaca: "PREPARADO",
		},
		{
			nome: "confirmado",
			montar: func(cfg Config) {
				pedirTransacao(cfg, "rotacapixaba", txDeTeste, "preparar",
					corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1)))
				pedirTransacao(cfg, "rotacapixaba", txDeTeste, "confirmar", map[string]any{})
			},
			querSituaca: "CONFIRMADO",
		},
		{
			nome: "abortado",
			montar: func(cfg Config) {
				pedirTransacao(cfg, "rotacapixaba", txDeTeste, "preparar",
					corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1)))
				pedirTransacao(cfg, "rotacapixaba", txDeTeste, "abortar", map[string]any{})
			},
			querSituaca: "ABORTADO",
		},
		{
			nome:        "desconhecida, responde DESCONHECIDA",
			montar:      func(Config) {},
			querSituaca: "DESCONHECIDA",
		},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))
			caso.montar(cfg)

			corpo := consultaDaResposta(t, pedirTransacao(cfg, "rotacapixaba", txDeTeste, "consultar", map[string]any{}))
			if corpo.Papel != "PARTICIPANTE" {
				t.Errorf("papel = %q, want PARTICIPANTE", corpo.Papel)
			}
			if corpo.Situacao != caso.querSituaca {
				t.Errorf("situacao = %q, want %q", corpo.Situacao, caso.querSituaca)
			}
			// O corpo de um participante não leva decisao: os dois campos se
			// excluem, e é o papel que diz qual ler (seção 4.6).
			if corpo.Decisao != "" {
				t.Errorf("decisao = %q num corpo de participante, want vazia", corpo.Decisao)
			}
		})
	}
}

// A tabela do coordenador da seção 4.6 pela borda. Aqui a transação é
// coordenada por vaijunto, que é a empresa deste servidor.
//
// A terceira linha é a mais importante: transação que nunca existiu responde
// ABORTADA **e não grava nada**. É o abort presumido (D23) visto de fora, e é
// o que a seção 7 do API_CONSORCIO.md demonstra com curl.
func TestConsultar_CoordenadorRespondeADecisao(t *testing.T) {
	const txLocal = "vaijunto:res-0000abcd"

	for _, caso := range []struct {
		nome    string
		montar  func(*dominio.Estado)
		querDec string
	}{
		{
			nome:    "desconhecida, abort presumido",
			montar:  func(*dominio.Estado) {},
			querDec: "ABORTADA",
		},
		{
			nome:    "em votacao",
			montar:  func(e *dominio.Estado) { e.MarcarTransacaoEmVotacao(txLocal) },
			querDec: "EM_ANDAMENTO",
		},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			estado := parComUmaCarona(t, "vaijunto")
			cfg := configDeTransacoes(estado)
			caso.montar(estado)

			corpo := consultaDaResposta(t, pedirTransacao(cfg, "rotacapixaba", txLocal, "consultar", map[string]any{}))
			if corpo.Papel != "COORDENADOR" {
				t.Errorf("papel = %q, want COORDENADOR", corpo.Papel)
			}
			if corpo.Decisao != caso.querDec {
				t.Errorf("decisao = %q, want %q", corpo.Decisao, caso.querDec)
			}
			if corpo.Situacao != "" {
				t.Errorf("situacao = %q num corpo de coordenador, want vazia", corpo.Situacao)
			}
			// Responder ABORTADA não cria participação nenhuma: o coordenador
			// não grava nada antes de decidir (D23, D37, regra 1). Se gravasse,
			// um PREPARAR legítimo que chegasse depois seria recusado por um
			// veto que ninguém pediu.
			if n := len(estado.Participacoes()); n != 0 {
				t.Errorf("a consulta ao coordenador criou %d participação(ões), want 0", n)
			}
		})
	}
}

// O veto da D38 pela borda: consultar uma transação desconhecida grava
// ABORTADO antes de responder, e o preparar atrasado recebe NÃO.
//
// É o que torna a terminação segura, e é o passo que o diagrama da seção 6.3
// da API mostra: a rotafluminense nunca recebeu o preparar, é consultada,
// veta, e com isso autoriza quem perguntou a abortar.
func TestConsultar_DesconhecidaVetaERecusaOPrepararAtrasado(t *testing.T) {
	estado := parComUmaCarona(t, "vaijunto")
	cfg := configDeTransacoes(estado)

	corpo := consultaDaResposta(t, pedirTransacao(cfg, "rotacapixaba", txDeTeste, "consultar", map[string]any{}))
	if corpo.Situacao != "DESCONHECIDA" {
		t.Fatalf("situacao = %q, want DESCONHECIDA", corpo.Situacao)
	}

	pt, existe := estado.Participacao(txDeTeste)
	if !existe || pt.Situacao != dominio.SituacaoAbortado || len(pt.Itens) != 0 {
		t.Fatalf("veto = %+v (existe=%t), want ABORTADO sem itens", pt, existe)
	}

	voto := votoDaResposta(t, pedirTransacao(cfg, "rotacapixaba", txDeTeste, "preparar",
		corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1))))
	if voto.Voto != "NAO" || voto.Motivo != motivoTransacaoAbortada {
		t.Errorf("voto = %+v, want NAO/TRANSACAO_ABORTADA", voto)
	}
}

// Consultar é o único recurso de transação aceito de **qualquer** empresa do
// consórcio (seção 1.2), e não só do coordenador: quem é consultado sobre uma
// transação desconhecida não tem como saber quem participa dela, logo não teria
// como saber de quem aceitar a pergunta.
//
// O contraste com preparar/confirmar/abortar, que respondem 403 no mesmo
// cenário, está em TestTransacoes_PapelInvalidoEm403.
func TestConsultar_AceitoDeQualquerEmpresaDoConsorcio(t *testing.T) {
	cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))

	// rotacapixaba pergunta sobre uma transação coordenada por rotafluminense:
	// não é o coordenador e mesmo assim é atendida.
	corpo := consultaDaResposta(t, pedirTransacao(cfg, "rotacapixaba", "rotafluminense:res-1", "consultar", map[string]any{}))
	if corpo.Papel != "PARTICIPANTE" || corpo.Situacao != "DESCONHECIDA" {
		t.Errorf("resposta = %+v, want PARTICIPANTE/DESCONHECIDA", corpo)
	}
}

// A abertura é para o consórcio, não para o mundo: quem não está em --pares
// continua recusado pelo middleware, como em qualquer outro recurso (D20).
func TestConsultar_EmpresaForaDosParesEm403(t *testing.T) {
	estado := parComUmaCarona(t, "vaijunto")
	cfg := configDeTransacoes(estado)

	rec := pedirTransacao(cfg, "intrusa", txDeTeste, "consultar", map[string]any{})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
	}
	if got := codigoDoErro(t, rec); got != codigoEmpresaDesconhecida {
		t.Errorf("codigo = %q, want %q", got, codigoEmpresaDesconhecida)
	}
	// E nada foi gravado: a recusa vem antes de o participante ser chamado,
	// então um estranho não consegue vetar transações alheias.
	if n := len(estado.Participacoes()); n != 0 {
		t.Errorf("a consulta recusada gravou %d participação(ões), want 0", n)
	}
}

// txID sem prefixo não tem papel a resolver: o caminho é malformado, e a
// resposta é 400, não 403 (seção 5.1).
func TestConsultar_TxSemPrefixoEm400(t *testing.T) {
	cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))

	rec := pedirTransacao(cfg, "rotacapixaba", "res-sem-empresa", "consultar", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	if got := codigoDoErro(t, rec); got != codigoCorpoInvalido {
		t.Errorf("codigo = %q, want %q", got, codigoCorpoInvalido)
	}
}

// A violação de protocolo (seção 7.4) responde 409 e é registrada (seção 5.1).
//
// 409 é o único status que encerra a repetição (seção 1.3): nenhuma
// retransmissão corrige uma transição impossível.
func TestTransacoes_ViolacaoEm409ERegistrada(t *testing.T) {
	for _, caso := range []struct {
		nome     string
		preparar func(Config)
		recurso  string
	}{
		{
			nome:     "confirmar transação desconhecida",
			preparar: func(Config) {},
			recurso:  "confirmar",
		},
		{
			nome: "confirmar transação abortada",
			preparar: func(cfg Config) {
				pedirTransacao(cfg, "rotacapixaba", txDeTeste, "abortar", map[string]any{})
			},
			recurso: "confirmar",
		},
		{
			nome: "abortar transação confirmada",
			preparar: func(cfg Config) {
				pedirTransacao(cfg, "rotacapixaba", txDeTeste, "preparar",
					corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1)))
				pedirTransacao(cfg, "rotacapixaba", txDeTeste, "confirmar", map[string]any{})
			},
			recurso: "abortar",
		},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			var saida bytes.Buffer
			cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))
			cfg.Registro = NovoRegistro(&saida, cfg.Empresa)
			caso.preparar(cfg)

			rec := pedirTransacao(cfg, "rotacapixaba", txDeTeste, caso.recurso, map[string]any{})
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body)
			}
			if got := codigoDoErro(t, rec); got != codigoViolacaoProtocolo {
				t.Errorf("codigo = %q, want %q", got, codigoViolacaoProtocolo)
			}
			if !strings.Contains(saida.String(), "VIOLACAO_PROTOCOLO de rotacapixaba") {
				t.Errorf("registro sem a linha de violação: %s", saida.String())
			}
		})
	}
}

// O registro de operações traz, na mesma linha, o Lamport recebido, o txID, o
// carimbo e o voto: é por ela que o 2PC fica visível na demonstração
// (seção 5.2).
func TestPreparar_RegistroTrazTxCarimboEVoto(t *testing.T) {
	var saida bytes.Buffer
	cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))
	cfg.Registro = NovoRegistro(&saida, cfg.Empresa)

	pedirTransacao(cfg, "rotacapixaba", txDeTeste, "preparar",
		corpoPreparar(itemDeFio("vaijunto:car-1", 0, 1)))

	linha := saida.String()
	for _, parte := range []string{
		"[vaijunto]", "← rotacapixaba", "POST /transacoes/" + txDeTeste + "/preparar",
		"L=8", "tx=" + txDeTeste, "carimbo=(41,rotacapixaba)", "voto=SIM", "→ 200",
	} {
		if !strings.Contains(linha, parte) {
			t.Errorf("linha do registro sem %q: %s", parte, linha)
		}
	}
	if n := strings.Count(strings.TrimSpace(linha), "\n"); n != 0 {
		t.Errorf("o registro escreveu %d linhas para uma requisição: %s", n+1, linha)
	}
}

// Método errado num recurso de transação é RECURSO_INEXISTENTE, como em
// qualquer outro (seção 5.1): o padrão do ServeMux inclui o método.
func TestTransacoes_MetodoErradoEm404(t *testing.T) {
	cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))

	req := httptest.NewRequest(http.MethodGet, "/transacoes/"+txDeTeste+"/preparar", nil)
	rec := pedir(cfg, comCabecalhos(req, "rotacapixaba", 7))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body)
	}
	if got := codigoDoErro(t, rec); got != codigoRecursoInexistente {
		t.Errorf("codigo = %q, want %q", got, codigoRecursoInexistente)
	}
}

// Os recursos de transação não são isentos de cabeçalho (seção 1.1): sem
// X-VaiJunto-Empresa não há papel a conferir.
func TestTransacoes_SemCabecalhosEm400(t *testing.T) {
	cfg := configDeTransacoes(parComUmaCarona(t, "vaijunto"))

	req := httptest.NewRequest(http.MethodPost, "/transacoes/"+txDeTeste+"/preparar", nil)
	rec := pedir(cfg, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	if got := codigoDoErro(t, rec); got != codigoCabecalhoInvalido {
		t.Errorf("codigo = %q, want %q", got, codigoCabecalhoInvalido)
	}
}
