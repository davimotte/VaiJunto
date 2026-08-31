package servidor

import (
	"encoding/json"
	"time"

	"vaijunto/internal/protocolo"
)

// rotear despacha a requisição para o handler do seu tipo (camada 4,
// PROJETO.md seção 5.1). Nesta fase só PING está implementado: qualquer
// outro tipo responde TIPO_DESCONHECIDO, mesmo os já definidos em
// internal/protocolo (como LOGIN) — o roteador ainda não tem um caso para
// eles. A camada de sessão (autenticação, NAO_AUTENTICADO) chega junto com o
// handler de LOGIN, na próxima fase.
func rotear(req protocolo.Requisicao) protocolo.Resposta {
	switch req.Tipo {
	case protocolo.TipoPing:
		return tratarPing(req)
	default:
		return respostaErro(req.ID, protocolo.CodigoTipoDesconhecido, "Operação desconhecida: "+req.Tipo+".")
	}
}

// tratarPing responde ao diagnóstico PING (PROTOCOL.md, seção 5.1). Não
// exige autenticação.
func tratarPing(req protocolo.Requisicao) protocolo.Resposta {
	dados, _ := json.Marshal(protocolo.PingResposta{ServidorEm: time.Now()})
	return protocolo.Resposta{ID: req.ID, Status: protocolo.StatusOK, Dados: dados}
}

// respostaErro monta uma Resposta de erro (PROTOCOL.md, seção 2.2). Dados
// vem sempre presente, mesmo vazio, para que o campo nunca falte na resposta.
func respostaErro(id, codigo, mensagem string) protocolo.Resposta {
	return protocolo.Resposta{
		ID:       id,
		Status:   protocolo.StatusErro,
		Codigo:   codigo,
		Mensagem: mensagem,
		Dados:    json.RawMessage("{}"),
	}
}
