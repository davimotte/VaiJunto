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
	prazoChamada := flag.Duration("prazo-chamada", 2*time.Second, "prazo de cada chamada HTTP a um par (D41)")
	prazoBusca := flag.Duration("prazo-busca", 2*time.Second, "prazo único da rodada de pares de uma busca federada (D41); vale para o leque inteiro, não por par")
	prazoVotacao := flag.Duration("prazo-votacao", 5*time.Second, "prazo da votação do coordenador, incluindo as esperas do wait-die (D41); vale para o leque inteiro de PREPARAR")
	prazoAguarde := flag.Duration("prazo-aguarde", 200*time.Millisecond, "intervalo entre repetições do PREPARAR após um voto AGUARDE (D41)")
	prazoFase2 := flag.Duration("prazo-fase2", 2*time.Second, "intervalo entre repetições de CONFIRMAR e de ABORTAR na fase 2 do 2PC (D41)")
	prazoDuvida := flag.Duration("prazo-duvida", 10*time.Second, "tempo em PREPARADO antes de o participante consultar o coordenador (D41, seção 9.4)")
	prazoConsulta := flag.Duration("prazo-consulta", 3*time.Second, "intervalo entre as consultas de um participante em dúvida (D41, seção 9.4)")
	auditoria := flag.Bool("auditoria", false, "expõe GET /auditoria com o estado completo desta empresa (seção 11.2); só para os testes, desligada por padrão")
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

	// Config única para as três pontas da pilha REST: assim o servidor HTTP, o
	// cliente da federação e a busca de uma empresa compartilham
	// obrigatoriamente o mesmo relógio e a mesma lista de pares. Dois relógios
	// no mesmo processo quebrariam a ordem total da seção 8.1.
	// O participante é montado antes da Config porque o coordenador também
	// precisa dele: a mesma instância atende os pares pela borda REST e a
	// chamada local do coordenador quando esta empresa é dona de algum trecho
	// (D21; seção 7.1, passo 5).
	participante := transacao.NovoParticipante(estado)

	cfgREST := federacao.Config{
		Empresa: *empresa,
		Pares:   pares,
		Relogio: relogio,
		Estado:  estado,
		// D21: o participante do 2PC é montado sobre o mesmo Estado, e é por
		// ele que passam tanto as chamadas dos pares quanto, a partir da fase
		// 5, o coordenador local quando esta empresa também é dona de um
		// trecho (seção 7.1, passo 5).
		Participante: participante,
		PrazoChamada: *prazoChamada,
		PrazoBusca:   *prazoBusca,
		Auditoria:    *auditoria,
	}
	if *logOperacoes {
		cfgREST.Registro = federacao.NovoRegistro(os.Stderr, *empresa)
	}

	// O cliente dos pares é construído uma vez e compartilhado pelos seus dois
	// chamadores: a busca federada (D26) e o coordenador do 2PC (D21). Dois
	// clientes funcionariam, mas teriam dois http.Client e, com eles, dois
	// pools de conexão para os mesmos pares.
	cliente := federacao.NovoCliente(cfgREST)
	busca := federacao.NovaBusca(cfgREST, cliente)

	// O coordenador é o lado que decide do 2PC (seção 7.1). Recebe a rede para
	// falar com os pares e o votante local para a própria empresa, que é
	// chamada direto, sem HTTP contra si mesma.
	coordenador := transacao.NovoCoordenador(transacao.ConfigCoordenador{
		Empresa:      *empresa,
		Estado:       estado,
		Relogio:      relogio,
		Rede:         cliente,
		Participante: federacao.NovoVotanteLocal(participante),
		Consorcio:    empresasDoConsorcio(*empresa, pares),
		Registro:     cfgREST.Registro,
		PrazoVotacao: *prazoVotacao,
		PrazoAguarde: *prazoAguarde,
		PrazoFase2:   *prazoFase2,
	})

	// O terminador é o lado em dúvida do 2PC (D38, seção 9.4): o que devolve um
	// assento cujo coordenador nunca mandou a decisão. Recebe a mesma rede do
	// coordenador, porque a consulta é uma chamada a par como as outras.
	terminador := transacao.NovoTerminador(transacao.ConfigTerminador{
		Empresa:       *empresa,
		Estado:        estado,
		Rede:          cliente,
		Registro:      cfgREST.Registro,
		PrazoDuvida:   *prazoDuvida,
		PrazoConsulta: *prazoConsulta,
	})

	// As duas portas são abertas antes de qualquer uma ser atendida: porta
	// ocupada vira falha de boot com mensagem clara, em vez de aparecer depois
	// numa goroutine já solta, com o servidor meio no ar.
	rest, err := federacao.Escutar(*enderecoPares, cfgREST)
	if err != nil {
		log.Fatalf("servidor: não foi possível escutar em %s (porta dos pares): %v", *enderecoPares, err)
	}

	s, err := servidor.Escutar(*endereco, estado, busca, coordenador)
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

	// Passo 5 da seção 9.2: abrir as portas **e** disparar as tarefas de
	// recuperação da seção 9.3. Nesta ordem, e não antes das portas: as duas
	// tarefas falam com os pares, e um par que nos responda precisa nos
	// encontrar no ar — se perguntarmos antes de atender, o outro lado
	// perguntaria de volta e não acharia ninguém.
	//
	// Todo boot passa por aqui, porque todo boot é uma recuperação (seção 9.1):
	// num servidor que parou limpo as duas listas vêm vazias e nada acontece.
	retomadas := coordenador.Recuperar()
	emDuvida := terminador.Recuperar()
	log.Printf("servidor: recuperação: %d reserva(s) com a fase 2 a retomar, %d participação(ões) em PREPARADO com o prazo de dúvida armado",
		retomadas, emDuvida)

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

// empresasDoConsorcio junta a própria empresa às dos pares (D20).
//
// O coordenador usa a lista para recusar, no passo 1, uma carona cujo prefixo
// não seja de ninguém do consórcio — antes de criar transação e antes de falar
// com qualquer um.
func empresasDoConsorcio(propria string, pares map[string]string) []string {
	lista := make([]string, 0, len(pares)+1)
	lista = append(lista, propria)
	for empresa := range pares {
		lista = append(lista, empresa)
	}
	return lista
}
