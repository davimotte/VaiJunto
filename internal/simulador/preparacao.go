package simulador

import (
	"fmt"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// formatoData é o do campo "data" de BUSCAR_ITINERARIOS (PROTOCOL.md, seção
// 3): um dia civil, que só existe dentro de um fuso.
const formatoData = "2006-01-02"

const (
	// horaDaPrimeiraPerna e duracaoDaPerna montam os horários das caronas
	// sintéticas, e passoEntrePernas é a soma da perna com a folga de
	// baldeação.
	//
	// A folga é de 1 h, dentro de [MARGEM_BALDEACAO, ESPERA_MAXIMA_BALDEACAO] =
	// [30 min, 12 h] (D13). Não é a folga mínima de propósito: no limite exato
	// de 30 min, um itinerário que a busca aceita e a reserva recusa seria um
	// bug difícil de distinguir de um erro do cenário.
	horaDaPrimeiraPerna = 8
	duracaoDaPerna      = 2 * time.Hour
	passoEntrePernas    = 3 * time.Hour

	// precoDoTrecho é o preço de cada trecho das caronas sintéticas, em
	// centavos (D11). O valor não participa de nenhuma regra da carga; existe
	// porque PUBLICAR_CARONA o exige.
	precoDoTrecho = 2500
)

// par é um par origem/destino de busca.
type par struct{ origem, destino string }

// plano é o cenário preparado: os dias em que as caronas sintéticas existem, o
// par de cidades que os passageiros de cada empresa buscam e o catálogo, para o
// sorteio das buscas vazias.
type plano struct {
	dias    []time.Time
	busca   map[string]par
	cidades []string
}

// publicacao é uma carona sintética a publicar, e por qual empresa.
type publicacao struct {
	empresa  string
	paradas  []protocolo.Parada
	assentos int
	precos   []int
}

// montarPlano desenha o cenário do ponto: quais caronas publicar e o que cada
// passageiro vai buscar.
//
// Os horários saem no fuso das cidades (D09, seção 10.1 do PROJETO.md), e nunca
// em time.Local: dentro do contêiner, time.Local é UTC, e uma carona montada
// nele sairia três horas deslocada.
func montarPlano(cfg Config, dias, diaBase int) (plano, []publicacao, error) {
	cidades := dominio.CidadesAtendidas()
	necessarias := len(cfg.Empresas) + 1
	if cfg.Cenario == CenarioLocal {
		necessarias = 2 * len(cfg.Empresas)
	}
	if necessarias > len(cidades) {
		return plano{}, nil, fmt.Errorf("cenário %s com %d empresas precisa de %d cidades, e o catálogo tem %d",
			cfg.Cenario, len(cfg.Empresas), necessarias, len(cidades))
	}

	fuso := dominio.FusoDasCidades()
	hoje := time.Now().In(fuso)
	primeiro := time.Date(hoje.Year(), hoje.Month(), hoje.Day(), 0, 0, 0, 0, fuso).AddDate(0, 0, diaBase)

	p := plano{
		dias:    make([]time.Time, 0, dias),
		busca:   make(map[string]par, len(cfg.Empresas)),
		cidades: cidades,
	}
	for i := 0; i < dias; i++ {
		p.dias = append(p.dias, primeiro.AddDate(0, 0, i))
	}

	// No cenário federado, as empresas formam uma corrente: a empresa k leva da
	// cidade k à cidade k+1, e todos os passageiros buscam da primeira à
	// última. Como nenhuma cidade repete e as folgas respeitam a D13, a busca
	// devolve um itinerário só, com uma perna em cada empresa — e o RESERVAR
	// vira um 2PC com um participante por empresa.
	//
	// No cenário local, cada empresa fica com um par de cidades só seu, e os
	// seus passageiros buscam esse par. Nenhuma outra empresa tem carona que
	// encadeie ali, então o itinerário é sempre de uma perna, da própria
	// empresa do passageiro.
	var publicacoes []publicacao
	for k, e := range cfg.Empresas {
		var origem, destino string
		if cfg.Cenario == CenarioFederado {
			origem, destino = cidades[k], cidades[k+1]
			p.busca[e.Nome] = par{origem: cidades[0], destino: cidades[len(cfg.Empresas)]}
		} else {
			origem, destino = cidades[2*k], cidades[2*k+1]
			p.busca[e.Nome] = par{origem: origem, destino: destino}
		}

		// A k-ésima perna da corrente sai passoEntrePernas depois da anterior.
		// No cenário local não há corrente, e toda carona sai no mesmo horário.
		deslocamento := time.Duration(k) * passoEntrePernas
		if cfg.Cenario == CenarioLocal {
			deslocamento = 0
		}

		for _, dia := range p.dias {
			partida := dia.Add(time.Duration(horaDaPrimeiraPerna)*time.Hour + deslocamento)
			publicacoes = append(publicacoes, publicacao{
				empresa: e.Nome,
				paradas: []protocolo.Parada{
					{Cidade: origem, Horario: partida},
					{Cidade: destino, Horario: partida.Add(duracaoDaPerna)},
				},
				assentos: cfg.Assentos,
				precos:   []int{precoDoTrecho},
			})
		}
	}
	return p, publicacoes, nil
}

// publicar manda as caronas sintéticas pelos motoristas da semente, uma
// conexão por empresa.
//
// A preparação fica **fora** das métricas: ela mede o custo de publicar, que
// não é o que a seção 11.4 pede, e acontece antes da largada, sem concorrência
// nenhuma. Uma recusa aqui é erro do simulador, e não resultado: sem as caronas
// não há o que buscar.
func publicar(cfg Config, publicacoes []publicacao) error {
	for _, e := range cfg.Empresas {
		sessao, err := abrir(e.Endereco, e.Nome+"-"+e.Motorista, cfg.Prazo)
		if err != nil {
			return fmt.Errorf("preparação da %s: %w", e.Nome, err)
		}
		if err := publicarNaEmpresa(sessao, e, publicacoes); err != nil {
			sessao.fechar()
			return fmt.Errorf("preparação da %s: %w", e.Nome, err)
		}
		sessao.fechar()
	}
	return nil
}

func publicarNaEmpresa(s *sessao, e Empresa, publicacoes []publicacao) error {
	if err := s.entrar(e.Motorista, e.SenhaMotorista); err != nil {
		return err
	}
	for _, p := range publicacoes {
		if p.empresa != e.Nome {
			continue
		}
		resposta, _, err := s.executar(protocolo.TipoPublicarCarona, protocolo.PublicarCaronaRequisicao{
			Paradas:        p.paradas,
			Assentos:       p.assentos,
			PrecosCentavos: p.precos,
		}, nil)
		if err != nil {
			return err
		}
		if resposta.Status != protocolo.StatusOK {
			return fmt.Errorf("publicar %s → %s em %s recusado com %s: %s",
				p.paradas[0].Cidade, p.paradas[len(p.paradas)-1].Cidade,
				p.paradas[0].Horario.Format(time.RFC3339), resposta.Codigo, resposta.Mensagem)
		}
	}
	return nil
}

// conferirCenario faz uma busca de prova depois de publicar: no primeiro dia,
// pelo par da primeira empresa, a busca tem de devolver **exatamente um**
// itinerário, com uma perna por empresa no cenário federado e uma só no local.
//
// Serve a dois defeitos que calariam de outro jeito. Um itinerário a mais
// significa que os dias deste cenário caíram em cima de uma rodada anterior,
// cujas caronas continuam no log (D22): a medição seguinte seria de SEM_ASSENTO
// nas caronas velhas e de CONFLITO_HORARIO com as reservas que elas ainda
// sustentam. Zero itinerários, ou um com o número errado de pernas, significa
// que a corrente de cidades e horários não fechou, e a carga inteira mediria
// buscas vazias — com uma tabela de aparência perfeitamente normal.
//
// A busca de prova usa a primeira conta de passageiro e não é medida: ela
// acontece antes da largada, sem concorrência.
func conferirCenario(cfg Config, pl plano) error {
	primeira := cfg.Empresas[0]
	pernasEsperadas := 1
	if cfg.Cenario == CenarioFederado {
		pernasEsperadas = len(cfg.Empresas)
	}

	s, err := abrir(primeira.Endereco, primeira.Nome+"-prova", cfg.Prazo)
	if err != nil {
		return fmt.Errorf("busca de prova na %s: %w", primeira.Nome, err)
	}
	defer s.fechar()

	if err := s.entrar("teste01", primeira.SenhaPassageiro); err != nil {
		return fmt.Errorf("busca de prova na %s: %w", primeira.Nome, err)
	}

	procurado := pl.busca[primeira.Nome]
	dia := pl.dias[0]
	var encontrados protocolo.BuscarItinerariosResposta
	resposta, _, err := s.executar(protocolo.TipoBuscarItinerarios,
		protocolo.BuscarItinerariosRequisicao{
			Origem:  procurado.origem,
			Destino: procurado.destino,
			Data:    dia.Format(formatoData),
		}, &encontrados)
	if err != nil {
		return fmt.Errorf("busca de prova na %s: %w", primeira.Nome, err)
	}
	if resposta.Status != protocolo.StatusOK {
		return fmt.Errorf("busca de prova na %s recusada com %s: %s", primeira.Nome, resposta.Codigo, resposta.Mensagem)
	}
	if encontrados.Parcial {
		return fmt.Errorf("busca de prova parcial: as empresas %v não responderam", encontrados.EmpresasIndisponiveis)
	}

	if len(encontrados.Itinerarios) != 1 {
		return fmt.Errorf("busca de prova %s → %s em %s devolveu %d itinerário(s), e o cenário preparado tem 1: "+
			"os dias desta rodada caíram sobre caronas que já estavam no log; escolha outro --dia-base ou apague o diretório de estado das empresas",
			procurado.origem, procurado.destino, dia.Format(formatoData), len(encontrados.Itinerarios))
	}
	if pernas := len(encontrados.Itinerarios[0].Trechos); pernas != pernasEsperadas {
		return fmt.Errorf("busca de prova %s → %s devolveu itinerário de %d perna(s), e o cenário %s tem %d",
			procurado.origem, procurado.destino, pernas, cfg.Cenario, pernasEsperadas)
	}
	return nil
}
