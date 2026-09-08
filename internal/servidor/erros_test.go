package servidor

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// TestTraducaoCobreTodosOsSentinelas é o teste exigido pelo PROJETO.md
// (seção 5.3): todo erro sentinela exportado por internal/dominio precisa ter
// entrada na tabela de tradução.
//
// Sem ele, acrescentar um sentinela no domínio e esquecer da tabela faria o
// servidor responder ERRO_INTERNO a uma falha perfeitamente prevista, e nada
// quebraria — é a divergência silenciosa que a seção 5.3 quer impedir. O
// domínio não pode importar o protocolo, então este teste é o único ponto em
// que as duas pontas são confrontadas.
func TestTraducaoCobreTodosOsSentinelas(t *testing.T) {
	for _, sentinela := range dominio.TodosOsErros() {
		traducao, ok := traducaoErros[sentinela]
		if !ok {
			t.Errorf("sentinela %v não tem entrada em traducaoErros", sentinela)
			continue
		}
		if traducao.Codigo == "" || traducao.Mensagem == "" {
			t.Errorf("sentinela %v: entrada incompleta %+v", sentinela, traducao)
		}
	}
}

// TestTraducaoUsaCodigosDoProtocolo confere que todo código da tabela existe
// mesmo na seção 6 do PROTOCOL.md, e não é uma string inventada aqui.
func TestTraducaoUsaCodigosDoProtocolo(t *testing.T) {
	conhecidos := map[string]bool{
		protocolo.CodigoJSONInvalido: true, protocolo.CodigoEnvelopeInvalido: true,
		protocolo.CodigoTipoDesconhecido: true, protocolo.CodigoCampoInvalido: true,
		protocolo.CodigoNaoAutenticado: true, protocolo.CodigoJaAutenticado: true,
		protocolo.CodigoCredenciaisInvalidas: true, protocolo.CodigoPerfilIncorreto: true,
		protocolo.CodigoCidadeDesconhecida: true, protocolo.CodigoRotaInvalida: true,
		protocolo.CodigoPartidaInvalida: true, protocolo.CodigoCaronaNaoEncontrada: true,
		protocolo.CodigoCaronaCancelada: true, protocolo.CodigoNaoEDono: true,
		protocolo.CodigoItinerarioInvalido: true, protocolo.CodigoSemAssento: true,
		protocolo.CodigoConflitoHorario: true, protocolo.CodigoReservaNaoEncontrada: true,
		protocolo.CodigoReservaJaCancelada: true, protocolo.CodigoPrazoCancelamentoExpirado: true,
		protocolo.CodigoErroInterno: true,
	}
	for sentinela, traducao := range traducaoErros {
		if !conhecidos[traducao.Codigo] {
			t.Errorf("sentinela %v traduz para o código %q, que não está na seção 6", sentinela, traducao.Codigo)
		}
	}
}

// TestRespostaDeErroDeDominio_ReconheceErroEmbrulhado confere que a tradução
// atravessa o %w: o domínio embrulha os sentinelas para acrescentar contexto,
// e uma comparação por igualdade em vez de errors.Is deixaria todo erro real
// cair em ERRO_INTERNO.
func TestRespostaDeErroDeDominio_ReconheceErroEmbrulhado(t *testing.T) {
	embrulhado := fmt.Errorf("%w: origem %q", dominio.ErrCidadeDesconhecida, "Ilhéus")

	resp := respostaDeErroDeDominio("7", embrulhado)
	if resp.Codigo != protocolo.CodigoCidadeDesconhecida {
		t.Fatalf("codigo = %q, want %q", resp.Codigo, protocolo.CodigoCidadeDesconhecida)
	}
	if resp.ID != "7" {
		t.Fatalf("ID = %q, want %q", resp.ID, "7")
	}
}

// TestRespostaDeErroDeDominio_DesconhecidoViraErroInterno confere o destino de
// um erro que não é sentinela nenhum: ERRO_INTERNO, nunca um código plausível
// escolhido por proximidade.
func TestRespostaDeErroDeDominio_DesconhecidoViraErroInterno(t *testing.T) {
	resp := respostaDeErroDeDominio("7", errors.New("falha qualquer"))
	if resp.Codigo != protocolo.CodigoErroInterno {
		t.Fatalf("codigo = %q, want %q", resp.Codigo, protocolo.CodigoErroInterno)
	}
}

// TestTodaOperacaoDaTabelaTemHandler confere que a tabela perfilExigido e o
// switch de rotear não divergem: um tipo listado na tabela sem case
// correspondente passaria pela autorização e cairia no default, respondendo
// TIPO_DESCONHECIDO a uma operação que o servidor diz suportar.
//
// O payload vai vazio de propósito. O que se afirma aqui não é que a operação
// funcione, e sim que ela foi reconhecida: qualquer resposta serve, menos
// TIPO_DESCONHECIDO.
func TestTodaOperacaoDaTabelaTemHandler(t *testing.T) {
	estado := dominio.NovoEstado()

	for tipo, exigencia := range perfilExigido {
		sess := &sessao{}
		if exigencia != exigeNada {
			perfil := protocolo.PerfilMotorista
			if exigencia != exigeAutenticacao {
				perfil = exigencia
			}
			sess.autenticado = true
			sess.usuario = dominio.Usuario{Usuario: "fulano", Nome: "Fulano", Perfil: perfil}
		}

		req := protocolo.Requisicao{ID: "1", Tipo: tipo, Dados: json.RawMessage("{}")}
		resp := rotear(req, estado, sess)
		if resp.Codigo == protocolo.CodigoTipoDesconhecido {
			t.Errorf("tipo %q está em perfilExigido mas não tem case em rotear", tipo)
		}
	}
}
