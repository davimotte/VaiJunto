package cliente

import (
	"errors"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// Peças que os dois menus compartilham: o fuso do corredor, a escolha de
// cidade, o laço de login e a política de tratamento de erro.

// nomeFusoDoCorredor é o fuso das cidades do corredor (D09), todas na Bahia.
const nomeFusoDoCorredor = "America/Bahia"

// FusoDoCorredor devolve o fuso em que os horários digitados pelo motorista
// são interpretados.
//
// Precisa ser explícito, e não time.Local: dentro do contêiner Alpine o fuso
// local é UTC, e uma partida digitada como 08:00 viraria 08:00Z — três horas
// à frente do que o motorista quis dizer e do que o passageiro veria.
//
// Depende do import de time/tzdata no main do cliente. O deslocamento fixo é
// a rede de segurança para o caso de esse import sumir: perde o horário de
// verão hipotético, mas mantém o cliente utilizável em vez de recusar toda
// publicação.
func FusoDoCorredor() *time.Location {
	if fuso, err := time.LoadLocation(nomeFusoDoCorredor); err == nil {
		return fuso
	}
	return time.FixedZone("-03", -3*60*60)
}

// EscolherCidade mostra o corredor enumerado e devolve a grafia canônica.
//
// O usuário escolhe por número e nunca digita o nome, que é a premissa da
// seção 3 do PROTOCOL.md: o servidor compara cidades por igualdade exata de
// string e não normaliza grafia, então a string precisa sair do cliente já
// canônica. É o mesmo princípio que faz o passageiro escolher itinerário por
// número em vez de digitar carona_id.
func EscolherCidade(term *Terminal, titulo string) (string, error) {
	cidades := dominio.CidadesCorredor()
	escolhida, err := term.LerOpcao(titulo, cidades)
	if err != nil {
		return "", err
	}
	return cidades[escolhida], nil
}

// Entrar executa o LOGIN da sessão, repetindo a pergunta enquanto o servidor
// recusar as credenciais ou o perfil não for o exigido por este cliente.
//
// O laço só existe porque a conexão é única (D15): sem ele, uma senha errada
// obrigaria a reiniciar o processo e reabrir o socket. E é por causa dele que
// LOGOUT tem uso real no cliente — um LOGIN em conexão já autenticada
// responderia JA_AUTENTICADO (PROTOCOL.md, seção 4), então trocar de usuário
// exige desautenticar a conexão antes.
func Entrar(term *Terminal, conexao *Conexao, perfilExigido string) (protocolo.LoginResposta, error) {
	for {
		usuario, err := term.LerTexto("Usuário: ")
		if err != nil {
			return protocolo.LoginResposta{}, err
		}
		// A senha é lida com eco na tela: desligar o eco exigiria termios, e
		// portanto uma dependência externa, que o enunciado proíbe. As senhas
		// do protótipo são de teste e já ficam em texto claro no
		// usuarios.json (D12).
		senha, err := term.LerTexto("Senha: ")
		if err != nil {
			return protocolo.LoginResposta{}, err
		}

		login, err := conexao.Login(usuario, senha)
		if err != nil {
			var erroServidor *ErroServidor
			if errors.As(err, &erroServidor) {
				term.Imprimir("\n%s\n\n", erroServidor.Mensagem)
				continue
			}
			return protocolo.LoginResposta{}, err
		}

		if login.Perfil == perfilExigido {
			return login, nil
		}

		// O perfil é imutável e um usuário tem exatamente um (D12), então
		// insistir seria inútil: o servidor recusaria toda operação com
		// PERFIL_INCORRETO. Vale mais dizer isso agora, e desautenticar a
		// conexão para que a próxima tentativa não esbarre em JA_AUTENTICADO.
		term.Imprimir("\nO usuário %s tem perfil %s, e este cliente é do perfil %s.\n\n",
			login.Usuario, login.Perfil, perfilExigido)
		if err := conexao.Logout(); err != nil {
			return protocolo.LoginResposta{}, err
		}
	}
}

// TratarErro aplica a política de erro dos menus e devolve nil quando o menu
// pode continuar.
//
// A distinção é entre erro que o servidor explicou e erro que quebrou o
// canal. Um ErroServidor é uma recusa prevista pelo PROTOCOL.md — sem
// assento, prazo expirado, campo inválido —, e a conexão continua íntegra: a
// mensagem do protocolo é exibida como veio, e o usuário volta ao menu. Erro
// de transporte ou resposta fora de sincronia não tem conserto do lado do
// cliente e sobe para encerrar a sessão.
//
// A mensagem exibida é sempre a do servidor. Traduzir código de erro para um
// texto próprio do cliente duplicaria a tabela do internal/servidor e faria
// um cliente desatualizado explicar errado uma recusa que o servidor já
// explicou certo.
func TratarErro(term *Terminal, err error) error {
	if err == nil {
		return nil
	}
	var erroServidor *ErroServidor
	if errors.As(err, &erroServidor) {
		term.Imprimir("\n%s\n", erroServidor.Mensagem)
		return nil
	}
	return err
}

// RotaDoCorredor devolve as cidades percorridas de origem a destino, na
// ordem em que a carona passa por elas.
//
// Serve apenas para rotular a pergunta do preço de cada trecho na publicação:
// perguntar "preço de Salvador → Feira de Santana" é compreensível, e
// "preço do trecho 1" não é. A rota que vale continua sendo a que o servidor
// deriva e devolve na resposta (D09) — esta é a mesma derivação, feita pela
// mesma tabela do corredor, e não uma segunda regra.
//
// Também é ela que fixa quantos preços a requisição precisa levar: um a menos
// que o número de cidades. Derivar isso da mesma fonte que o servidor usa
// evita a recusa por ROTA_INVALIDA em contagem de preços.
func RotaDoCorredor(origem, destino string) []string {
	inicio, ok := dominio.IndiceCidade(origem)
	if !ok {
		return nil
	}
	fim, ok := dominio.IndiceCidade(destino)
	if !ok {
		return nil
	}

	passo := 1
	if fim < inicio {
		passo = -1
	}

	cidades := dominio.CidadesCorredor()
	rota := []string{}
	for i := inicio; ; i += passo {
		rota = append(rota, cidades[i])
		if i == fim {
			break
		}
	}
	return rota
}
