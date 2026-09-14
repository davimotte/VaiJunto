// Comando passageiro é o cliente de terminal do perfil PASSAGEIRO.
//
// É um menu numérico, e não um CLI de subcomandos (D15): a execução inteira
// vive sobre uma única conexão TCP, aberta antes do LOGIN e fechada depois do
// LOGOUT. Um processo por operação abriria uma conexão por comando e obrigaria
// a autenticar toda vez, esvaziando o argumento de D08 — que é justamente a
// conexão persistente tornar o token de sessão desnecessário.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	_ "time/tzdata" // fusos embutidos no binário: a imagem Alpine do cliente não traz tzdata, e sem eles FusoDasCidades cairia no deslocamento fixo.

	"vaijunto/internal/cliente"
	"vaijunto/internal/protocolo"
)

func main() {
	term := cliente.TerminalPadrao()
	endereco := cliente.EnderecoDoServidor()

	conexao, err := cliente.Conectar(endereco)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vaijunto: não foi possível conectar em %s: %v\n", endereco, err)
		fmt.Fprintf(os.Stderr, "vaijunto: defina %s com o endereço do servidor.\n", cliente.VariavelServidor)
		os.Exit(1)
	}
	defer func() { _ = conexao.Fechar() }()

	term.Imprimir("=== VAIJUNTO — Passageiro ===\nServidor: %s\n\n", endereco)

	login, err := cliente.Entrar(term, conexao, protocolo.PerfilPassageiro)
	if err != nil {
		encerrar(term, err)
		return
	}
	term.Imprimir("\nConectado a %s como %s.\n", endereco, login.Nome)

	if err := menu(term, conexao); err != nil {
		encerrar(term, err)
		return
	}

	// LOGOUT antes de fechar: é o encerramento previsto no fluxo da seção 4
	// do PROTOCOL.md. Fechar direto também seria correto — a seção 4 trata o
	// fechamento como desconexão normal —, mas o LOGOUT explícito deixa a
	// sessão terminar pelo caminho documentado.
	if err := conexao.Logout(); err != nil {
		encerrar(term, err)
		return
	}
	term.Imprimir("Até logo.\n")
}

// encerrar termina o processo diante de um erro que o menu não conseguiu
// tratar.
//
// Fim de entrada não é falha: acontece quando o terminal fecha ou a entrada
// redirecionada acaba, e o cliente sai com status zero. Qualquer outro erro
// aqui é de transporte ou de dessincronização — a conexão já não serve, e
// insistir nela produziria respostas pareadas com a requisição errada.
func encerrar(term *cliente.Terminal, err error) {
	if errors.Is(err, cliente.ErrEntradaEncerrada) {
		term.Imprimir("Até logo.\n")
		return
	}
	fmt.Fprintf(os.Stderr, "\nvaijunto: sessão encerrada: %v\n", err)
	os.Exit(1)
}

// menu é o laço principal. Devolve nil quando o usuário escolhe sair, e o
// erro que encerra a sessão nos demais casos.
func menu(term *cliente.Terminal, conexao *cliente.Conexao) error {
	opcoes := []string{
		"Buscar itinerários",
		"Minhas reservas",
		"Cancelar reserva",
		"Sair",
	}

	for {
		escolha, err := term.LerOpcao("O que você quer fazer?", opcoes)
		if err != nil {
			return err
		}

		var erroOperacao error
		switch escolha {
		case 0:
			erroOperacao = buscarEReservar(term, conexao)
		case 1:
			erroOperacao = listarReservas(term, conexao)
		case 2:
			erroOperacao = cancelarReserva(term, conexao)
		case 3:
			return nil
		}

		// Recusa do servidor é exibida com a mensagem do protocolo e o laço
		// continua; erro que quebrou o canal sobe e encerra a sessão.
		if err := cliente.TratarErro(term, erroOperacao); err != nil {
			return err
		}
	}
}

// buscarEReservar faz a busca da seção 5.8 e, se o passageiro escolher um
// resultado, a reserva da seção 5.9.
//
// As duas operações são independentes do lado do servidor: entre uma e outra
// nada fica reservado nem bloqueado (D07). Por isso o cliente não guarda
// estado de "reserva em andamento" — se outro passageiro levar o último
// assento nesse intervalo, a confirmação volta como SEM_ASSENTO e a mensagem
// do protocolo explica qual trecho esgotou.
func buscarEReservar(term *cliente.Terminal, conexao *cliente.Conexao) error {
	origem, err := cliente.EscolherCidade(term, "Origem:")
	if err != nil {
		return err
	}
	destino, err := cliente.EscolherCidade(term, "Destino:")
	if err != nil {
		return err
	}
	data, err := term.LerDataISO("\nData (AAAA-MM-DD): ")
	if err != nil {
		return err
	}

	resposta, err := conexao.BuscarItinerarios(origem, destino, data)
	if err != nil {
		return err
	}
	// Lista vazia é resposta OK, e não erro (seção 5.8): não há nada a
	// oferecer, e isso não é uma falha da consulta.
	if len(resposta.Itinerarios) == 0 {
		term.Imprimir("\nNenhum itinerário de %s para %s em %s.\n", origem, destino, data)
		return nil
	}

	term.Imprimir("\n%s:\n\n", cliente.Plural(len(resposta.Itinerarios), "itinerário encontrado", "itinerários encontrados"))
	var tabela cliente.Tabela
	for i, itinerario := range resposta.Itinerarios {
		// A linha em branco separa um bloco do seguinte, e por isso vem antes
		// de cada bloco menos o primeiro: depois do último ela só empurraria
		// a pergunta seguinte para baixo.
		if i > 0 {
			tabela.LinhaSolta("")
		}
		acrescentarItinerario(&tabela, i+1, itinerario)
	}
	tabela.Escrever(term)
	term.Imprimir("\n")

	escolha, err := term.LerEscolhaOuVoltar("Reservar qual? (0 para voltar): ", len(resposta.Itinerarios))
	if err != nil {
		return err
	}
	if escolha == cliente.VoltarAoMenu {
		return nil
	}

	escolhido := resposta.Itinerarios[escolha]

	// O núcleo de D15: o passageiro escolheu um número, e o cliente devolve
	// ao servidor os campos carona_id, de e ate que ele mesmo recebeu na
	// busca. Nenhum identificador foi digitado, e nenhum apareceu na tela.
	itens := make([]protocolo.ItemReserva, len(escolhido.Trechos))
	for i, trecho := range escolhido.Trechos {
		itens[i] = protocolo.ItemReserva{CaronaID: trecho.CaronaID, De: trecho.De, Ate: trecho.Ate}
	}

	confirmada, err := conexao.Reservar(itens)
	if err != nil {
		return err
	}
	term.Imprimir("\nReserva %s confirmada. Total: %s\n",
		confirmada.ReservaID, cliente.FormatarCentavos(confirmada.PrecoTotalCentavos))
	return nil
}

// listarReservas mostra as reservas do passageiro (seção 5.10).
func listarReservas(term *cliente.Terminal, conexao *cliente.Conexao) error {
	incluirCanceladas, err := term.LerSimNao("Incluir as reservas canceladas?", false)
	if err != nil {
		return err
	}

	resposta, err := conexao.ListarMinhasReservas(incluirCanceladas)
	if err != nil {
		return err
	}
	if len(resposta.Reservas) == 0 {
		term.Imprimir("\nVocê não tem reservas.\n")
		return nil
	}

	exibirReservas(term, resposta.Reservas)
	return nil
}

// cancelarReserva cancela uma reserva escolhida por número (seção 5.11).
//
// A listagem pede só as ativas: oferecer uma reserva já cancelada levaria a
// uma ida à rede que só poderia voltar como RESERVA_JA_CANCELADA.
func cancelarReserva(term *cliente.Terminal, conexao *cliente.Conexao) error {
	resposta, err := conexao.ListarMinhasReservas(false)
	if err != nil {
		return err
	}
	if len(resposta.Reservas) == 0 {
		term.Imprimir("\nVocê não tem reserva ativa para cancelar.\n")
		return nil
	}

	term.Imprimir("\n%s:\n\n", cliente.Plural(len(resposta.Reservas), "reserva ativa", "reservas ativas"))
	exibirReservas(term, resposta.Reservas)
	term.Imprimir("\n")

	escolha, err := term.LerEscolhaOuVoltar("Cancelar qual? (0 para voltar): ", len(resposta.Reservas))
	if err != nil {
		return err
	}
	if escolha == cliente.VoltarAoMenu {
		return nil
	}
	alvo := resposta.Reservas[escolha]

	confirmado, err := term.LerSimNao(
		fmt.Sprintf("Cancelar a viagem de %s?", cliente.FormatarInstante(alvo.Partida)), false)
	if err != nil {
		return err
	}
	if !confirmado {
		return nil
	}

	// O reserva_id vem da listagem que o servidor acabou de mandar, e não do
	// teclado (D15).
	cancelada, err := conexao.CancelarReserva(alvo.ReservaID)
	if err != nil {
		return err
	}
	term.Imprimir("\nReserva %s cancelada. Os assentos voltaram para os trechos.\n", cancelada.ReservaID)
	return nil
}

// acrescentarItinerario escreve um resultado da busca na tabela.
//
// Nem carona_id, nem "de", nem "ate" aparecem: o passageiro decide por preço,
// horário e motorista, que é o que ele tem como julgar.
func acrescentarItinerario(tabela *cliente.Tabela, numero int, itinerario protocolo.Itinerario) {
	tabela.LinhaSolta("[%d] %s — %s → %s (%s)",
		numero,
		cliente.FormatarCentavos(itinerario.PrecoTotalCentavos),
		cliente.FormatarInstante(itinerario.Partida),
		cliente.FormatarHoraRelativa(itinerario.Chegada, itinerario.Partida),
		cliente.Plural(itinerario.Baldeacoes, "baldeação", "baldeações"))

	for _, trecho := range itinerario.Trechos {
		acrescentarPerna(tabela, trecho.Origem, trecho.Destino,
			trecho.Partida, trecho.Chegada, itinerario.Partida,
			trecho.Motorista, trecho.PrecoCentavos)
	}
}

// exibirReservas imprime a listagem de reservas do passageiro.
//
// Uma única tabela para a listagem inteira, e não uma por reserva: as colunas
// só ficam alinhadas entre si se a largura for medida sobre todas as linhas.
func exibirReservas(term *cliente.Terminal, reservas []protocolo.ReservaResumo) {
	var tabela cliente.Tabela
	for i, reserva := range reservas {
		if i > 0 {
			tabela.LinhaSolta("")
		}
		acrescentarReserva(&tabela, i+1, reserva)
	}
	tabela.Escrever(term)
}

// acrescentarReserva escreve uma reserva do passageiro na tabela.
func acrescentarReserva(tabela *cliente.Tabela, numero int, reserva protocolo.ReservaResumo) {
	situacao := "ativa"
	if !reserva.Ativa {
		situacao = "cancelada"
	}

	// Uma reserva sempre tem ao menos um trecho; a contagem de baldeações é
	// protegida contra uma lista vazia para que "-1 baldeações" nunca chegue
	// à tela.
	baldeacoes := len(reserva.Trechos) - 1
	if baldeacoes < 0 {
		baldeacoes = 0
	}

	tabela.LinhaSolta("[%d] %s — %s", numero, reserva.ReservaID, situacao)
	tabela.LinhaSolta("    %s → %s  •  %s  •  %s",
		cliente.FormatarInstante(reserva.Partida),
		cliente.FormatarHoraRelativa(reserva.Chegada, reserva.Partida),
		cliente.FormatarCentavos(reserva.PrecoTotalCentavos),
		cliente.Plural(baldeacoes, "baldeação", "baldeações"))

	for _, trecho := range reserva.Trechos {
		acrescentarPerna(tabela, trecho.Origem, trecho.Destino,
			trecho.Partida, trecho.Chegada, reserva.Partida,
			trecho.Motorista, trecho.PrecoCentavos)
	}
}

// acrescentarPerna escreve uma perna de viagem, no mesmo formato para
// itinerário e para reserva.
//
// referencia é a partida do itinerário inteiro: horas do mesmo dia saem só
// como "06:00", e uma perna que caiu no dia seguinte sai com a data junto.
func acrescentarPerna(tabela *cliente.Tabela, origem, destino string, partida, chegada, referencia time.Time, motorista string, precoCentavos int) {
	tabela.Linha(
		"    "+origem+" → "+destino,
		cliente.FormatarIntervalo(partida, chegada, referencia),
		motorista,
		cliente.FormatarCentavos(precoCentavos))
}
