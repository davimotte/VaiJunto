// Comando servidor sobe o servidor central do VAIJUNTO.
package main

import (
	"flag"
	"log"
	"os"

	_ "time/tzdata" // fusos embutidos no binário: a imagem Alpine não traz tzdata, e sem eles dominio.FusoDasCidades cairia no deslocamento fixo.

	"vaijunto/internal/dominio"
	"vaijunto/internal/servidor"
)

func main() {
	empresa := flag.String("empresa", "", "identificador desta empresa, obrigatório (ex.: vaijunto); prefixa os IDs de carona e de reserva")
	endereco := flag.String("endereco", "0.0.0.0:9000", "endereço e porta TCP em que o servidor escuta")
	caminhoUsuarios := flag.String("usuarios", "dados/vaijunto/usuarios.json", "arquivo de carga dos usuários")
	caminhoCaronas := flag.String("caronas", "dados/vaijunto/caronas.json", "arquivo de carga das caronas")
	logOperacoes := flag.Bool("log-operacoes", true, "registra no terminal cada conexão e cada operação atendida; desligue ao medir carga")
	flag.Parse()

	// Sem default de propósito: uma empresa que sobe com o nome de outra
	// geraria IDs com o prefixo errado, e o erro só apareceria entre
	// máquinas (D27).
	if err := dominio.ValidarEmpresa(*empresa); err != nil {
		log.Fatalf("servidor: --empresa é obrigatória e precisa estar no formato certo: %v", err)
	}

	// Carga única no boot (D02, D12): daqui em diante o estado vive só em
	// memória. Falha de leitura aborta a subida em vez de deixar o servidor no
	// ar sem usuário nenhum, situação em que todo LOGIN falharia sem que a
	// causa aparecesse.
	estado, err := dominio.CarregarEstado(*empresa, *caminhoUsuarios, *caminhoCaronas)
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
	log.Printf("servidor: empresa %s escutando em %s (registro de operações: %t)", *empresa, s.Endereco(), *logOperacoes)

	if err := s.Aceitar(); err != nil {
		log.Fatalf("servidor: loop de aceitação encerrado: %v", err)
	}
}
