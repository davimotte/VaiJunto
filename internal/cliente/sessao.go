package cliente

import (
	"errors"
	"fmt"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// Peças que os dois menus compartilham: a escolha de cidade, a coleta de
// paradas, o laço de login e a política de tratamento de erro. O fuso das
// cidades vem de dominio.FusoDasCidades, o mesmo que o servidor usa.

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

// EscolherOrigemEDestino pergunta a origem e o destino de uma busca, repetindo
// a pergunta do destino enquanto ele for igual à origem.
//
// Segue D15: a recusa acontece no menu, e não depois de uma ida ao servidor
// que só poderia voltar como ROTA_INVALIDA. Só o destino se repete, porque a
// origem escolhida estava certa.
func EscolherOrigemEDestino(term *Terminal) (origem, destino string, err error) {
	origem, err = EscolherCidade(term, "Origem:")
	if err != nil {
		return "", "", err
	}
	for {
		destino, err = EscolherCidade(term, "Destino:")
		if err != nil {
			return "", "", err
		}
		if destino != origem {
			return origem, destino, nil
		}
		term.recusar("O destino precisa ser diferente da origem.")
	}
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
// As cidades que já estão na rota continuam no menu, marcadas com "(já na
// rota)", em vez de sumirem: a numeração fica a mesma em toda pergunta —
// Salvador é sempre 1, e encerrar é sempre a última opção —, o que importa a
// quem digita de memória, e a marca avisa antes da escolha, e não só depois.
//
// A opção de encerrar só entra no menu a partir da terceira parada, quando já
// existe uma carona possível; e, com todas as cidades na rota, a coleta
// termina sozinha, porque nenhuma outra poderia entrar sem repetir.
//
// O fuso vem de quem chama, pelo mesmo motivo de LerInstante.
func ColetarParadas(term *Terminal, fuso *time.Location) ([]protocolo.Parada, error) {
	cidades := dominio.CidadesAtendidas()
	encerrar := len(cidades)

	var paradas []protocolo.Parada
	naRota := make(map[string]bool, len(cidades))

	for len(paradas) < len(cidades) {
		numero := len(paradas) + 1
		titulo := fmt.Sprintf("Cidade da parada %d:", numero)

		opcoes := make([]string, 0, len(cidades)+1)
		for _, cidade := range cidades {
			if naRota[cidade] {
				cidade += " (já na rota)"
			}
			opcoes = append(opcoes, cidade)
		}
		if len(paradas) >= 2 {
			opcoes = append(opcoes, "Encerrar a rota aqui")
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
// redigitar um dado correto.
//
// A partir da segunda parada, Enter na data repete o dia da parada anterior,
// e o rótulo mostra qual é. É o caso comum, e poupa digitar a mesma data a cada
// parada. Uma carona noturna que atravessa a meia-noite continua explícita: o
// motorista digita o dia seguinte. O que o menu não faz é deduzir esse dia a
// partir de uma hora "menor" que a anterior — isso seria adivinhar, e um
// 07:00 digitado no lugar de 17:00 viraria, sem aviso, uma viagem de um dia
// para o outro. A primeira parada não tem data anterior, e nela a data é
// obrigatória.
func lerHorarioDaParada(term *Terminal, numero int, anteriores []protocolo.Parada, fuso *time.Location) (time.Time, error) {
	rotuloHora := fmt.Sprintf("Hora da parada %d (HH:MM): ", numero)
	if len(anteriores) == 0 {
		return term.LerInstante(fmt.Sprintf("Data da parada %d (AAAA-MM-DD): ", numero), rotuloHora, fuso)
	}

	anterior := anteriores[len(anteriores)-1]
	rotuloData := fmt.Sprintf("Data da parada %d (AAAA-MM-DD, Enter para %s): ",
		numero, anterior.Horario.In(fuso).Format(formatoData))
	for {
		horario, err := term.LerInstanteComDataPadrao(rotuloData, rotuloHora, anterior.Horario, fuso)
		if err != nil {
			return time.Time{}, err
		}
		// Estritamente depois, e comparado com After: um horário igual seria um
		// trecho de duração zero, e o servidor o recusaria do mesmo jeito.
		if horario.After(anterior.Horario) {
			return horario, nil
		}
		term.recusar(fmt.Sprintf("O horário precisa ser posterior ao da parada anterior (%s, %s).",
			anterior.Cidade, FormatarInstante(anterior.Horario)))
	}
}
