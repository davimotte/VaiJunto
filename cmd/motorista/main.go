// Comando motorista é o cliente de terminal do perfil MOTORISTA.
//
// Vale aqui a mesma estrutura de cmd/passageiro e a mesma justificativa
// (D15): um menu numérico sobre uma única conexão TCP, aberta antes do LOGIN
// e fechada depois do LOGOUT, em vez de um CLI de subcomandos que reabriria a
// conexão e reautenticaria a cada operação.
package main

import (
	"errors"
	"fmt"
	"os"

	_ "time/tzdata" // fusos embutidos no binário: a imagem Alpine do cliente não traz tzdata, e sem eles dominio.FusoDasCidades cairia no deslocamento fixo.

	"vaijunto/internal/cliente"
	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// assentosMaximos limita o que o menu aceita digitar. Não é regra de
// negócio — o servidor só exige assentos >= 1 (seção 5.4) —, é apenas a faixa
// que LerInteiro precisa para recusar um dedo escorregado no teclado.
const assentosMaximos = 99

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

	term.Imprimir("=== VAIJUNTO — Motorista ===\nServidor: %s\n\n", endereco)

	login, err := cliente.Entrar(term, conexao, protocolo.PerfilMotorista)
	if err != nil {
		encerrar(term, err)
		return
	}
	term.Imprimir("\nConectado a %s como %s.\n", endereco, login.Nome)

	if err := menu(term, conexao); err != nil {
		encerrar(term, err)
		return
	}

	if err := conexao.Logout(); err != nil {
		encerrar(term, err)
		return
	}
	term.Imprimir("Até logo.\n")
}

// encerrar termina o processo diante de um erro que o menu não conseguiu
// tratar. Fim de entrada é saída normal; o resto é canal quebrado.
func encerrar(term *cliente.Terminal, err error) {
	if errors.Is(err, cliente.ErrEntradaEncerrada) {
		term.Imprimir("Até logo.\n")
		return
	}
	fmt.Fprintf(os.Stderr, "\nvaijunto: sessão encerrada: %v\n", err)
	os.Exit(1)
}

// menu é o laço principal. Devolve nil quando o usuário escolhe sair.
func menu(term *cliente.Terminal, conexao *cliente.Conexao) error {
	opcoes := []string{
		"Publicar carona",
		"Minhas caronas",
		"Detalhar carona (passageiros por trecho)",
		"Cancelar carona",
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
			erroOperacao = publicarCarona(term, conexao)
		case 1:
			erroOperacao = listarCaronas(term, conexao)
		case 2:
			erroOperacao = detalharCarona(term, conexao)
		case 3:
			erroOperacao = cancelarCarona(term, conexao)
		case 4:
			return nil
		}

		if err := cliente.TratarErro(term, erroOperacao); err != nil {
			return err
		}
	}
}

// publicarCarona coleta os campos da seção 5.4 e publica.
//
// O motorista informa cada parada com o seu horário, os assentos e o preço de
// cada trecho (D09). O servidor não calcula nada: a resposta devolve a rota
// como ele a guardou, e é ela que o menu mostra no final.
func publicarCarona(term *cliente.Terminal, conexao *cliente.Conexao) error {
	term.Imprimir("\nInforme as paradas na ordem em que o carro passa por elas.\n")
	paradas, err := cliente.ColetarParadas(term, dominio.FusoDasCidades())
	if err != nil {
		return err
	}

	assentos, err := term.LerInteiro("\nAssentos: ", 1, assentosMaximos)
	if err != nil {
		return err
	}

	// Um preço por trecho entre paradas consecutivas, na ordem: len(paradas)-1
	// valores, que é exatamente a contagem que a seção 5.4 exige. Os rótulos
	// saem das paradas que o próprio motorista acabou de informar.
	precos := make([]int, 0, len(paradas)-1)
	term.Imprimir("\nPreço de cada trecho:\n")
	for i := 0; i+1 < len(paradas); i++ {
		preco, err := term.LerCentavos(fmt.Sprintf("  %s → %s: R$ ", paradas[i].Cidade, paradas[i+1].Cidade))
		if err != nil {
			return err
		}
		precos = append(precos, preco)
	}

	publicada, err := conexao.PublicarCarona(protocolo.PublicarCaronaRequisicao{
		Paradas:        paradas,
		Assentos:       assentos,
		PrecosCentavos: precos,
	})
	if err != nil {
		return err
	}

	term.Imprimir("\nCarona %s publicada, com %s.\n",
		publicada.CaronaID, cliente.Plural(assentos, "assento", "assentos"))

	// Rota e Horarios são listas paralelas na resposta da seção 5.4; o laço
	// para na mais curta para que uma resposta inesperada vire linha faltando
	// em vez de pânico no meio da demonstração.
	var tabela cliente.Tabela
	for i, cidade := range publicada.Rota {
		if i >= len(publicada.Horarios) {
			break
		}
		tabela.Linha("    "+cidade, cliente.FormatarInstante(publicada.Horarios[i]))
	}
	tabela.Escrever(term)
	return nil
}

// listarCaronas mostra as caronas do motorista (seção 5.5).
func listarCaronas(term *cliente.Terminal, conexao *cliente.Conexao) error {
	incluirCanceladas, err := term.LerSimNao("Incluir as caronas canceladas?", false)
	if err != nil {
		return err
	}

	resposta, err := conexao.ListarMinhasCaronas(incluirCanceladas)
	if err != nil {
		return err
	}
	if len(resposta.Caronas) == 0 {
		term.Imprimir("\nVocê não tem caronas publicadas.\n")
		return nil
	}

	exibirCaronas(term, resposta.Caronas)
	return nil
}

// detalharCarona mostra os passageiros confirmados por trecho (seção 5.6),
// atendendo ao RF04.
//
// A escolha é por número sobre a listagem que o servidor acabou de mandar: o
// carona_id enviado é o que veio na resposta, e não algo digitado (D15).
// A listagem inclui as canceladas porque é justamente nelas que o motorista
// quer conferir quem foi atingido pelo cancelamento em cascata.
func detalharCarona(term *cliente.Terminal, conexao *cliente.Conexao) error {
	alvo, err := escolherCarona(term, conexao, true, "Detalhar qual? (0 para voltar): ")
	if err != nil || alvo == nil {
		return err
	}

	detalhe, err := conexao.DetalharCarona(alvo.CaronaID)
	if err != nil {
		return err
	}

	term.Imprimir("\nCarona %s — %s\n\n", detalhe.CaronaID, cliente.DescreverRota(alvo.Rota))
	var tabela cliente.Tabela
	for i, trecho := range detalhe.Trechos {
		if i > 0 {
			tabela.LinhaSolta("")
		}
		tabela.Linha(trecho.Origem+" → "+trecho.Destino,
			cliente.Plural(trecho.Livres, "assento livre", "assentos livres"))
		if len(trecho.Passageiros) == 0 {
			tabela.LinhaSolta("    (nenhum passageiro)")
		}
		for _, passageiro := range trecho.Passageiros {
			tabela.LinhaSolta("    %s (%s) — reserva %s",
				passageiro.Nome, passageiro.Usuario, passageiro.ReservaID)
		}
	}
	tabela.Escrever(term)
	return nil
}

// cancelarCarona cancela uma carona escolhida por número (seção 5.7).
//
// A listagem traz só as não canceladas: uma carona já cancelada só poderia
// voltar como CARONA_CANCELADA.
func cancelarCarona(term *cliente.Terminal, conexao *cliente.Conexao) error {
	alvo, err := escolherCarona(term, conexao, false, "Cancelar qual? (0 para voltar): ")
	if err != nil || alvo == nil {
		return err
	}

	// O aviso é literal: o cancelamento derruba em cascata toda reserva que
	// use qualquer trecho desta carona, e ignora o prazo de uma hora do
	// passageiro (D13). É a operação menos reversível do sistema.
	confirmado, err := term.LerSimNao(
		fmt.Sprintf("Cancelar a carona de %s? As reservas dos passageiros também serão canceladas",
			cliente.FormatarInstante(alvo.Horarios[0])), false)
	if err != nil {
		return err
	}
	if !confirmado {
		return nil
	}

	cancelada, err := conexao.CancelarCarona(alvo.CaronaID)
	if err != nil {
		return err
	}
	term.Imprimir("\nCarona %s cancelada. %s.\n", cancelada.CaronaID,
		cliente.Plural(cancelada.ReservasCanceladas,
			"reserva de passageiro cancelada em cascata",
			"reservas de passageiros canceladas em cascata"))
	return nil
}

// escolherCarona lista as caronas do motorista e devolve a escolhida, ou nil
// quando não há nenhuma ou o usuário digita 0 para voltar.
//
// É o passo que mantém o carona_id fora do teclado nas duas operações que
// precisam dele.
func escolherCarona(term *cliente.Terminal, conexao *cliente.Conexao, incluirCanceladas bool, rotulo string) (*protocolo.CaronaResumo, error) {
	resposta, err := conexao.ListarMinhasCaronas(incluirCanceladas)
	if err != nil {
		return nil, err
	}
	if len(resposta.Caronas) == 0 {
		term.Imprimir("\nVocê não tem caronas para essa operação.\n")
		return nil, nil
	}

	exibirCaronas(term, resposta.Caronas)
	term.Imprimir("\n")

	escolha, err := term.LerEscolhaOuVoltar(rotulo, len(resposta.Caronas))
	if err != nil {
		return nil, err
	}
	if escolha == cliente.VoltarAoMenu {
		return nil, nil
	}
	return &resposta.Caronas[escolha], nil
}

// exibirCaronas imprime a listagem de caronas do motorista.
//
// Uma única tabela para a listagem inteira, e não uma por carona: as colunas
// só ficam alinhadas entre si se a largura for medida sobre todas as linhas.
func exibirCaronas(term *cliente.Terminal, caronas []protocolo.CaronaResumo) {
	term.Imprimir("\n%s:\n\n", cliente.Plural(len(caronas), "carona", "caronas"))
	var tabela cliente.Tabela
	for i, carona := range caronas {
		// A linha em branco separa um bloco do seguinte, e por isso vem antes
		// de cada bloco menos o primeiro: depois do último ela só empurraria
		// o menu para baixo.
		if i > 0 {
			tabela.LinhaSolta("")
		}
		acrescentarCarona(&tabela, i+1, carona)
	}
	tabela.Escrever(term)
}

// acrescentarCarona escreve uma carona com seus trechos, preços e assentos
// livres.
func acrescentarCarona(tabela *cliente.Tabela, numero int, carona protocolo.CaronaResumo) {
	// Uma carona sempre tem ao menos duas cidades e dois horários (D09), mas
	// indexar direto uma lista que veio da rede transformaria uma resposta
	// inesperada em pânico do cliente.
	if len(carona.Rota) == 0 || len(carona.Horarios) == 0 {
		tabela.LinhaSolta("[%d] %s — resposta incompleta do servidor", numero, carona.CaronaID)
		return
	}

	situacao := ""
	if carona.Cancelada {
		situacao = " — CANCELADA"
	}

	partida := carona.Horarios[0]
	chegada := carona.Horarios[len(carona.Horarios)-1]

	tabela.LinhaSolta("[%d] %s — %s%s", numero, carona.CaronaID, cliente.DescreverRota(carona.Rota), situacao)
	tabela.LinhaSolta("    %s → %s  •  %s",
		cliente.FormatarInstante(partida),
		cliente.FormatarHoraRelativa(chegada, partida),
		cliente.Plural(carona.Assentos, "assento", "assentos"))

	for _, trecho := range carona.Trechos {
		tabela.Linha(
			"    "+trecho.Origem+" → "+trecho.Destino,
			// Os horários do trecho são os da rota nas suas duas pontas: o
			// índice do trecho é o índice da cidade de onde ele parte.
			intervaloDoTrecho(carona, trecho.Indice),
			cliente.FormatarCentavos(trecho.PrecoCentavos),
			// A concordância segue o total, não o disponível: "1 de 1 assento
			// livre" e "1 de 3 assentos livres".
			fmt.Sprintf("%d de %s", trecho.Livres, cliente.Plural(carona.Assentos, "assento livre", "assentos livres")))
	}
}

// intervaloDoTrecho monta o par partida/chegada do trecho de índice indice.
//
// A checagem de faixa não é defensiva à toa: Rota, Horarios e Trechos chegam
// do servidor como listas independentes, e imprimir uma listagem não pode
// derrubar o cliente se elas discordarem.
func intervaloDoTrecho(carona protocolo.CaronaResumo, indice int) string {
	if indice < 0 || indice+1 >= len(carona.Horarios) {
		return ""
	}
	return cliente.FormatarIntervalo(carona.Horarios[indice], carona.Horarios[indice+1], carona.Horarios[0])
}
