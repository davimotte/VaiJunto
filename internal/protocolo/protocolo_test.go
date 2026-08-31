package protocolo

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
// fracionária.
func TestInstanteFormatoRFC3339(t *testing.T) {
	fuso := time.FixedZone("-03:00", -3*60*60)
	partida := time.Date(2026, 9, 15, 8, 0, 0, 0, fuso)

	b, err := json.Marshal(PublicarCaronaRequisicao{
		Origem:         "Salvador",
		Destino:        "Vitória da Conquista",
		Partida:        partida,
		Assentos:       3,
		PrecosCentavos: []int{3000, 5000, 4000},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(b, []byte(`"partida":"2026-09-15T08:00:00-03:00"`)) {
		t.Fatalf("formato de instante inesperado: %s", b)
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
