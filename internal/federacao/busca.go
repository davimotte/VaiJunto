package federacao

import (
	"context"
	"fmt"
	"sort"
	"time"

	"vaijunto/internal/dominio"
)

// Resultado é o que a busca federada entrega à borda TCP (PROJETO_P2.md, seção
// 6, passo 7).
//
// Parcial e EmpresasIndisponiveis não são erro: os itinerários que vieram são
// válidos, e o que falta é a informação de que podem existir outros. Tratar a
// queda de um par como falha da busca faria uma empresa fora do ar derrubar a
// consulta das outras duas, que é exatamente o contrário da D30.
//
// EmpresasIndisponiveis vem ordenada e nunca é nil.
type Resultado struct {
	Itinerarios           []dominio.Itinerario
	Parcial               bool
	EmpresasIndisponiveis []string
}

// Busca executa o algoritmo de busca federada da seção 6 do PROJETO_P2.md.
//
// Mora aqui, e não em internal/servidor, porque a busca federada é a camada
// 1–4' da seção 5.2: ela é quem conhece prazo e rede. A borda TCP só delega.
//
// Não guarda estado próprio entre buscas: nada do que uma busca descobre sobre
// os pares sobrevive a ela. Isso é a D26 — sem cache nem réplica das caronas
// alheias — e não uma simplificação pendente.
type Busca struct {
	empresa  string
	estado   *dominio.Estado
	cliente  *Cliente
	registro *Registro
	prazo    time.Duration
}

// NovaBusca monta a busca a partir da mesma Config das outras duas pontas da
// pilha REST, para que o relógio e a lista de pares sejam obrigatoriamente os
// mesmos do servidor e do cliente.
func NovaBusca(cfg Config, cliente *Cliente) *Busca {
	return &Busca{
		empresa:  cfg.Empresa,
		estado:   cfg.Estado,
		cliente:  cliente,
		registro: cfg.Registro,
		prazo:    cfg.PrazoBusca,
	}
}

// Buscar devolve os itinerários da origem ao destino cuja primeira perna parte
// na data pedida, somando as caronas locais às dos pares (seção 6, passos 1 a
// 7).
//
// Os passos estão marcados no corpo, na ordem do documento. Dois pontos
// carregam o peso do desenho:
//
//   - **Nenhuma E/S de rede com o lock preso** (regra 2). O lock é tomado e
//     solto inteiro dentro do passo 3; os passos 4 e 5 são rede e CPU, sem
//     lock; o passo 6 é função pura sobre cópias.
//   - **A busca não reserva nem promete nada.** Nada aqui grava no log, nada
//     põe assento em espera. A disponibilidade informada é uma fotografia, e
//     quem a revalida é o dono, no PREPARAR (D26).
//
// data carrega o fuso em que a pergunta "que dia é 05/11?" deve ser respondida;
// quem chama é responsável por escolhê-lo (D11).
func (b *Busca) Buscar(ctx context.Context, origem, destino string, data time.Time) (Resultado, error) {
	// Passo 1 — validar. Antes de qualquer chamada: pedido inválido não gasta
	// uma rodada de rede, e o passageiro recebe o erro na hora. A função pura
	// revalida o mesmo no passo 6, e isso não é redundância acidental — é o
	// contrato dela, que precisa valer para qualquer chamador.
	if !dominio.CidadeConhecida(origem) {
		return Resultado{}, fmt.Errorf("%w: origem %q", dominio.ErrCidadeDesconhecida, origem)
	}
	if !dominio.CidadeConhecida(destino) {
		return Resultado{}, fmt.Errorf("%w: destino %q", dominio.ErrCidadeDesconhecida, destino)
	}
	if origem == destino {
		return Resultado{}, fmt.Errorf("%w: origem e destino são a mesma cidade %q", dominio.ErrRotaInvalida, origem)
	}

	// Passo 2 — calcular a janela (D40). É a mesma conta no dono e em quem
	// pergunta, porque é a mesma função do domínio.
	desde, ate := dominio.JanelaDaBusca(data)

	// Passo 3 — copiar as caronas locais sob o lock, e soltá-lo. A própria
	// empresa entra pela porta de casa, e não por HTTP contra si mesma: uma
	// chamada a si tomaria o próprio lock por um caminho que o P1 nunca
	// percorreu, e é por isso que ParsearPares recusa a própria empresa (D20).
	caronas := b.estado.CaronasNaJanela(desde, ate)

	// Passos 4 e 5 — pares em paralelo e filtragem do que chegou. Daqui em
	// diante nenhum lock está na mão.
	recebidas, indisponiveis := b.consultarPares(ctx, desde, ate)
	caronas = append(caronas, recebidas...)

	// Passo 6 — o algoritmo do P1 sobre a união, fora do lock.
	itinerarios, err := dominio.BuscarItinerariosEm(caronas, origem, destino, data)
	if err != nil {
		return Resultado{}, err
	}

	// Passo 7 — responder.
	return Resultado{
		Itinerarios:           itinerarios,
		Parcial:               len(indisponiveis) > 0,
		EmpresasIndisponiveis: indisponiveis,
	}, nil
}

// consultarPares executa os passos 4 e 5: pergunta a todos os pares em
// paralelo, com um prazo único, e devolve as caronas aceitas junto da lista
// ordenada das empresas que ficaram de fora.
//
// O prazo é **um só** para o leque inteiro (D41), e não um por chamada: o
// relógio começa aqui, antes da primeira goroutine, de modo que três pares
// lentos não somem três prazos na espera do passageiro. O cancelar no defer
// garante que nenhuma goroutine nem conexão fique pendurada depois do retorno.
//
// Uma goroutine por par, resultados por canal com capacidade igual ao número de
// pares: assim nenhuma escrita bloqueia, nem a de uma goroutine que termine
// depois de o prazo estourar, e a função não precisa de WaitGroup nem de mutex
// para juntar o que chegou.
func (b *Busca) consultarPares(ctx context.Context, desde, ate time.Time) (caronas []dominio.CaronaBusca, indisponiveis []string) {
	pares := b.cliente.Pares()
	if len(pares) == 0 {
		// Servidor sozinho: a busca é a do P1, pelo mesmo caminho. Nunca
		// parcial, porque não há par nenhum de quem ficar devendo resposta.
		return nil, []string{}
	}

	ctx, cancelar := context.WithTimeout(ctx, b.prazo)
	defer cancelar()

	type retorno struct {
		empresa  string
		resposta RespostaCaronas
		err      error
	}
	respostas := make(chan retorno, len(pares))
	for _, par := range pares {
		go func(par string) {
			r, err := b.cliente.Caronas(ctx, par, desde, ate)
			respostas <- retorno{empresa: par, resposta: r, err: err}
		}(par)
	}

	indisponiveis = make([]string, 0, len(pares))
	for range pares {
		r := <-respostas
		if r.err != nil {
			// Prazo estourado, conexão recusada, resposta ilegível ou status de
			// erro: para a busca é tudo a mesma coisa (D30). Sem repetição — a
			// seção 1.3 da API é explícita de que este recurso não repete, e a
			// empresa entra como indisponível. A linha com o motivo exato já foi
			// escrita pelo cliente.
			indisponiveis = append(indisponiveis, r.empresa)
			continue
		}
		caronas = append(caronas, b.aceitar(r.empresa, r.resposta)...)
	}

	// A ordem de chegada das respostas depende da rede; a da lista, não.
	sort.Strings(indisponiveis)
	return caronas, indisponiveis
}

// aceitar é o passo 5: converte as caronas de um par para a forma do domínio,
// descartando com registro no log toda a que não passa pela validação.
//
// Descarte não torna a empresa indisponível, e a diferença importa: uma empresa
// indisponível é uma de quem não sabemos nada, e a busca avisa que o resultado
// é parcial; uma carona descartada é um dado que conhecemos e recusamos, e o
// resto do que aquele par mandou continua valendo. Tratar as duas igual faria
// uma única carona malformada apagar o catálogo inteiro de uma empresa que está
// no ar.
//
// A conferência de que a empresa declarada é o par que respondeu fecha o
// caminho em que um par, por defeito ou de propósito, anuncia caronas de outro:
// o itinerário levaria o passageiro a um PREPARAR endereçado a quem não é dono
// do trecho.
func (b *Busca) aceitar(par string, resposta RespostaCaronas) []dominio.CaronaBusca {
	aceitas := make([]dominio.CaronaBusca, 0, len(resposta.Caronas))
	for _, c := range resposta.Caronas {
		carona := dominio.CaronaBusca{
			ID:            c.ID,
			Empresa:       c.Empresa,
			MotoristaNome: c.MotoristaNome,
			Rota:          c.Rota,
			Horarios:      c.Horarios,
			Assentos:      c.Assentos,
			PrecoTrecho:   c.PrecosCentavos,
			Livres:        c.Livres,
		}

		if carona.Empresa != par {
			b.registro.descartou(par, c.ID, fmt.Sprintf("declarada da empresa %q, que não é quem respondeu", carona.Empresa))
			continue
		}
		if err := carona.Validar(); err != nil {
			b.registro.descartou(par, c.ID, err.Error())
			continue
		}
		aceitas = append(aceitas, carona)
	}
	return aceitas
}
