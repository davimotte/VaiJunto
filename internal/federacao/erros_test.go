package federacao

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"vaijunto/internal/dominio"
)

// TestTraducaoCobreTodosOsSentinelas é o teste exigido pela seção 5.5 do
// PROJETO_P2.md, o mesmo que internal/servidor/erros_test.go faz para a borda
// TCP: todo sentinela exportado por internal/dominio tem entrada na tabela.
//
// Sem ele, acrescentar um sentinela no domínio e esquecer desta tabela faria o
// servidor responder ERRO_INTERNO a uma falha prevista — e, no 2PC, um
// ERRO_INTERNO manda o coordenador repetir (seção 1.3) uma operação que
// deveria ter virado voto NÃO.
func TestTraducaoCobreTodosOsSentinelas(t *testing.T) {
	for _, sentinela := range dominio.TodosOsErros() {
		traducao, ok := traducaoErros[sentinela]
		if !ok {
			t.Errorf("sentinela %v não tem entrada em traducaoErros", sentinela)
			continue
		}
		if traducao.Codigo == "" || traducao.Mensagem == "" || traducao.Status == 0 {
			t.Errorf("sentinela %v: entrada incompleta %+v", sentinela, traducao)
		}
	}
}

// Todo código da tabela existe mesmo na seção 5.1 (erros HTTP) ou na 5.2
// (motivos do voto NÃO) do API_CONSORCIO.md, e não é uma string inventada aqui.
//
// O status também é conferido contra o documento: um motivo de voto só pode
// sair com 200, porque voto NÃO é "requisição processada" (seção 2), e um erro
// HTTP nunca pode sair com 200, ou o coordenador leria recusa como sucesso.
func TestTraducaoUsaCodigosDaAPI(t *testing.T) {
	errosHTTP := map[string]bool{
		codigoCabecalhoInvalido: true, codigoCorpoInvalido: true,
		codigoParametroInvalido: true, codigoItemDeOutraEmpresa: true,
		codigoNaoCoordenador: true, codigoEmpresaDesconhecida: true,
		codigoPapelInvalido: true, codigoRecursoInexistente: true,
		codigoViolacaoProtocolo: true, codigoCorpoGrande: true,
		codigoErroInterno: true,
	}
	motivosDeVoto := map[string]bool{
		motivoSemAssento: true, motivoPrecedencia: true,
		motivoCaronaNaoEncontrada: true, motivoCaronaCancelada: true,
		motivoItinerarioInvalido: true, motivoTransacaoAbortada: true,
	}

	for sentinela, traducao := range traducaoErros {
		switch {
		case motivosDeVoto[traducao.Codigo]:
			if traducao.Status != http.StatusOK {
				t.Errorf("sentinela %v: motivo de voto %q com status %d, want 200", sentinela, traducao.Codigo, traducao.Status)
			}
		case errosHTTP[traducao.Codigo]:
			if traducao.Status == http.StatusOK {
				t.Errorf("sentinela %v: erro %q com status 200", sentinela, traducao.Codigo)
			}
		default:
			t.Errorf("sentinela %v traduz para o código %q, que não está na seção 5.1 nem na 5.2", sentinela, traducao.Codigo)
		}
	}
}

// A tradução atravessa o %w: o domínio embrulha os sentinelas para acrescentar
// contexto, e comparar por igualdade em vez de errors.Is deixaria todo erro
// real cair em ERRO_INTERNO.
func TestErroHTTPDeDominio_ReconheceErroEmbrulhado(t *testing.T) {
	embrulhado := fmt.Errorf("%w: carona %q", dominio.ErrCaronaCancelada, "rotacapixaba:car-1")

	got := erroHTTPDeDominio(embrulhado)
	if got.Codigo != motivoCaronaCancelada {
		t.Errorf("codigo = %q, want %q", got.Codigo, motivoCaronaCancelada)
	}
}

// Erro que não é sentinela nenhum vira ERRO_INTERNO, nunca um código
// plausível escolhido por proximidade.
func TestErroHTTPDeDominio_DesconhecidoViraErroInterno(t *testing.T) {
	got := erroHTTPDeDominio(errors.New("falha qualquer"))

	if got.Status != http.StatusInternalServerError || got.Codigo != codigoErroInterno {
		t.Errorf("erro desconhecido traduzido para %+v, want 500 ERRO_INTERNO", got)
	}
}

// Um motivo de voto que escape para o caminho de erro vira 500, e não uma
// resposta 200 com cara de erro: a seção 2 da API separa "processou e
// respondeu" de "não respondeu direito", e o coordenador decide repetir ou não
// com base nessa separação.
func TestResponderErro_MotivoDeVotoNaoViraResposta200(t *testing.T) {
	rec := httptest.NewRecorder()

	responderErro(rec, traducaoErros[dominio.ErrSemAssento])

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
