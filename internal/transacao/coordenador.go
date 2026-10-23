package transacao

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"vaijunto/internal/dominio"
)

// Coordenador é o lado que decide do two-phase commit (D21): recebe o RESERVAR
// de um passageiro desta empresa e conduz a transação até confirmar ou abortar,
// implementando a seção 7.1 do PROJETO_P2.md.
//
// O coordenador **não é fixo**. Cada empresa coordena as reservas dos seus
// próprios passageiros, então o papel se distribui naturalmente pelo consórcio
// e não existe componente central (regra 1 do enunciado). A queda de uma
// empresa afeta só as transações dos passageiros dela, que de qualquer forma
// não conseguiriam usar o sistema.
//
// É camada 5 (seção 5.2): a única que conhece prazos e retransmissões, e a
// única que espera. Ela nunca espera segurando o lock da camada 7 — chama o
// estado, solta, faz a E/S de rede, e volta ao estado com o resultado (regra 2).
//
// Por que 2PC e não Saga: a Saga reserva trecho a trecho e compensa com
// cancelamentos, nunca bloqueia, mas expõe estados em que o primeiro trecho
// está confirmado e o segundo não — e o enunciado exige "ou todos os trechos,
// ou nenhum". No 2PC nenhum trecho é confirmado antes de todos terem garantido
// o assento. O custo é o bloqueio residual da seção 9.6, assumido e delimitado.
type Coordenador struct {
	empresa      string
	estado       *dominio.Estado
	relogio      *Relogio
	rede         Rede
	participante Votante
	consorcio    map[string]bool
	registro     RegistroDecisao
	prazoVotacao time.Duration
	prazoAguarde time.Duration
	prazoFase2   time.Duration
	tentativas   int

	// parar encerra as tarefas de segundo plano (fase 2 e ABORTAR). Ver Parar.
	base     context.Context
	encerrar context.CancelFunc
}

// RegistroDecisao recebe a linha de registro de cada decisão (D19; seção 5.2):
//
//	2026/11/05 08:15:02.437 [vaijunto] DECISAO tx=vaijunto:res-3f0b11c4 → CONFIRMAR
//
// A interface é declarada aqui, no consumidor, e não importada de
// internal/federacao: é a federação que importa esta camada (seção 5.3), e
// receber o tipo concreto de lá inverteria a dependência. É o mesmo padrão de
// Rede e de servidor.BuscaFederada.
//
// Um valor nil não escreve nada, e é assim que o registro fica desligado.
type RegistroDecisao interface {
	Decisao(txID, decisao string)
}

// ConfigCoordenador são as peças que o boot liga ao coordenador. É struct, e
// não uma lista de parâmetros, porque são onze e a maioria tem valor padrão.
type ConfigCoordenador struct {
	Empresa      string
	Estado       *dominio.Estado
	Relogio      *Relogio
	Rede         Rede    // pares; nos testes, a rede em memória
	Participante Votante // chamada local, sem rede (D21)
	// Consorcio são as empresas que existem, isto é, os pares mais a própria
	// (D20). Serve ao passo 1: uma carona com prefixo de empresa desconhecida é
	// recusada antes de qualquer coisa.
	//
	// É coisa diferente dos Participantes de um PedidoPreparacao, que são as
	// empresas **desta** transação, calculadas no passo 4 a partir dos prefixos
	// das caronas pedidas.
	Consorcio    []string
	Registro     RegistroDecisao
	PrazoVotacao time.Duration // D41: 5 s, incluindo as esperas do wait-die
	PrazoAguarde time.Duration // D41: 200 ms entre repetições após AGUARDE
	PrazoFase2   time.Duration // D41: 2 s entre repetições de CONFIRMAR e ABORTAR
	Tentativas   int           // D36: 3
}

// Prazos e contagem padrão da D41 e da D36, usados quando a Config os deixa em
// zero. Ficam aqui, e não só como valor de flag, para que um Coordenador montado
// num teste sem configurar nada já se comporte como o de produção.
const (
	prazoVotacaoPadrao = 5 * time.Second
	prazoAguardePadrao = 200 * time.Millisecond
	prazoFase2Padrao   = 2 * time.Second
	tentativasPadrao   = 3
)

func NovoCoordenador(cfg ConfigCoordenador) *Coordenador {
	c := &Coordenador{
		empresa:      cfg.Empresa,
		estado:       cfg.Estado,
		relogio:      cfg.Relogio,
		rede:         cfg.Rede,
		participante: cfg.Participante,
		consorcio:    make(map[string]bool, len(cfg.Consorcio)+1),
		registro:     cfg.Registro,
		prazoVotacao: cfg.PrazoVotacao,
		prazoAguarde: cfg.PrazoAguarde,
		prazoFase2:   cfg.PrazoFase2,
		tentativas:   cfg.Tentativas,
	}
	for _, empresa := range cfg.Consorcio {
		c.consorcio[empresa] = true
	}
	// A própria empresa é sempre do consórcio, mesmo que a lista de pares venha
	// vazia — é o caso do servidor sozinho, dos testes e da fase 1.
	c.consorcio[cfg.Empresa] = true

	if c.prazoVotacao <= 0 {
		c.prazoVotacao = prazoVotacaoPadrao
	}
	if c.prazoAguarde <= 0 {
		c.prazoAguarde = prazoAguardePadrao
	}
	if c.prazoFase2 <= 0 {
		c.prazoFase2 = prazoFase2Padrao
	}
	if c.tentativas <= 0 {
		c.tentativas = tentativasPadrao
	}
	c.base, c.encerrar = context.WithCancel(context.Background())
	return c
}

// Parar encerra as tarefas de segundo plano: a fase 2 e os ABORTAR que repetem
// "sem limite" (D41).
//
// Existe por causa dos testes. Em produção o servidor não tem rotina de
// desligamento — todo boot é uma recuperação (seção 9.1) —, mas num
// `go test -race -count=20` uma goroutine que repete a cada 2 s continuaria
// batendo em servidores httptest já fechados depois do fim do teste, vazando
// goroutine e acusando corrida onde não há. Os testes chamam isto no
// t.Cleanup, antes de fechar os servidores.
func (c *Coordenador) Parar() { c.encerrar() }

// Reservar conduz a reserva atômica de um itinerário, implementando os nove
// passos da seção 7.1.
//
// Devolve a reserva criada e o itinerário resolvido, ou o erro de domínio que a
// borda traduz em código do PROTOCOL.md (seção 5.5).
func (c *Coordenador) Reservar(ctx context.Context, passageiro dominio.Passageiro, passageiroID string, itens []dominio.ItemReserva) (dominio.Reserva, dominio.Itinerario, error) {
	// Passo 1 — formato. Nada mudou no estado ainda, e nada muda se falhar.
	if err := dominio.ValidarItens(itens); err != nil {
		return dominio.Reserva{}, dominio.Itinerario{}, err
	}
	porEmpresa, err := c.agrupar(itens)
	if err != nil {
		return dominio.Reserva{}, dominio.Itinerario{}, err
	}

	// Passo 2 — exclusividade (D35). A marca sai em QUALQUER saída daqui para
	// baixo: erro, prazo esgotado, abort ou pânico. É o que o defer garante.
	if err := c.estado.MarcarReservaEmAndamento(passageiroID); err != nil {
		return dominio.Reserva{}, dominio.Itinerario{}, err
	}
	defer c.estado.LiberarReservaEmAndamento(passageiroID)

	// Passo 3 — carimbo (D24). Uma vez só, FORA do laço de tentativas: as três
	// tentativas da D36 compartilham o mesmo carimbo, e é isso que torna a
	// transação relativamente mais velha a cada disputa, impedindo inanição
	// (seção 8.5).
	carimbo := dominio.Carimbo{Lamport: c.relogio.AoCriarTransacao(), Empresa: c.empresa}

	var ultimo recusa
	for tentativa := 1; tentativa <= c.tentativas; tentativa++ {
		// Passo 4 — nova tentativa: txID próprio, mesmo carimbo (D36).
		txID, err := dominio.GerarIDTransacao(c.empresa)
		if err != nil {
			return dominio.Reserva{}, dominio.Itinerario{}, err
		}

		reserva, itinerario, motivo, err := c.tentar(ctx, txID, carimbo, passageiro, passageiroID, itens, porEmpresa)
		if err != nil {
			return dominio.Reserva{}, dominio.Itinerario{}, err
		}
		if motivo == nil {
			return reserva, itinerario, nil
		}
		ultimo = *motivo

		// Passo 6 — se o motivo foi PRECEDENCIA e ainda há tentativas, volta ao
		// passo 4 com o mesmo carimbo.
		//
		// Nesta fase o participante é no-wait e nunca responde PRECEDENCIA nem
		// AGUARDE (seção 14), então este ramo não dispara: a estrutura está
		// pronta e a fase 7 a liga junto com o wait-die. Qualquer outro motivo
		// é definitivo e não se resolve tentando de novo.
		if ultimo.motivo != MotivoPrecedencia {
			break
		}
	}

	return dominio.Reserva{}, dominio.Itinerario{}, ultimo.erro()
}

// tentar executa uma tentativa: votação, validação do coordenador e commit.
//
// Devolve (reserva, itinerário, nil, nil) quando confirma, e
// (zero, zero, &recusa, nil) quando a tentativa não obteve unanimidade — caso
// em que o ABORTAR já foi disparado em segundo plano. O último retorno é só
// para falha que não é recusa, como erro de log ou de geração de ID.
func (c *Coordenador) tentar(ctx context.Context, txID string, carimbo dominio.Carimbo, passageiro dominio.Passageiro, passageiroID string, itens []dominio.ItemReserva, porEmpresa map[string][]dominio.ItemReserva) (dominio.Reserva, dominio.Itinerario, *recusa, error) {
	participantes := make([]string, 0, len(porEmpresa))
	for empresa := range porEmpresa {
		participantes = append(participantes, empresa)
	}
	// Ordem explícita: a lista vai no PREPARAR e é guardada pelos participantes
	// para a terminação cooperativa (D38). Iteração de mapa é aleatória em Go, e
	// uma lista em ordem diferente a cada tentativa tornaria o log e os testes
	// instáveis.
	sort.Strings(participantes)

	// Passo 5 — votação.
	votos, falta := c.votar(ctx, txID, carimbo, passageiro, participantes, porEmpresa)

	// Passo 6 — fim da votação sem unanimidade.
	if falta != nil {
		// O ABORTAR vai em segundo plano e a decisão NÃO é gravada: quem não
		// receber descobre consultando (D23, abort presumido). Não esperamos por
		// ele, porque o passageiro não precisa esperar para receber a recusa.
		c.abortarTodos(txID, participantes)
		c.anotar(txID, "ABORTAR")
		return dominio.Reserva{}, dominio.Itinerario{}, falta, nil
	}

	// Passo 7 — validação do coordenador (D34), sobre as pernas AUTORITATIVAS
	// devolvidas nos votos, e não sobre o resultado da busca, que pode estar
	// desatualizado. Fora do lock: é função pura sobre cópias.
	pernas := make([]dominio.Perna, 0, len(itens))
	for _, empresa := range participantes {
		pernas = append(pernas, votos[empresa]...)
	}
	// Na ordem em que o passageiro pediu, e não na ordem em que os votos
	// chegaram: eles vêm agrupados por empresa, e a ordem das empresas não é a
	// ordem da viagem.
	pernas = ordenarPernas(pernas, itens)

	if err := dominio.ValidarEncadeamento(pernas); err != nil {
		c.abortarTodos(txID, participantes)
		c.anotar(txID, "ABORTAR")
		// Sem nova tentativa: o itinerário não melhora tentando de novo.
		return dominio.Reserva{}, dominio.Itinerario{}, &recusa{definitivo: err}, nil
	}

	// Passo 8 — ponto de commit. A sobreposição (passo 4 do P1) é conferida
	// dentro da mesma seção crítica que grava, pela regra 5.
	reserva, itinerario, err := c.estado.ConfirmarReserva(txID, passageiroID, participantes, itens, pernas, time.Now().In(dominio.FusoDasCidades()))
	if err != nil {
		c.abortarTodos(txID, participantes)
		c.anotar(txID, "ABORTAR")
		if errors.Is(err, dominio.ErrFalhaNoLog) {
			// Falha de disco não é recusa de negócio: sobe como erro.
			return dominio.Reserva{}, dominio.Itinerario{}, nil, err
		}
		return dominio.Reserva{}, dominio.Itinerario{}, &recusa{definitivo: err}, nil
	}
	c.anotar(txID, "CONFIRMAR")

	// Passo 9 — fase 2 em segundo plano. O passageiro recebe OK antes de ela
	// terminar, e isso é seguro: a decisão já está gravada com fsync e será
	// levada a todos os participantes, inclusive depois de uma queda (seção
	// 9.3). Enquanto o CONFIRMAR não chega, o assento está em PREPARADO, que já
	// conta como ocupado.
	go c.confirmarTodos(txID, participantes)

	return reserva, itinerario, nil, nil
}

// votar é o passo 5: PREPARAR em paralelo a cada participante, com um prazo
// único para o leque inteiro (D41).
//
// Devolve as pernas de cada voto SIM e, se não houve unanimidade, a recusa que
// explica por quê.
//
// Nenhum lock é segurado aqui (regra 2) — e isso não é só disciplina: o
// participante local adquire o MESMO mutex do estado, então fazer a votação com
// o lock preso seria deadlock imediato, não apenas lentidão.
func (c *Coordenador) votar(ctx context.Context, txID string, carimbo dominio.Carimbo, passageiro dominio.Passageiro, participantes []string, porEmpresa map[string][]dominio.ItemReserva) (map[string][]dominio.Perna, *recusa) {
	ctx, cancelar := context.WithTimeout(ctx, c.prazoVotacao)
	defer cancelar()

	type resultado struct {
		empresa  string
		resposta RespostaPreparar
		err      error
	}
	// Capacidade igual ao número de participantes: nenhuma goroutine fica presa
	// ao escrever, mesmo que o laço abaixo já tenha desistido pelo prazo.
	respostas := make(chan resultado, len(participantes))

	for _, empresa := range participantes {
		pedido := dominio.PedidoPreparacao{
			TxID:          txID,
			Coordenador:   c.empresa,
			Participantes: participantes,
			Carimbo:       carimbo,
			Passageiro:    passageiro,
			Itens:         porEmpresa[empresa],
		}
		go func(empresa string) {
			r, err := c.prepararComRepeticao(ctx, empresa, pedido)
			respostas <- resultado{empresa: empresa, resposta: r, err: err}
		}(empresa)
	}

	pernas := make(map[string][]dominio.Perna, len(participantes))
	for range participantes {
		select {
		case r := <-respostas:
			switch {
			case r.err != nil:
				// A goroutine só desiste quando o prazo acaba, então um erro
				// aqui significa empresa indisponível (D30): ela não respondeu
				// dentro do prazo de votação.
				return pernas, &recusa{motivo: MotivoTransacaoAbortada, indisponivel: r.empresa}
			case r.resposta.Voto == VotoSim:
				pernas[r.empresa] = r.resposta.Pernas
			default:
				// NÃO, ou AGUARDE que não virou SIM dentro do prazo.
				return pernas, &recusa{
					motivo:   r.resposta.Motivo,
					empresa:  r.empresa,
					caronaID: r.resposta.CaronaID,
					trecho:   r.resposta.Trecho,
				}
			}
		case <-ctx.Done():
			return pernas, &recusa{motivo: MotivoTransacaoAbortada}
		}
	}
	return pernas, nil
}

// prepararComRepeticao envia o PREPARAR a uma empresa e repete enquanto houver
// prazo, conforme o passo 5.
//
// Dois casos pedem repetição, por motivos diferentes:
//
//   - AGUARDE: o participante é mais novo que os portadores do assento e pede
//     para o coordenador voltar depois (wait-die, seção 8.2). Esperar não
//     prende o handler nem o lock do dono: ele responde na hora e nós voltamos
//     em 200 ms (D41). Na fase 5 isto não acontece, porque o participante é
//     no-wait;
//   - falha de comunicação: o PREPARAR é idempotente pelo txID (D28, regra 6),
//     e a falha pode ter sido só da resposta — o participante pode já estar em
//     PREPARADO. Repetir é seguro e é o que recupera uma resposta perdida.
func (c *Coordenador) prepararComRepeticao(ctx context.Context, empresa string, pedido dominio.PedidoPreparacao) (RespostaPreparar, error) {
	for {
		r, err := c.preparar(ctx, empresa, pedido)
		switch {
		case err == nil && r.Voto != VotoAguarde:
			return r, nil
		case err != nil && ehViolacao(err):
			// 409 encerra a repetição: violação de protocolo não se corrige
			// repetindo (seção 1.3 da API).
			return RespostaPreparar{}, err
		}

		select {
		case <-ctx.Done():
			if err != nil {
				return RespostaPreparar{}, err
			}
			// Último AGUARDE vira a resposta: o prazo acabou esperando.
			return r, nil
		case <-time.After(c.prazoAguarde):
		}
	}
}

// preparar manda o PREPARAR a uma empresa, pela rede ou localmente.
//
// Quando a empresa é a própria, o participante é chamado **direto**, pelo mesmo
// código, sem passar pela rede (D21): uma empresa não faz HTTP contra si mesma.
// A chamada adquire o lock do estado do jeito normal, como qualquer outra.
func (c *Coordenador) preparar(ctx context.Context, empresa string, pedido dominio.PedidoPreparacao) (RespostaPreparar, error) {
	if empresa == c.empresa {
		return c.participante.Preparar(pedido)
	}
	return c.rede.Preparar(ctx, empresa, pedido)
}

// confirmarTodos é a fase 2 (passo 9): CONFIRMAR a todos os participantes,
// repetindo a cada PrazoFase2 até cada um confirmar o recebimento, e então
// gravar CONCLUIDA.
//
// A repetição é "sem limite" (D41) porque a decisão já está gravada e **tem**
// de chegar: é o passo 4 do argumento de atomicidade da seção 7.4. O único
// motivo para parar é o servidor terminar (ver Parar).
func (c *Coordenador) confirmarTodos(txID string, participantes []string) {
	falta := make(map[string]bool, len(participantes))
	for _, empresa := range participantes {
		falta[empresa] = true
	}

	for len(falta) > 0 {
		for empresa := range falta {
			if c.base.Err() != nil {
				return
			}
			ctx, cancelar := context.WithTimeout(c.base, c.prazoFase2)
			var err error
			if empresa == c.empresa {
				_, err = c.participante.Confirmar(txID)
			} else {
				_, err = c.rede.Confirmar(ctx, empresa, txID)
			}
			cancelar()
			if err == nil {
				delete(falta, empresa)
			}
		}
		if len(falta) == 0 {
			break
		}
		select {
		case <-c.base.Done():
			return
		case <-time.After(c.prazoFase2):
		}
	}

	// Todos confirmaram: a transação acabou.
	_ = c.estado.ConcluirReserva(txID)
	c.anotar(txID, "CONCLUIDA")
}

// abortarTodos leva a decisão de abortar aos participantes, em segundo plano.
//
// O abort NÃO é gravado (D23): quem não receber descobre a decisão perguntando,
// e um PREPARAR atrasado que chegue depois recebe NÃO porque o abortar grava um
// veto no participante que não conhecia a transação (D38). Por isso a
// repetição aqui é só "enquanto o servidor estiver no ar", e não "até
// confirmar" como na fase 2.
func (c *Coordenador) abortarTodos(txID string, participantes []string) {
	for _, empresa := range participantes {
		empresa := empresa
		go func() {
			for {
				if c.base.Err() != nil {
					return
				}
				ctx, cancelar := context.WithTimeout(c.base, c.prazoFase2)
				var err error
				if empresa == c.empresa {
					_, err = c.participante.Abortar(txID)
				} else {
					_, err = c.rede.Abortar(ctx, empresa, txID)
				}
				cancelar()
				if err == nil || ehViolacao(err) {
					return
				}
				select {
				case <-c.base.Done():
					return
				case <-time.After(c.prazoFase2):
				}
			}
		}()
	}
}

// agrupar separa as pernas pedidas por empresa dona, pelo prefixo do
// identificador da carona (D27; passo 4 da seção 7.1).
//
// A empresa dona sai do próprio identificador, sem perguntar a ninguém: é o que
// torna o agrupamento uma operação local, e é por isso que os IDs levam
// prefixo. O resultado da busca informava a empresa de cada perna, mas o
// RESERVAR do cliente não a devolve, e não precisa.
func (c *Coordenador) agrupar(itens []dominio.ItemReserva) (map[string][]dominio.ItemReserva, error) {
	porEmpresa := make(map[string][]dominio.ItemReserva)
	for _, item := range itens {
		empresa, err := dominio.EmpresaDoID(item.CaronaID)
		if err != nil {
			return nil, err
		}
		if !c.consorcio[empresa] {
			return nil, fmt.Errorf("%w: %q não é do consórcio", dominio.ErrEmpresaInvalida, empresa)
		}
		porEmpresa[empresa] = append(porEmpresa[empresa], item)
	}
	return porEmpresa, nil
}

// anotar escreve a linha de decisão no registro de operações (D19). Fora de
// qualquer seção crítica, como manda a decisão.
func (c *Coordenador) anotar(txID, decisao string) {
	if c.registro != nil {
		c.registro.Decisao(txID, decisao)
	}
}

// ordenarPernas põe as pernas devolvidas pelos participantes na ordem do
// itinerário.
//
// Faz falta porque os votos chegam agrupados por empresa, e a lista de
// participantes está em ordem alfabética — que não tem nada a ver com a ordem
// da viagem. O encadeamento (passo 7) só faz sentido na ordem das pernas, e
// quem a conhece é o **pedido do passageiro**, cuja lista de itens já foi
// validada no passo 1 e atravessa o algoritmo inteira para isso.
//
// Ordenar pelo horário de partida seria tentador e estaria errado: um
// participante malicioso ou com defeito poderia devolver horários que
// reordenassem o itinerário e fizessem passar um encadeamento que o passageiro
// não pediu.
func ordenarPernas(pernas []dominio.Perna, ordem []dominio.ItemReserva) []dominio.Perna {
	posicao := make(map[string]int, len(ordem))
	for i, item := range ordem {
		posicao[item.CaronaID] = i
	}
	emOrdem := append([]dominio.Perna(nil), pernas...)
	sort.SliceStable(emOrdem, func(i, j int) bool {
		return posicao[emOrdem[i].CaronaID] < posicao[emOrdem[j].CaronaID]
	})
	return emOrdem
}

// recusa é o motivo pelo qual uma tentativa não confirmou, guardado para que o
// laço de tentativas decida entre tentar de novo e responder ao passageiro.
type recusa struct {
	motivo   string
	empresa  string
	caronaID string
	trecho   int
	// indisponivel é a empresa que não respondeu, quando foi esse o motivo.
	//
	// A fase 7 acrescenta aqui o oposto dela: um sinalizador de que a última
	// resposta pendente era AGUARDE, que é o que a seção 5.2 da API usa para
	// distinguir EMPRESA_INDISPONIVEL (alguém não respondeu) de
	// DISPUTA_PERDIDA (todos responderam, e o último pedia para esperar). Com
	// no-wait não há AGUARDE, então a distinção ainda não decide nada.
	indisponivel string
	// definitivo é o erro de domínio quando a recusa não veio de um voto, e sim
	// da validação do coordenador (passo 7) ou do commit.
	definitivo error
}

// erro traduz a recusa no erro de domínio que a borda converte em código do
// PROTOCOL.md (seção 5.5; tabela da seção 5.2 da API).
func (r recusa) erro() error {
	if r.definitivo != nil {
		return r.definitivo
	}
	switch r.motivo {
	case MotivoSemAssento:
		return &dominio.ErroSemAssento{CaronaID: r.caronaID, IndiceTrecho: r.trecho}
	case MotivoCaronaNaoEncontrada:
		return fmt.Errorf("%w: %q", dominio.ErrCaronaNaoEncontrada, r.caronaID)
	case MotivoCaronaCancelada:
		return fmt.Errorf("%w: %q", dominio.ErrCaronaCancelada, r.caronaID)
	case MotivoItinerarioInvalido:
		return fmt.Errorf("%w: recusado por %q", dominio.ErrItinerarioInvalido, r.empresa)
	case MotivoPrecedencia:
		// Tentativas esgotadas na disputa. Nesta fase o participante é no-wait e
		// nunca responde PRECEDENCIA, então este caminho não é alcançável; a
		// fase 7 o troca por DISPUTA_PERDIDA, que é o código próprio da D36.
		return &dominio.ErroSemAssento{CaronaID: r.caronaID, IndiceTrecho: r.trecho}
	default:
		// Inclui TRANSACAO_ABORTADA e o prazo esgotado: alguma empresa não
		// respondeu, ou respondeu que a transação já estava abortada ali porque
		// a rede atrasou a ponto de alguém consultar antes (seção 5.2 da API).
		// Nenhum assento fica preso: o ABORTAR já foi disparado.
		if r.indisponivel != "" {
			return fmt.Errorf("%w: %q", dominio.ErrEmpresaIndisponivel, r.indisponivel)
		}
		return dominio.ErrEmpresaIndisponivel
	}
}
