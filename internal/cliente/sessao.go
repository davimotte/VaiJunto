package cliente

import (
	"errors"
	"fmt"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// Peças que os dois menus compartilham: o fuso das cidades, a escolha de
// cidade, a coleta de paradas, o laço de login e a política de tratamento de
// erro.

// nomeFusoDasCidades é o fuso das cidades atendidas (D09), todas na Bahia.
const nomeFusoDasCidades = "America/Bahia"

// FusoDasCidades devolve o fuso em que os horários digitados pelo motorista
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
func FusoDasCidades() *time.Location {
	if fuso, err := time.LoadLocation(nomeFusoDasCidades); err == nil {
		return fuso
	}
	return time.FixedZone("-03", -3*60*60)
}

// EscolherCidade mostra as cidades atendidas enumeradas e devolve a grafia
// canônica.
//
// O usuário escolhe por número e nunca digita o nome, que é a premissa da
// seção 3 do PROTOCOL.md: o servidor compara cidades por igualdade exata de
// string e não normaliza grafia, então a string precisa sair do cliente já
// canônica. É o mesmo princípio que faz o passageiro escolher itinerário por
// número em vez de digitar carona_id.
func EscolherCidade(term *Terminal, titulo string) (string, error) {
	cidades := dominio.CidadesAtendidas()
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

// ColetarParadas pergunta, parada por parada, a cidade e o horário da carona
// que o motorista vai publicar (D09; PROTOCOL.md, seção 5.4).
//
// Duas regras de rota do servidor são repetidas aqui como recusa imediata:
// cidade que já está na rota e horário que não é posterior ao da parada
// anterior fazem a pergunta se repetir (D15), em vez de montar uma requisição
// que só poderia voltar como ROTA_INVALIDA depois de o motorista digitar a rota
// inteira e os preços. Não é uma segunda validação que substitua a do servidor
// — um cliente escrito em outra linguagem não passa por aqui —, é só o menu
// poupando o operador de um dedo errado.
//
// A opção de encerrar só entra no menu a partir da terceira parada, quando já
// existe uma carona possível; e, com todas as cidades na rota, a coleta
// termina sozinha, porque nenhuma outra poderia entrar sem repetir.
//
// O fuso vem de quem chama, pelo mesmo motivo de LerInstante.
func ColetarParadas(term *Terminal, fuso *time.Location) ([]protocolo.Parada, error) {
	cidades := dominio.CidadesAtendidas()
	comEncerrar := append(append([]string(nil), cidades...), "Encerrar a rota aqui")
	encerrar := len(cidades)

	var paradas []protocolo.Parada
	naRota := make(map[string]bool, len(cidades))

	for len(paradas) < len(cidades) {
		numero := len(paradas) + 1
		titulo := fmt.Sprintf("Cidade da parada %d:", numero)
		opcoes := cidades
		if len(paradas) >= 2 {
			opcoes = comEncerrar
		}

		escolha, err := term.LerOpcao(titulo, opcoes)
		if err != nil {
			return nil, err
		}
		if escolha == encerrar {
			break
		}

		cidade := cidades[escolha]
		if naRota[cidade] {
			term.recusar(fmt.Sprintf("%s já está na rota. Uma carona não passa duas vezes pela mesma cidade.", cidade))
			continue
		}

		horario, err := lerHorarioDaParada(term, numero, paradas, fuso)
		if err != nil {
			return nil, err
		}
		paradas = append(paradas, protocolo.Parada{Cidade: cidade, Horario: horario})
		naRota[cidade] = true
	}
	return paradas, nil
}

// lerHorarioDaParada pede o horário da parada numero e repete a pergunta
// enquanto ele não for posterior ao da parada anterior.
//
// Só o horário se repete: a cidade já foi aceita, e reabri-la obrigaria a
// redigitar um dado correto. A data é perguntada em toda parada porque uma
// carona noturna atravessa a meia-noite, e deduzir o dia seguinte a partir de
// uma hora "menor" seria adivinhar o que o motorista quis dizer.
func lerHorarioDaParada(term *Terminal, numero int, anteriores []protocolo.Parada, fuso *time.Location) (time.Time, error) {
	for {
		horario, err := term.LerInstante(
			fmt.Sprintf("Data da parada %d (AAAA-MM-DD): ", numero),
			fmt.Sprintf("Hora da parada %d (HH:MM): ", numero),
			fuso)
		if err != nil {
			return time.Time{}, err
		}
		if len(anteriores) == 0 {
			return horario, nil
		}
		anterior := anteriores[len(anteriores)-1]
		// Estritamente depois, e comparado com After: um horário igual seria um
		// trecho de duração zero, e o servidor o recusaria do mesmo jeito.
		if horario.After(anterior.Horario) {
			return horario, nil
		}
		term.recusar(fmt.Sprintf("O horário precisa ser posterior ao da parada anterior (%s, %s).",
			anterior.Cidade, FormatarInstante(anterior.Horario)))
	}
}
