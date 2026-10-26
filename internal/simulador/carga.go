package simulador

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"vaijunto/internal/protocolo"
)

// passageiro é um passageiro virtual: uma conta da semente, uma conexão só sua
// (D15), o seu coletor e o seu sorteio.
type passageiro struct {
	empresa Empresa
	conta   string
	par     par
	sessao  *sessao
	coletor *coletor
	sorteio *rand.Rand

	// reserva é a reserva que ele carrega entre dois ciclos, quando o sorteio
	// decidiu não cancelar no próprio ciclo. Vazia quando ele está de mãos
	// livres.
	reserva string
}

// medirPonto mede um ponto da curva: autentica todas as conexões, libera todas
// de uma vez e repete o ciclo do passageiro até o fim da duração.
//
// A largada é uma barreira — uma goroutine por passageiro esperando em
// `<-largada`, liberadas por um `close` — e não um laço que vai criando
// goroutinas: sem ela, os primeiros passageiros mediriam um servidor com um
// cliente, e os últimos um servidor com N.
func medirPonto(cfg Config, pl plano, porEmpresa map[string]int) (Ponto, error) {
	passageiros, err := conectarPassageiros(cfg, pl, porEmpresa)
	if err != nil {
		return Ponto{}, err
	}
	defer func() {
		for _, p := range passageiros {
			p.sessao.fechar()
		}
	}()

	largada := make(chan struct{})
	falhas := make([]error, len(passageiros))
	var espera sync.WaitGroup
	var fim time.Time

	for i, p := range passageiros {
		espera.Add(1)
		go func() {
			defer espera.Done()
			<-largada
			for time.Now().Before(fim) {
				if err := p.ciclo(cfg, pl); err != nil {
					falhas[i] = fmt.Errorf("%s:%s: %w", p.empresa.Nome, p.conta, err)
					return
				}
			}
		}()
	}

	// fim é escrito antes do close, que é a aresta de sincronização: toda
	// goroutine o lê depois de ter visto o canal fechado.
	inicio := time.Now()
	fim = inicio.Add(cfg.Duracao)
	close(largada)
	espera.Wait()
	decorrido := time.Since(inicio)

	for _, err := range falhas {
		if err != nil {
			return Ponto{}, err
		}
	}

	coletores := make([]*coletor, 0, len(passageiros))
	for _, p := range passageiros {
		coletores = append(coletores, p.coletor)
	}
	return resumir(len(passageiros), porEmpresa, decorrido, coletores), nil
}

// conectarPassageiros abre e autentica todas as sessões antes da largada. O
// login fica fora da medição: ele acontece uma vez por rodada, e o que a seção
// 11.4 pede é o custo da busca, da reserva e do cancelamento.
func conectarPassageiros(cfg Config, pl plano, porEmpresa map[string]int) ([]*passageiro, error) {
	var passageiros []*passageiro
	abortar := func(err error) ([]*passageiro, error) {
		for _, p := range passageiros {
			p.sessao.fechar()
		}
		return nil, err
	}

	// O índice é global e cresce na ordem da configuração, porque é ele que
	// semeia o sorteio: com a mesma semente, o passageiro de um dado índice faz
	// sempre as mesmas escolhas.
	indice := 0
	for _, e := range cfg.Empresas {
		for i := 0; i < porEmpresa[e.Nome]; i++ {
			conta := fmt.Sprintf("teste%02d", i+1)
			rotulo := fmt.Sprintf("%s-%s", e.Nome, conta)

			s, err := abrir(e.Endereco, rotulo, cfg.Prazo)
			if err != nil {
				return abortar(err)
			}
			if err := s.entrar(conta, e.SenhaPassageiro); err != nil {
				s.fechar()
				return abortar(err)
			}

			passageiros = append(passageiros, &passageiro{
				empresa: e,
				conta:   conta,
				par:     pl.busca[e.Nome],
				sessao:  s,
				coletor: &coletor{},
				sorteio: sorteioDoPassageiro(cfg.Semente, indice),
			})
			indice++
		}
	}
	if len(passageiros) == 0 {
		return nil, fmt.Errorf("nenhum passageiro a medir")
	}
	return passageiros, nil
}

// ciclo é uma sessão de passageiro: busca, reserva do primeiro itinerário e,
// com a probabilidade configurada, cancelamento (seção 11.4).
//
// O passageiro carrega no máximo uma reserva, como uma pessoa real: a que
// sobrou do ciclo anterior é cancelada antes de buscar de novo. É isso que
// mantém a ocupação global limitada ao número de passageiros, e portanto que
// faz a disputa medida ser a instantânea, e não o esgotamento do cenário.
//
// Uma recusa não interrompe nada: SEM_ASSENTO, DISPUTA_PERDIDA,
// EMPRESA_INDISPONIVEL e as outras são resultados da medição. Só falha de
// transporte devolve erro, e aí este passageiro para.
func (p *passageiro) ciclo(cfg Config, pl plano) error {
	if p.reserva != "" {
		if err := p.cancelar(); err != nil {
			return err
		}
	}

	// Os sorteios acontecem sempre na mesma ordem e sem depender do ramo
	// tomado: é o que mantém a sequência de escolhas de um passageiro igual
	// entre duas rodadas com a mesma semente.
	dia := pl.dias[p.sorteio.Intn(len(pl.dias))]
	vazia := p.sorteio.Float64() < cfg.FracaoBuscaVazia
	origem, destino := p.par.origem, p.par.destino
	operacao := OpBuscar
	if vazia {
		origem, destino = parSorteado(p.sorteio, pl.cidades)
		operacao = OpBuscarVazia
	}

	var encontrados protocolo.BuscarItinerariosResposta
	resposta, latencia, err := p.sessao.executar(protocolo.TipoBuscarItinerarios,
		protocolo.BuscarItinerariosRequisicao{
			Origem:  origem,
			Destino: destino,
			Data:    dia.Format(formatoData),
		}, &encontrados)
	if err != nil {
		return err
	}
	p.coletor.registrar(operacao, resposta, latencia)
	if resposta.Status != protocolo.StatusOK || len(encontrados.Itinerarios) == 0 {
		return nil
	}

	// Reserva o **primeiro** itinerário, que é o melhor pela ordem da D16, e
	// reserva o que a busca devolveu, e não um itinerário montado à mão: é o
	// que o cliente de menu faz, e é o que exercita o encadeamento que o
	// coordenador vai revalidar.
	primeiro := encontrados.Itinerarios[0]
	itens := make([]protocolo.ItemReserva, 0, len(primeiro.Trechos))
	for _, t := range primeiro.Trechos {
		itens = append(itens, protocolo.ItemReserva{CaronaID: t.CaronaID, De: t.De, Ate: t.Ate})
	}

	var confirmada protocolo.ReservarResposta
	resposta, latencia, err = p.sessao.executar(protocolo.TipoReservar,
		protocolo.ReservarRequisicao{Trechos: itens}, &confirmada)
	if err != nil {
		return err
	}
	p.coletor.registrar(OpReservar, resposta, latencia)

	cancelar := p.sorteio.Float64() < cfg.ProbabilidadeCancelamento
	if resposta.Status != protocolo.StatusOK {
		return nil
	}
	p.reserva = confirmada.ReservaID
	if cancelar {
		return p.cancelar()
	}
	return nil
}

// cancelar cancela a reserva que o passageiro carrega.
//
// A reserva sai das mãos dele mesmo quando a resposta é recusa: insistir nela
// a cada ciclo travaria o passageiro num erro permanente — uma reserva já
// cancelada em cascata pelo motorista, por exemplo — e a carga pararia de
// medir reserva nova.
func (p *passageiro) cancelar() error {
	resposta, latencia, err := p.sessao.executar(protocolo.TipoCancelarReserva,
		protocolo.CancelarReservaRequisicao{ReservaID: p.reserva}, nil)
	if err != nil {
		return err
	}
	p.coletor.registrar(OpCancelar, resposta, latencia)
	p.reserva = ""
	return nil
}

// sorteioDoPassageiro é o gerador de um passageiro virtual.
//
// A semente é a da rodada somada ao índice global, e não a da rodada
// compartilhada: um gerador por passageiro dispensa trava no caminho da carga
// e, com a mesma semente, o passageiro de um dado índice faz sempre as mesmas
// escolhas. O que a semente **não** fixa é o entrelaçamento entre passageiros,
// que é do escalonador — por isso duas rodadas iguais sorteiam igual, mas não
// disputam igual.
func sorteioDoPassageiro(semente int64, indice int) *rand.Rand {
	return rand.New(rand.NewSource(semente + int64(indice)))
}

// parSorteado devolve um par de cidades distintas do catálogo, para a fração
// de buscas que não deve achar nada.
//
// Origem igual ao destino é recusada com ROTA_INVALIDA (PROTOCOL.md, seção 6),
// que é defeito do cliente e não resultado da carga: o segundo sorteio é
// deslocado para nunca cair na origem.
func parSorteado(sorteio *rand.Rand, cidades []string) (string, string) {
	origem := sorteio.Intn(len(cidades))
	destino := (origem + 1 + sorteio.Intn(len(cidades)-1)) % len(cidades)
	return cidades[origem], cidades[destino]
}
