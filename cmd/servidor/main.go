// Comando servidor sobe o servidor central do VAIJUNTO.
package main

import (
	"flag"
	"log"
	"os"

	_ "time/tzdata" // fusos embutidos no binário: a imagem Alpine não traz tzdata, e sem eles dominio.FusoDasCidades cairia no deslocamento fixo.

	"vaijunto/internal/dominio"
	"vaijunto/internal/persistencia"
	"vaijunto/internal/servidor"
)

func main() {
	empresa := flag.String("empresa", "", "identificador desta empresa, obrigatório (ex.: vaijunto); prefixa os IDs de carona e de reserva")
	endereco := flag.String("endereco", "0.0.0.0:9000", "endereço e porta TCP em que o servidor escuta")
	dirDados := flag.String("dados", "dados/vaijunto", "diretório com usuarios.json e caronas.json desta empresa")
	listaPares := flag.String("pares", "", "demais empresas do consórcio, no formato empresa=url,empresa=url (D20); só validada nesta fase")
	caminhoLog := flag.String("log", "", "arquivo do log write-ahead (D22), ex.: /estado/vaijunto.wal; vazio = estado só em memória, perdido a cada reinício")
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

	// Boot (D37, seção 9.2): primeiro a semente, depois o log por cima. A
	// semente nunca muda depois do primeiro boot; o que mudou desde então está
	// no log. Falha em qualquer passo aborta a subida em vez de deixar o
	// servidor no ar com estado duvidoso.
	//
	// Cada empresa lê o seu próprio diretório: não há dado compartilhado entre
	// empresas (RD07, RD11).
	estado, err := servidor.CarregarSemente(*empresa, *dirDados)
	if err != nil {
		log.Fatalf("servidor: carga inicial: %v", err)
	}

	if *caminhoLog == "" {
		log.Printf("servidor: ATENÇÃO: sem --log, o estado é volátil e as caronas publicadas se perdem ao reiniciar")
	} else {
		arquivo, registros, err := persistencia.Abrir(*caminhoLog)
		if err != nil {
			log.Fatalf("servidor: log %s: %v", *caminhoLog, err)
		}
		// D37: aplica sem revalidar. O log de arquivo só é instalado depois do
		// replay, para que a reaplicação nunca grave de volta o que está lendo.
		if err := estado.Reaplicar(registros); err != nil {
			log.Fatalf("servidor: replay de %s: %v", *caminhoLog, err)
		}
		estado.UsarLog(arquivo)
		log.Printf("servidor: log %s aberto, %d registros reaplicados", *caminhoLog, len(registros))
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
