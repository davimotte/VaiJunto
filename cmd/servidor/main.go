// Comando servidor sobe o servidor de uma empresa do consórcio VAIJUNTO: a
// porta TCP dos clientes (P1) e a porta HTTP dos pares (D20, D28).
package main

import (
	"flag"
	"log"
	"os"
	"time"

	_ "time/tzdata" // fusos embutidos no binário: a imagem Alpine não traz tzdata, e sem eles dominio.FusoDasCidades cairia no deslocamento fixo.

	"vaijunto/internal/dominio"
	"vaijunto/internal/federacao"
	"vaijunto/internal/persistencia"
	"vaijunto/internal/servidor"
	"vaijunto/internal/transacao"
)

func main() {
	empresa := flag.String("empresa", "", "identificador desta empresa, obrigatório (ex.: vaijunto); prefixa os IDs de carona e de reserva")
	endereco := flag.String("endereco", "0.0.0.0:9000", "endereço e porta TCP em que o servidor atende os clientes")
	enderecoPares := flag.String("endereco-pares", "0.0.0.0:8080", "endereço e porta HTTP em que o servidor atende os pares do consórcio")
	dirDados := flag.String("dados", "dados/vaijunto", "diretório com usuarios.json e caronas.json desta empresa")
	listaPares := flag.String("pares", "", "demais empresas do consórcio, no formato empresa=url,empresa=url (D20)")
	caminhoLog := flag.String("log", "", "arquivo do log write-ahead (D22), ex.: /estado/vaijunto.wal; vazio = estado só em memória, perdido a cada reinício")
	logOperacoes := flag.Bool("log-operacoes", true, "registra no terminal cada conexão e cada operação atendida, dos clientes e dos pares; desligue ao medir carga")
	prazoChamada := flag.Duration("prazo-chamada", 2*time.Second, "prazo de cada chamada HTTP a um par (D41); quem o consome é o cliente da federação, a partir da busca federada")
	flag.Parse()

	// Sem default de propósito: uma empresa que sobe com o nome de outra
	// geraria IDs com o prefixo errado, e o erro só apareceria entre
	// máquinas (D27).
	if err := dominio.ValidarEmpresa(*empresa); err != nil {
		log.Fatalf("servidor: --empresa é obrigatória e precisa estar no formato certo: %v", err)
	}

	// A lista de pares é estática (D20): é com ela que o cliente da federação
	// encontra cada empresa e é ela que diz quem pode nos chamar. Validar já
	// aqui faz o erro de digitação aparecer na subida, e não numa busca
	// federada, em outra máquina.
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

	// D24: o relógio de Lamport é um por empresa, e é injetado no estado antes
	// de qualquer gravação, para que todo registro saia com o carimbo do
	// momento em que foi gravado (seção 4.8). O estado recebe a função de
	// leitura, e não o relógio: internal/dominio não importa pacote interno
	// nenhum (regra 8).
	relogio := transacao.NovoRelogio()
	estado.UsarRelogio(relogio.Agora)

	if *caminhoLog == "" {
		log.Printf("servidor: ATENÇÃO: sem --log, o estado é volátil e as caronas publicadas se perdem ao reiniciar")
	} else {
		arquivo, registros, err := persistencia.Abrir(*caminhoLog)
		if err != nil {
			log.Fatalf("servidor: log %s: %v", *caminhoLog, err)
		}
		// D37: aplica sem revalidar. O log de arquivo só é instalado depois do
		// replay, para que a reaplicação nunca grave de volta o que está lendo.
		maiorLamport, err := estado.Reaplicar(registros)
		if err != nil {
			log.Fatalf("servidor: replay de %s: %v", *caminhoLog, err)
		}
		// Seção 4.8: o relógio volta no maior valor do log, para nunca andar
		// para trás. Sem isto, um servidor religado reemitiria carimbos já
		// usados, e duas transações distintas apareceriam como simultâneas na
		// ordem da seção 8.1.
		relogio.Restaurar(maiorLamport)
		estado.UsarLog(arquivo)
		log.Printf("servidor: log %s aberto, %d registros reaplicados, relógio de Lamport em %d", *caminhoLog, len(registros), relogio.Agora())
	}

	// Config única para as duas pontas da pilha REST: assim o servidor HTTP e
	// o cliente da federação de uma empresa compartilham obrigatoriamente o
	// mesmo relógio. Dois relógios no mesmo processo quebrariam a ordem total
	// da seção 8.1.
	//
	// O cliente (federacao.NovoCliente) entra com a busca federada; aqui a
	// Config já carrega o prazo dele, como a flag --pares foi validada uma
	// fase antes de ter consumidor.
	cfgREST := federacao.Config{
		Empresa:      *empresa,
		Pares:        pares,
		Relogio:      relogio,
		PrazoChamada: *prazoChamada,
	}
	if *logOperacoes {
		cfgREST.Registro = federacao.NovoRegistro(os.Stderr, *empresa)
	}

	// As duas portas são abertas antes de qualquer uma ser atendida: porta
	// ocupada vira falha de boot com mensagem clara, em vez de aparecer depois
	// numa goroutine já solta, com o servidor meio no ar.
	rest, err := federacao.Escutar(*enderecoPares, cfgREST)
	if err != nil {
		log.Fatalf("servidor: não foi possível escutar em %s (porta dos pares): %v", *enderecoPares, err)
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
	log.Printf("servidor: empresa %s atendendo clientes em %s e pares em %s (pares: %d, registro de operações: %t)",
		*empresa, s.Endereco(), rest.Endereco(), len(pares), *logOperacoes)

	// A porta dos pares roda em sua própria goroutine, e a dos clientes
	// continua na principal. Se a porta dos pares cai, o processo cai: um
	// servidor que atende clientes mas não responde aos pares seria visto como
	// "no ar" por quem usa o TCP e como indisponível por quem usa o REST
	// (D30), e cada reserva com trecho dele abortaria sem explicação.
	go func() {
		if err := rest.Servir(); err != nil {
			log.Fatalf("servidor: porta dos pares encerrada: %v", err)
		}
	}()

	if err := s.Aceitar(); err != nil {
		log.Fatalf("servidor: loop de aceitação encerrado: %v", err)
	}
}
