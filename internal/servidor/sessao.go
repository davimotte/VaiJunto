package servidor

import (
	"encoding/json"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// sessao é a camada 3 do PROJETO.md (seção 5.1) e a materialização de D08:
// a identidade do cliente é uma variável local da goroutine que atende a
// conexão, e não um token nem uma entrada em tabela de sessões.
//
// Consequências de projeto, todas verificáveis lendo este arquivo: nenhuma
// requisição depois do LOGIN carrega credencial; não há expiração a controlar
// nem coleta de sessão vencida; e quando a conexão cai, a goroutine termina e
// a sessão deixa de existir junto, sem que ninguém precise limpá-la.
//
// Como a sessao vive dentro de uma única goroutine, seus campos não precisam
// de sincronização: nenhuma outra goroutine tem como alcançá-los.
type sessao struct {
	autenticado bool
	usuario     dominio.Usuario
}

// Guarda uma cópia do Usuario, não o ponteiro do estado. Os perfis são
// imutáveis após o boot (D12), então o ponteiro seria seguro na prática, mas a
// cópia elimina a discussão: nada que a sessão consulta a cada requisição
// aponta para dentro da estrutura protegida pelo mutex.

// Exigências de perfil por operação (PROTOCOL.md, seção 5).
//
// A tabela reproduz uma a uma as linhas daquela seção, e listar aqui é a
// única forma de uma operação ser aceita: o tipo ausente da tabela responde
// TIPO_DESCONHECIDO. Por isso ela contém somente o que este servidor já
// atende — as operações de reserva entram junto com seus handlers.
const (
	exigeNada         = ""            // PING e LOGIN
	exigeAutenticacao = "AUTENTICADO" // qualquer perfil, desde que logado
)

var perfilExigido = map[string]string{
	protocolo.TipoPing:                exigeNada,
	protocolo.TipoLogin:               exigeNada,
	protocolo.TipoLogout:              exigeAutenticacao,
	protocolo.TipoPublicarCarona:      protocolo.PerfilMotorista,
	protocolo.TipoListarMinhasCaronas: protocolo.PerfilMotorista,
	protocolo.TipoDetalharCarona:      protocolo.PerfilMotorista,
}

// autorizar aplica as regras de acesso da seção 4 do PROTOCOL.md antes de
// qualquer handler rodar. Devolve nil quando a operação pode prosseguir.
//
// A ordem das checagens importa: primeiro se a operação existe, depois se a
// conexão está autenticada, e só então se o perfil serve. Responder
// PERFIL_INCORRETO a quem sequer fez LOGIN diria ao cliente que ele errou o
// perfil quando o que faltou foi autenticar.
//
// Tipo inexistente responde TIPO_DESCONHECIDO mesmo sem login: a regra "só
// LOGIN e PING são aceitos antes de autenticar" fala do conjunto de operações
// do protocolo, e uma operação que não existe não pertence a ele em nenhum
// estado da conexão.
func (s *sessao) autorizar(req protocolo.Requisicao) *protocolo.Resposta {
	exigencia, conhecida := perfilExigido[req.Tipo]
	if !conhecida {
		resp := respostaErro(req.ID, protocolo.CodigoTipoDesconhecido, "Operação desconhecida: "+req.Tipo+".")
		return &resp
	}

	if req.Tipo == protocolo.TipoLogin && s.autenticado {
		resp := respostaErro(req.ID, protocolo.CodigoJaAutenticado, "Esta conexão já está autenticada. Use LOGOUT antes de entrar com outro usuário.")
		return &resp
	}

	if exigencia == exigeNada {
		return nil
	}
	if !s.autenticado {
		resp := respostaErro(req.ID, protocolo.CodigoNaoAutenticado, "Autentique-se antes de executar esta operação.")
		return &resp
	}
	if exigencia != exigeAutenticacao && s.usuario.Perfil != exigencia {
		resp := respostaErro(req.ID, protocolo.CodigoPerfilIncorreto, "Operação exclusiva do perfil "+exigencia+".")
		return &resp
	}
	return nil
}

// tratarLogin autentica a conexão (PROTOCOL.md, seção 5.2).
//
// A verificação de "já autenticado" ficou em autorizar, junto com as demais
// regras de acesso, e não aqui: assim existe um único lugar no código onde a
// seção 4 do protocolo é aplicada.
func tratarLogin(req protocolo.Requisicao, estado *dominio.Estado, s *sessao) protocolo.Resposta {
	var pedido protocolo.LoginRequisicao
	if err := json.Unmarshal(req.Dados, &pedido); err != nil {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "Campos usuario e senha precisam ser strings.")
	}
	if pedido.Usuario == "" || pedido.Senha == "" {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "Informe usuario e senha.")
	}

	usuario, err := estado.Autenticar(pedido.Usuario, pedido.Senha)
	if err != nil {
		return respostaDeErroDeDominio(req.ID, err)
	}

	// A escrita na sessão só acontece depois do domínio aprovar: uma tentativa
	// recusada não pode deixar a conexão meio autenticada.
	s.autenticado = true
	s.usuario = usuario

	return respostaOK(req.ID, protocolo.LoginResposta{
		Usuario: usuario.Usuario,
		Nome:    usuario.Nome,
		Perfil:  usuario.Perfil,
	})
}

// tratarLogout desautentica a conexão sem fechá-la (PROTOCOL.md, seção 5.3).
//
// Não há nada a liberar no servidor além destes dois campos, e é justamente
// esse o argumento de D08: como não existe estado de domínio preso à sessão,
// sair equivale a esquecer quem era o cliente. A conexão segue aberta e pode
// receber um novo LOGIN.
func tratarLogout(req protocolo.Requisicao, s *sessao) protocolo.Resposta {
	s.autenticado = false
	s.usuario = dominio.Usuario{}
	return respostaOK(req.ID, struct{}{})
}
