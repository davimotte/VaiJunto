// Comando servidor sobe o servidor central do VAIJUNTO.
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	_ "time/tzdata" // fusos embutidos no binário: a imagem Alpine não traz tzdata, e sem eles dominio.FusoDasCidades cairia no deslocamento fixo.

	"vaijunto/internal/dominio"
	"vaijunto/internal/servidor"
)

func main() {
	empresa := flag.String("empresa", "", "identificador desta empresa, obrigatório (ex.: vaijunto); prefixa os IDs de carona e de reserva")
	endereco := flag.String("endereco", "0.0.0.0:9000", "endereço e porta TCP em que o servidor escuta")
	dirDados := flag.String("dados", "dados/vaijunto", "diretório com usuarios.json e caronas.json desta empresa")
	listaPares := flag.String("pares", "", "demais empresas do consórcio, no formato empresa=url,empresa=url (D20); só validada nesta fase")
	logOperacoes := flag.Bool("log-operacoes", true, "registra no terminal cada conexão e cada operação atendida; desligue ao medir carga")
	flag.Parse()

	// Sem default de propósito: uma empresa que sobe com o nome de outra
	// geraria IDs com o prefixo errado, e o erro só apareceria entre
	// máquinas (D27).
	if err := dominio.ValidarEmpresa(*empresa); err != nil {
		log.Fatalf("servidor: --empresa é obrigatória e precisa estar no formato certo: %v", err)
	}

	// A lista de pares é estática (D20) e só é validada por enquanto: o
	// cliente HTTP que a consome entra na fase 3A. Validar já aqui faz o erro
	// de digitação aparecer na subida, e não numa busca federada.
	pares, err := servidor.ParsearPares(*empresa, *listaPares)
	if err != nil {
		log.Fatalf("servidor: --pares: %v", err)
	}

	// Carga única no boot (D02, D12): daqui em diante o estado vive só em
	// memória. Falha de leitura aborta a subida em vez de deixar o servidor no
	// ar sem usuário nenhum, situação em que todo LOGIN falharia sem que a
	// causa aparecesse.
	//
	// Cada empresa lê o seu próprio diretório: não há dado compartilhado entre
	// empresas (RD07, RD11).
	estado, err := dominio.CarregarEstado(*empresa,
		filepath.Join(*dirDados, "usuarios.json"), filepath.Join(*dirDados, "caronas.json"))
	if err != nil {
		log.Fatalf("servidor: carga inicial: %v", err)
	}

	s, err := servidor.Escutar(*endereco, estado)
	if err != nil {
		log.Fatalf("servidor: não foi possível escutar em %s: %v", *endereco, err)
	}
	// Ligado por padrão, para que a demonstração mostre o servidor
	// trabalhando. Desligado nas medições da seção 8.3 (D19): cada linha é
	// uma escrita síncrona no terminal, serializada pelo mutex do Logger, e
	// entraria na latência medida como um segundo ponto de disputa.
	//
	// Vai para o stderr, a mesma saída do pacote log, para que docker logs
	// mostre as duas coisas juntas.
	if *logOperacoes {
		s.RegistrarOperacoes(os.Stderr)
	}
	log.Printf("servidor: empresa %s escutando em %s (pares: %d, registro de operações: %t)", *empresa, s.Endereco(), len(pares), *logOperacoes)

	if err := s.Aceitar(); err != nil {
		log.Fatalf("servidor: loop de aceitação encerrado: %v", err)
	}
}
