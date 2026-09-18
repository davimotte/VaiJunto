package protocolo

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

// TestRequisicaoRoundTrip cobre o caminho LOGIN: monta uma Requisicao com
// Dados tipados, escreve a linha, lê de volta e decodifica Dados no tipo
// concreto, conferindo que nada se perde na ida e volta.
func TestRequisicaoRoundTrip(t *testing.T) {
	dados, err := json.Marshal(LoginRequisicao{Usuario: "joao", Senha: "1234"})
	if err != nil {
		t.Fatalf("marshal de LoginRequisicao: %v", err)
	}
	original := Requisicao{ID: "req-1", Tipo: TipoLogin, Dados: dados}

	var buf bytes.Buffer
	if err := EscreverLinha(&buf, original); err != nil {
		t.Fatalf("EscreverLinha: %v", err)
	}
	if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		t.Fatalf("linha escrita não termina em \\n: %q", buf.String())
	}

	leitor := NovoLeitorMensagens(&buf)
	linha, err := leitor.LerLinha()
	if err != nil {
		t.Fatalf("LerLinha: %v", err)
	}

	lida, err := DecodificarRequisicao(linha)
	if err != nil {
		t.Fatalf("DecodificarRequisicao: %v", err)
	}
	if lida.ID != original.ID || lida.Tipo != original.Tipo {
		t.Fatalf("envelope divergente: got %+v, want id=%q tipo=%q", lida, original.ID, original.Tipo)
	}

	var login LoginRequisicao
	if err := json.Unmarshal(lida.Dados, &login); err != nil {
		t.Fatalf("unmarshal de Dados: %v", err)
	}
	if login != (LoginRequisicao{Usuario: "joao", Senha: "1234"}) {
		t.Fatalf("dados divergentes: got %+v", login)
	}
}

// TestRespostaRoundTripOK confere que uma resposta OK não carrega "codigo"
// nem "mensagem" na linha serializada, por causa do omitempty do envelope.
func TestRespostaRoundTripOK(t *testing.T) {
	dados, _ := json.Marshal(LoginResposta{Usuario: "joao", Nome: "João Silva", Perfil: PerfilMotorista})
	original := Resposta{ID: "req-1", Status: StatusOK, Dados: dados}

	var buf bytes.Buffer
	if err := EscreverLinha(&buf, original); err != nil {
		t.Fatalf("EscreverLinha: %v", err)
	}
	if strings.Contains(buf.String(), "codigo") || strings.Contains(buf.String(), "mensagem") {
		t.Fatalf("resposta OK não deveria ter codigo/mensagem: %q", buf.String())
	}

	leitor := NovoLeitorMensagens(&buf)
	linha, err := leitor.LerLinha()
	if err != nil {
		t.Fatalf("LerLinha: %v", err)
	}
	lida, err := DecodificarResposta(linha)
	if err != nil {
		t.Fatalf("DecodificarResposta: %v", err)
	}
	if lida.Status != StatusOK || lida.Codigo != "" || lida.Mensagem != "" {
		t.Fatalf("resposta divergente: %+v", lida)
	}

	var login LoginResposta
	if err := json.Unmarshal(lida.Dados, &login); err != nil {
		t.Fatalf("unmarshal de Dados: %v", err)
	}
	if login.Usuario != "joao" || login.Perfil != PerfilMotorista {
		t.Fatalf("dados divergentes: %+v", login)
	}
}

// TestRespostaRoundTripErro cobre uma resposta de erro com dados de detalhe
// (SemAssentoDados), no formato do exemplo da seção 5.9.
func TestRespostaRoundTripErro(t *testing.T) {
	dados, _ := json.Marshal(SemAssentoDados{CaronaID: "car-1", IndiceTrecho: 0})
	original := Resposta{
		ID:       "req-7",
		Status:   StatusErro,
		Codigo:   CodigoSemAssento,
		Mensagem: "Assento esgotado no trecho Salvador → Feira de Santana.",
		Dados:    dados,
	}

	var buf bytes.Buffer
	if err := EscreverLinha(&buf, original); err != nil {
		t.Fatalf("EscreverLinha: %v", err)
	}

	leitor := NovoLeitorMensagens(&buf)
	linha, err := leitor.LerLinha()
	if err != nil {
		t.Fatalf("LerLinha: %v", err)
	}
	lida, err := DecodificarResposta(linha)
	if err != nil {
		t.Fatalf("DecodificarResposta: %v", err)
	}
	if lida.Status != StatusErro || lida.Codigo != CodigoSemAssento {
		t.Fatalf("resposta divergente: %+v", lida)
	}

	var detalhe SemAssentoDados
	if err := json.Unmarshal(lida.Dados, &detalhe); err != nil {
		t.Fatalf("unmarshal de Dados: %v", err)
	}
	if detalhe.CaronaID != "car-1" || detalhe.IndiceTrecho != 0 {
		t.Fatalf("detalhe divergente: %+v", detalhe)
	}
}

// TestInstanteFormatoRFC3339 confere que um instante sem fração de segundo
// serializa exatamente como os exemplos do PROTOCOL.md (seção 3), sem parte
// fracionária, e com os nomes de campo de PUBLICAR_CARONA (seção 5.4).
func TestInstanteFormatoRFC3339(t *testing.T) {
	fuso := time.FixedZone("-03:00", -3*60*60)

	b, err := json.Marshal(PublicarCaronaRequisicao{
		Paradas: []Parada{
			{Cidade: "Salvador", Horario: time.Date(2026, 9, 15, 8, 0, 0, 0, fuso)},
			{Cidade: "Feira de Santana", Horario: time.Date(2026, 9, 15, 10, 15, 0, 0, fuso)},
		},
		Assentos:       3,
		PrecosCentavos: []int{3000},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(b, []byte(`"paradas":[{"cidade":"Salvador","horario":"2026-09-15T08:00:00-03:00"}`)) {
		t.Fatalf("formato de parada ou de instante inesperado: %s", b)
	}
}

// TestLerLinha_IgnoraLinhasVazias confere que LerLinha pula linhas em branco
// silenciosamente e devolve a próxima mensagem real.
func TestLerLinha_IgnoraLinhasVazias(t *testing.T) {
	entrada := "\n\n" + `{"id":"1","tipo":"PING","dados":{}}` + "\n"
	leitor := NovoLeitorMensagens(strings.NewReader(entrada))

	linha, err := leitor.LerLinha()
	if err != nil {
		t.Fatalf("LerLinha: %v", err)
	}
	if string(linha) != `{"id":"1","tipo":"PING","dados":{}}` {
		t.Fatalf("linha inesperada: %q", linha)
	}
}

// TestLerLinha_LinhaMuitoGrande confere que uma linha maior que
// TamanhoMaximoLinha, sem '\n' dentro do limite, produz ErrLinhaMuitoGrande.
func TestLerLinha_LinhaMuitoGrande(t *testing.T) {
	grande := strings.Repeat("x", TamanhoMaximoLinha+100) + "\n"
	leitor := NovoLeitorMensagens(strings.NewReader(grande))

	_, err := leitor.LerLinha()
	if err != ErrLinhaMuitoGrande {
		t.Fatalf("erro esperado ErrLinhaMuitoGrande, got %v", err)
	}
}

// TestLerLinha_NoLimiteExato confere que uma linha exatamente no limite
// ainda é aceita (o buffer é TamanhoMaximoLinha+1 de propósito).
func TestLerLinha_NoLimiteExato(t *testing.T) {
	// "{}" mais preenchimento de espaços até ocupar TamanhoMaximoLinha bytes,
	// seguido do delimitador.
	conteudo := "{" + strings.Repeat(" ", TamanhoMaximoLinha-2) + "}"
	if len(conteudo) != TamanhoMaximoLinha {
		t.Fatalf("tamanho de fixture errado: %d", len(conteudo))
	}
	leitor := NovoLeitorMensagens(strings.NewReader(conteudo + "\n"))

	linha, err := leitor.LerLinha()
	if err != nil {
		t.Fatalf("LerLinha não deveria falhar no limite exato: %v", err)
	}
	if len(linha) != TamanhoMaximoLinha {
		t.Fatalf("tamanho de linha inesperado: %d", len(linha))
	}
}

// TestLinhaMalformada confere o comportamento da seção 1: a linha malformada
// ainda é entregue por LerLinha (o enquadramento não valida JSON), mas falha
// ao decodificar como envelope — é esse erro que a camada de roteamento usa
// para responder JSON_INVALIDO mantendo a conexão aberta.
func TestLinhaMalformada(t *testing.T) {
	entrada := `{"id":"1","tipo":` + "\n" // JSON truncado, ainda tem uma linha
	leitor := NovoLeitorMensagens(strings.NewReader(entrada))

	linha, err := leitor.LerLinha()
	if err != nil {
		t.Fatalf("LerLinha não deveria falhar em linha malformada: %v", err)
	}

	if _, err := DecodificarRequisicao(linha); err == nil {
		t.Fatalf("DecodificarRequisicao deveria falhar para JSON malformado: %q", linha)
	}
}

// TestLinhaMalformada_NaoJSON cobre uma linha que não é JSON algum.
func TestLinhaMalformada_NaoJSON(t *testing.T) {
	leitor := NovoLeitorMensagens(strings.NewReader("isso não é json\n"))

	linha, err := leitor.LerLinha()
	if err != nil {
		t.Fatalf("LerLinha: %v", err)
	}
	if _, err := DecodificarRequisicao(linha); err == nil {
		t.Fatalf("DecodificarRequisicao deveria falhar")
	}
}

// TestLerLinha_MultiplasMensagens confere que chamadas sucessivas de
// LerLinha avançam corretamente por várias linhas na mesma conexão.
func TestLerLinha_MultiplasMensagens(t *testing.T) {
	entrada := `{"id":"1","tipo":"PING","dados":{}}` + "\n" +
		`{"id":"2","tipo":"LOGOUT","dados":{}}` + "\n"
	leitor := NovoLeitorMensagens(strings.NewReader(entrada))

	primeira, err := leitor.LerLinha()
	if err != nil {
		t.Fatalf("LerLinha (1): %v", err)
	}
	segunda, err := leitor.LerLinha()
	if err != nil {
		t.Fatalf("LerLinha (2): %v", err)
	}

	req1, err := DecodificarRequisicao(primeira)
	if err != nil || req1.Tipo != TipoPing {
		t.Fatalf("primeira requisição inesperada: %+v, err=%v", req1, err)
	}
	req2, err := DecodificarRequisicao(segunda)
	if err != nil || req2.Tipo != TipoLogout {
		t.Fatalf("segunda requisição inesperada: %+v, err=%v", req2, err)
	}

	if _, err := leitor.LerLinha(); err == nil {
		t.Fatalf("esperava EOF ao final do stream")
	}
}

// --- Envelope: decodificação em dois estágios (PROTOCOL.md, seções 2.1 e 2.2) ---

// TestDecodificarRequisicao_Valida confere o caminho feliz do envelope: os três
// campos presentes, com "dados" objeto, não produzem erro.
func TestDecodificarRequisicao_Valida(t *testing.T) {
	req, err := DecodificarRequisicao([]byte(`{"id":"1","tipo":"PING","dados":{}}`))
	if err != nil {
		t.Fatalf("envelope válido não deveria falhar: %v", err)
	}
	if req.ID != "1" || req.Tipo != TipoPing || string(req.Dados) != "{}" {
		t.Fatalf("envelope decodificado divergente: %+v", req)
	}
}

// TestDecodificarRequisicao_JSONInvalido confere que uma linha que não decodifica
// como JSON produz ErrJSONInvalido, e não ErrEnvelopeInvalido: são códigos
// distintos na seção 6.
func TestDecodificarRequisicao_JSONInvalido(t *testing.T) {
	casos := map[string]string{
		"não é json alguma coisa": "isso não é json",
		"objeto truncado":         `{"id":"1","tipo":`,
		"chave sem valor":         `{"id":}`,
	}
	for nome, linha := range casos {
		req, err := DecodificarRequisicao([]byte(linha))
		if !errors.Is(err, ErrJSONInvalido) {
			t.Errorf("%s: err = %v, want ErrJSONInvalido", nome, err)
		}
		if req.ID != "" {
			t.Errorf("%s: ID = %q, want \"\" (não houve como ler o id)", nome, req.ID)
		}
	}
}

// TestDecodificarRequisicao_NaoObjeto confere que um JSON válido que não é um
// objeto é ENVELOPE_INVALIDO, e não JSON_INVALIDO: a linha decodifica, mas não
// como envelope. Não há id a ecoar.
func TestDecodificarRequisicao_NaoObjeto(t *testing.T) {
	for _, linha := range []string{`[1,2,3]`, `5`, `"texto"`, `null`, `true`} {
		req, err := DecodificarRequisicao([]byte(linha))
		if !errors.Is(err, ErrEnvelopeInvalido) {
			t.Errorf("linha %q: err = %v, want ErrEnvelopeInvalido", linha, err)
		}
		if req.ID != "" {
			t.Errorf("linha %q: ID = %q, want \"\"", linha, req.ID)
		}
	}
}

// TestDecodificarRequisicao_IDEcoadoEmErroDeEnvelope é o teste da divergência 3:
// o id é extraído no primeiro estágio, antes da validação de tipo e dados, para
// que a resposta de erro possa ecoá-lo (seção 2.2: `id` só é "" quando não foi
// possível lê-lo).
func TestDecodificarRequisicao_IDEcoadoEmErroDeEnvelope(t *testing.T) {
	casos := map[string]string{
		"tipo com tipo errado": `{"id":"req-7","tipo":5,"dados":{}}`,
		"tipo ausente":         `{"id":"req-7","dados":{}}`,
		"tipo vazio":           `{"id":"req-7","tipo":"","dados":{}}`,
		"dados ausente":        `{"id":"req-7","tipo":"PING"}`,
		"dados nulo":           `{"id":"req-7","tipo":"PING","dados":null}`,
	}
	for nome, linha := range casos {
		req, err := DecodificarRequisicao([]byte(linha))
		if !errors.Is(err, ErrEnvelopeInvalido) {
			t.Errorf("%s: err = %v, want ErrEnvelopeInvalido", nome, err)
		}
		if req.ID != "req-7" {
			t.Errorf("%s: ID = %q, want %q (o id era legível)", nome, req.ID, "req-7")
		}
	}
}

// TestDecodificarRequisicao_DadosNaoObjeto é o teste da divergência 2: a seção
// 2.1 exige que `dados` seja um objeto, vazio quando a operação não tem campos.
// Ausente, nulo, número, string, booleano ou array reprovam o envelope.
func TestDecodificarRequisicao_DadosNaoObjeto(t *testing.T) {
	casos := map[string]string{
		"ausente":  `{"id":"9","tipo":"PING"}`,
		"nulo":     `{"id":"9","tipo":"PING","dados":null}`,
		"número":   `{"id":"9","tipo":"PING","dados":5}`,
		"string":   `{"id":"9","tipo":"PING","dados":"x"}`,
		"booleano": `{"id":"9","tipo":"PING","dados":true}`,
		"array":    `{"id":"9","tipo":"PING","dados":[1,2]}`,
	}
	for nome, linha := range casos {
		req, err := DecodificarRequisicao([]byte(linha))
		if !errors.Is(err, ErrEnvelopeInvalido) {
			t.Errorf("dados %s: err = %v, want ErrEnvelopeInvalido", nome, err)
		}
		if req.ID != "9" {
			t.Errorf("dados %s: ID = %q, want %q", nome, req.ID, "9")
		}
	}
}

// TestDecodificarRequisicao_IDInvalido confere que um id ausente, vazio ou com
// tipo errado reprova o envelope e não tem o que ecoar.
func TestDecodificarRequisicao_IDInvalido(t *testing.T) {
	casos := map[string]string{
		"ausente":  `{"tipo":"PING","dados":{}}`,
		"vazio":    `{"id":"","tipo":"PING","dados":{}}`,
		"numérico": `{"id":7,"tipo":"PING","dados":{}}`,
		"nulo":     `{"id":null,"tipo":"PING","dados":{}}`,
		"objeto":   `{"id":{"a":1},"tipo":"PING","dados":{}}`,
	}
	for nome, linha := range casos {
		req, err := DecodificarRequisicao([]byte(linha))
		if !errors.Is(err, ErrEnvelopeInvalido) {
			t.Errorf("id %s: err = %v, want ErrEnvelopeInvalido", nome, err)
		}
		if req.ID != "" {
			t.Errorf("id %s: ID = %q, want \"\"", nome, req.ID)
		}
	}
}

// TestLerLinha_FatiaNaoAliasaBuffer confere a regra do PROJETO.md, seção 5.1:
// a fatia devolvida por ReadSlice aponta para o buffer interno do
// bufio.Reader, então LerLinha precisa devolver uma cópia.
//
// O leitor é embrulhado em iotest.OneByteReader de propósito. Com um
// strings.Reader puro, a entrada inteira entra no buffer num único fill e a
// segunda leitura não reescreve nada — o teste passaria mesmo sem a cópia. É
// entregando os bytes aos poucos, como um socket faz, que a segunda leitura
// força fill a deslocar o que restou para o início do buffer, sobrescrevendo
// exatamente a região onde a primeira linha estava.
func TestLerLinha_FatiaNaoAliasaBuffer(t *testing.T) {
	primeiraEsperada := `{"id":"1","tipo":"PING","dados":{}}`
	entrada := primeiraEsperada + "\n" +
		`{"id":"2","tipo":"LOGOUT","dados":{"preenchimento":"zzzzzzzzzzzzzzzzzzzz"}}` + "\n"
	leitor := NovoLeitorMensagens(iotest.OneByteReader(strings.NewReader(entrada)))

	primeira, err := leitor.LerLinha()
	if err != nil {
		t.Fatalf("LerLinha (1): %v", err)
	}
	if _, err := leitor.LerLinha(); err != nil {
		t.Fatalf("LerLinha (2): %v", err)
	}

	if string(primeira) != primeiraEsperada {
		t.Fatalf("primeira linha corrompida pela leitura seguinte (fatia sem cópia): got %q, want %q", primeira, primeiraEsperada)
	}
}

// --- Fim de stream: mensagem só existe se terminar em '\n' (seção 1) ---

// TestLerLinha_EOFLimpo confere que o fim de stream logo após uma linha
// completa é EOF puro, e não linha incompleta: é a desconexão normal da seção
// 4, sem nada pendente para descartar.
func TestLerLinha_EOFLimpo(t *testing.T) {
	leitor := NovoLeitorMensagens(strings.NewReader(`{"id":"1","tipo":"PING","dados":{}}` + "\n"))

	if _, err := leitor.LerLinha(); err != nil {
		t.Fatalf("LerLinha (1): %v", err)
	}

	_, err := leitor.LerLinha()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
	if errors.Is(err, ErrLinhaIncompleta) {
		t.Fatalf("EOF limpo não deveria virar ErrLinhaIncompleta")
	}
}

// TestLerLinha_LinhaIncompletaNoEOF confere a regra da seção 1: bytes
// pendentes sem o terminador no momento do EOF são descartados, e o erro é
// distinto do EOF limpo para que o servidor consiga registrar que o cliente
// encerrou no meio de uma mensagem.
func TestLerLinha_LinhaIncompletaNoEOF(t *testing.T) {
	// Sem o '\n' final, mesmo sendo um JSON sintaticamente completo.
	leitor := NovoLeitorMensagens(strings.NewReader(`{"id":"1","tipo":"PING","dados":{}}`))

	linha, err := leitor.LerLinha()
	if !errors.Is(err, ErrLinhaIncompleta) {
		t.Fatalf("err = %v, want ErrLinhaIncompleta", err)
	}
	if linha != nil {
		t.Fatalf("linha incompleta não deveria ser entregue ao chamador: %q", linha)
	}
}

// TestLerLinha_CompletasAntesDaIncompleta confere que só o fragmento final é
// descartado: as mensagens que chegaram inteiras antes dele são entregues
// normalmente.
func TestLerLinha_CompletasAntesDaIncompleta(t *testing.T) {
	entrada := `{"id":"1","tipo":"PING","dados":{}}` + "\n" +
		`{"id":"2","tipo":"LOGOUT","dados":{}}` + "\n" +
		`{"id":"3","tipo":"PIN` // cortada no meio
	leitor := NovoLeitorMensagens(strings.NewReader(entrada))

	for i := 1; i <= 2; i++ {
		linha, err := leitor.LerLinha()
		if err != nil {
			t.Fatalf("LerLinha (%d): %v", i, err)
		}
		req, err := DecodificarRequisicao(linha)
		if err != nil {
			t.Fatalf("DecodificarRequisicao (%d): %v", i, err)
		}
		if req.ID != string(rune('0'+i)) {
			t.Fatalf("LerLinha (%d): ID = %q", i, req.ID)
		}
	}

	if _, err := leitor.LerLinha(); !errors.Is(err, ErrLinhaIncompleta) {
		t.Fatalf("err = %v, want ErrLinhaIncompleta", err)
	}
}
