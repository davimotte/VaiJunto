package servidor

import (
	"encoding/json"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// rotear despacha a requisição para o handler do seu tipo (camada 4,
// PROJETO.md seção 5.1).
//
// As regras de acesso da seção 4 do PROTOCOL.md são aplicadas antes do
// switch, em um só lugar, por sessao.autorizar. Nenhum handler repete
// checagem de autenticação ou de perfil: quando um deles roda, já se sabe que
// a conexão podia executar aquela operação.
//
// CANCELAR_CARONA e as operações de passageiro ainda não estão na tabela de
// perfis, então caem em TIPO_DESCONHECIDO; entram junto com seus handlers.
func rotear(req protocolo.Requisicao, estado *dominio.Estado, s *sessao) protocolo.Resposta {
	if recusa := s.autorizar(req); recusa != nil {
		return *recusa
	}

	switch req.Tipo {
	case protocolo.TipoPing:
		return tratarPing(req)
	case protocolo.TipoLogin:
		return tratarLogin(req, estado, s)
	case protocolo.TipoLogout:
		return tratarLogout(req, s)
	case protocolo.TipoPublicarCarona:
		return tratarPublicarCarona(req, estado, s)
	case protocolo.TipoListarMinhasCaronas:
		return tratarListarMinhasCaronas(req, estado, s)
	case protocolo.TipoDetalharCarona:
		return tratarDetalharCarona(req, estado, s)
	default:
		// Inalcançável: autorizar já recusou todo tipo fora da tabela de
		// perfis. Fica como rede de segurança para o caso de a tabela ganhar
		// uma entrada sem o case correspondente.
		return respostaErro(req.ID, protocolo.CodigoTipoDesconhecido, "Operação desconhecida: "+req.Tipo+".")
	}
}

// tratarPing responde ao diagnóstico PING (PROTOCOL.md, seção 5.1). Não
// exige autenticação.
func tratarPing(req protocolo.Requisicao) protocolo.Resposta {
	return respostaOK(req.ID, protocolo.PingResposta{ServidorEm: time.Now()})
}

// respostaOK monta uma Resposta de sucesso serializando dados no campo
// "dados" (PROTOCOL.md, seção 2.2).
//
// Falha de serialização vira ERRO_INTERNO em vez de derrubar a conexão: as
// structs de resposta são todas serializáveis, então cair aqui significaria
// um defeito do servidor, e o cliente merece uma resposta a respeito em vez
// do silêncio de uma conexão fechada.
func respostaOK(id string, dados any) protocolo.Resposta {
	b, err := json.Marshal(dados)
	if err != nil {
		return respostaErro(id, protocolo.CodigoErroInterno, "Falha ao serializar a resposta.")
	}
	return protocolo.Resposta{ID: id, Status: protocolo.StatusOK, Dados: b}
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
