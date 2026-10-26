// Package simulador gera carga de passageiros sobre as empresas do consórcio,
// falando o protocolo TCP do PROTOCOL.md como os clientes reais. É o
// entregável E4 do enunciado, especificado na seção 11.4 do PROJETO_P2.md.
//
// O simulador fala **só** TCP: a API REST entre servidores e a auditoria da
// seção 11.2 não entram aqui. Ele é um cliente, e a carga que gera é a de um
// cliente; verificar invariantes é trabalho de quem observa as três empresas,
// não de quem as usa.
//
// A lógica mora neste pacote, e não em cmd/simulador, para que percentil,
// sorteio e CSV tenham teste unitário com -race, e para que o teste de sistema
// da seção 11.5 possa chamar Rodar com as empresas no próprio processo. A
// **medição** continua tendo de rodar sem -race: o detector multiplica o custo
// de cada acesso à memória, e o número medido seria o dele.
package simulador

import (
	"fmt"
	"io"
	"time"
)

// Cenários da carga (seção 11.6). A diferença entre os dois é o custo da
// coordenação distribuída, e é ela que entra na comparação do relatório:
//
//   - em CenarioLocal, o itinerário tem uma perna só, da empresa do próprio
//     passageiro. O 2PC degenera no caso simples, com um participante que é o
//     próprio coordenador: nenhuma chamada de rede, um fsync;
//   - em CenarioFederado, o itinerário tem uma perna em cada empresa. O mesmo
//     RESERVAR passa a custar um PREPARAR por dono remoto, a votação, a
//     decisão e um fsync em cada empresa.
//
// A busca é federada nos dois: o servidor de origem consulta os pares sempre
// (D26). O que muda é o RESERVAR.
const (
	CenarioLocal    = "local"
	CenarioFederado = "federado"
)

const (
	// MaximoPassageirosPorEmpresa é o teto das contas teste01..teste50 que cada
	// empresa traz na semente (seção 12.1). A identidade é da conexão (D08):
	// dois passageiros virtuais na mesma conta disputariam a sobreposição de
	// horário um com o outro (D14), e a carga mediria CONFLITO_HORARIO em vez
	// de disputa por assento.
	MaximoPassageirosPorEmpresa = 50

	// diaBasePadrao é a distância, em dias, entre hoje e as caronas sintéticas.
	// Longe o bastante da semente de 05–06/11 (seção 12.2) e da
	// ANTECEDENCIA_CANCELAMENTO de 1 h, para que o cancelamento do ciclo nunca
	// caia em PRAZO_CANCELAMENTO_EXPIRADO.
	diaBasePadrao = 60

	// capacidadePorPassageiro é quantos assentos a preparação cria por
	// passageiro virtual. Cada passageiro carrega no máximo uma reserva, então
	// a ocupação global nunca passa do número de passageiros; com o dobro de
	// assentos, o cenário não esgota e a disputa medida é a instantânea, e não
	// o fim do estoque.
	capacidadePorPassageiro = 2

	// blocosDoRelogio é quantos blocos de dias o relógio distingue ao derivar
	// o dia base. Com 100 blocos, duas rodadas só caem no mesmo bloco se
	// começarem com 100 minutos exatos de diferença — e, quando caem,
	// conferirCenario recusa a rodada em vez de medir um cenário sujo.
	blocosDoRelogio = 100
)

// Empresa é uma empresa do consórcio na visão do simulador: onde falar TCP e
// com quais credenciais.
//
// O motorista entra na configuração, e não numa tabela fixa, porque as
// sementes das três empresas têm motoristas diferentes (joao, rafael, marcos —
// seção 12.1) e o fixture do P1, que os testes em processo carregam nas três,
// tem só os do P1.
type Empresa struct {
	Nome            string
	Endereco        string
	Motorista       string
	SenhaMotorista  string
	SenhaPassageiro string
}

// Config é a configuração de uma rodada. Há um campo por flag de
// cmd/simulador (seção 11.4).
type Config struct {
	Empresas []Empresa
	Cenario  string

	// Passageiros é por empresa, e Curva é a lista de totais no consórcio. Com
	// Curva vazia, a rodada tem um ponto só, com Passageiros em cada empresa.
	Passageiros int
	Curva       []int

	Duracao                   time.Duration
	ProbabilidadeCancelamento float64
	FracaoBuscaVazia          float64

	// Assentos das caronas sintéticas, e Dias quantos dias elas ocupam. Dias
	// zero deriva de Assentos e do número de passageiros.
	Assentos int
	Dias     int

	// DiaBase é a distância em dias entre hoje e o primeiro dia do cenário.
	// Zero deriva do relógio, para que duas rodadas contra o mesmo volume não
	// disputem os mesmos dias: o log (D22) guarda as caronas da rodada
	// anterior, e elas continuariam aparecendo na busca.
	DiaBase int

	Semente int64
	Prazo   time.Duration

	// Saida é o diretório dos CSVs; vazia não grava nada. Rotulo identifica a
	// rodada no nome do arquivo; vazio usa o cenário.
	Saida  string
	Rotulo string

	// Registro recebe a tabela e o andamento; nil descarta.
	Registro io.Writer
}

// Resultado é o que a rodada mediu.
type Resultado struct {
	Cenario  string
	Rotulo   string
	Semente  int64
	Pontos   []Ponto
	Arquivos []string
}

// CodigosObservados conta, em toda a rodada, quantas respostas de cada código
// apareceram. Serve ao teste que afirma que nenhuma resposta saiu fora dos
// códigos do PROTOCOL.md.
func (r Resultado) CodigosObservados() map[string]int {
	codigos := make(map[string]int)
	for _, ponto := range r.Pontos {
		for codigo, quantidade := range ponto.Total.PorCodigo {
			codigos[codigo] += quantidade
		}
	}
	return codigos
}

// Rodar prepara o cenário e mede cada ponto da curva, em sequência.
//
// Cada ponto ganha caronas e dias só dele. O estado sobrevive entre os pontos —
// o log é persistente e as reservas do ponto anterior continuam lá —, e dias
// repetidos fariam a busca do ponto seguinte encontrar caronas já cheias. É a
// mesma precaução do teste de carga do P1 (PROJETO.md, seção 8.3).
func Rodar(cfg Config) (Resultado, error) {
	if err := cfg.validar(); err != nil {
		return Resultado{}, err
	}

	resultado := Resultado{Cenario: cfg.Cenario, Rotulo: cfg.rotulo(), Semente: cfg.Semente}
	diaBase := cfg.DiaBase
	if diaBase == 0 {
		diaBase = diaBaseDerivado(time.Now(), cfg.maiorBloco())
	}

	for _, total := range cfg.totais() {
		porEmpresa := repartir(total, cfg.Empresas)
		dias := cfg.diasDe(total)

		plano, publicacoes, err := montarPlano(cfg, dias, diaBase)
		if err != nil {
			return resultado, err
		}
		diaBase += dias

		cfg.anunciar("preparando %d dia(s) e %d carona(s) para %d passageiro(s), a partir de hoje + %d",
			dias, len(publicacoes), total, diaBase-dias)
		if err := publicar(cfg, publicacoes); err != nil {
			return resultado, err
		}
		if err := conferirCenario(cfg, plano); err != nil {
			return resultado, err
		}

		cfg.anunciar("medindo %d passageiro(s) por %s", total, cfg.Duracao)
		ponto, err := medirPonto(cfg, plano, porEmpresa)
		if err != nil {
			return resultado, err
		}
		resultado.Pontos = append(resultado.Pontos, ponto)
	}

	imprimirTabela(cfg.Registro, resultado)
	if cfg.Saida != "" {
		arquivos, err := gravarCSV(cfg, resultado)
		if err != nil {
			return resultado, err
		}
		resultado.Arquivos = arquivos
		cfg.anunciar("resultados gravados em %s", arquivos)
	}
	return resultado, nil
}

// validar recusa a configuração antes de qualquer conexão: a regra de validar
// tudo antes de escrever qualquer coisa vale também para o simulador, porque a
// preparação publica caronas de verdade no log das empresas.
func (cfg Config) validar() error {
	if len(cfg.Empresas) == 0 {
		return fmt.Errorf("nenhuma empresa configurada")
	}
	for _, e := range cfg.Empresas {
		switch {
		case e.Nome == "":
			return fmt.Errorf("empresa sem nome")
		case e.Endereco == "":
			return fmt.Errorf("empresa %s sem endereço", e.Nome)
		case e.Motorista == "":
			return fmt.Errorf("empresa %s sem motorista para a preparação", e.Nome)
		}
	}
	if cfg.Cenario != CenarioLocal && cfg.Cenario != CenarioFederado {
		return fmt.Errorf("cenário %q: use %q ou %q", cfg.Cenario, CenarioLocal, CenarioFederado)
	}
	if cfg.Duracao <= 0 {
		return fmt.Errorf("duração %s: precisa ser positiva", cfg.Duracao)
	}
	if cfg.Prazo <= 0 {
		return fmt.Errorf("prazo %s: precisa ser positivo", cfg.Prazo)
	}
	if cfg.Assentos < 1 {
		return fmt.Errorf("assentos %d: precisa ser pelo menos 1", cfg.Assentos)
	}
	if cfg.Dias < 0 {
		return fmt.Errorf("dias %d: precisa ser zero (derivado) ou positivo", cfg.Dias)
	}
	if cfg.ProbabilidadeCancelamento < 0 || cfg.ProbabilidadeCancelamento > 1 {
		return fmt.Errorf("probabilidade de cancelamento %v: precisa estar entre 0 e 1", cfg.ProbabilidadeCancelamento)
	}
	if cfg.FracaoBuscaVazia < 0 || cfg.FracaoBuscaVazia > 1 {
		return fmt.Errorf("fração de busca vazia %v: precisa estar entre 0 e 1", cfg.FracaoBuscaVazia)
	}

	for _, total := range cfg.totais() {
		if total < 1 {
			return fmt.Errorf("ponto com %d passageiros: precisa ser pelo menos 1", total)
		}
		for nome, quantidade := range repartir(total, cfg.Empresas) {
			if quantidade > MaximoPassageirosPorEmpresa {
				return fmt.Errorf("ponto com %d passageiros deixa %d na %s, acima das %d contas de teste da semente",
					total, quantidade, nome, MaximoPassageirosPorEmpresa)
			}
		}
	}
	return nil
}

// totais é a lista de pontos da rodada, em número total de passageiros no
// consórcio.
func (cfg Config) totais() []int {
	if len(cfg.Curva) > 0 {
		return cfg.Curva
	}
	return []int{cfg.Passageiros * len(cfg.Empresas)}
}

// diasDe é quantos dias o cenário de um ponto ocupa.
func (cfg Config) diasDe(total int) int {
	if cfg.Dias > 0 {
		return cfg.Dias
	}
	dias := (capacidadePorPassageiro*total + cfg.Assentos - 1) / cfg.Assentos
	if dias < 1 {
		return 1
	}
	return dias
}

// maiorBloco é a largura em dias da rodada: o ponto que consome mais dias.
func (cfg Config) maiorBloco() int {
	maior := 1
	for _, total := range cfg.totais() {
		if dias := cfg.diasDe(total); dias > maior {
			maior = dias
		}
	}
	return maior
}

func (cfg Config) rotulo() string {
	if cfg.Rotulo != "" {
		return cfg.Rotulo
	}
	return cfg.Cenario
}

func (cfg Config) anunciar(formato string, argumentos ...any) {
	if cfg.Registro == nil {
		return
	}
	fmt.Fprintf(cfg.Registro, "simulador: "+formato+"\n", argumentos...)
}

// repartir distribui o total de passageiros entre as empresas, na ordem da
// configuração, com o resto indo para as primeiras: 10 em três empresas vira
// 4, 3 e 3.
//
// O número da curva é o total no consórcio, e não o de cada empresa, para que
// o eixo das curvas do P2 seja o mesmo das do P1 (seção 11.6) e a comparação
// do relatório seja direta.
func repartir(total int, empresas []Empresa) map[string]int {
	porEmpresa := make(map[string]int, len(empresas))
	for i, e := range empresas {
		quantidade := total / len(empresas)
		if i < total%len(empresas) {
			quantidade++
		}
		porEmpresa[e.Nome] = quantidade
	}
	return porEmpresa
}

// diaBaseDerivado espalha as rodadas no calendário, um bloco por minuto do
// relógio.
//
// As caronas da rodada anterior continuam no log (D22), e uma rodada nova sobre
// os mesmos dias encontraria, na busca, as caronas dela — já cheias, e
// contando como um itinerário a mais. A medição passaria a ser de SEM_ASSENTO e
// de CONFLITO_HORARIO com as reservas que a rodada anterior deixou de pé.
//
// O bloco é a largura em dias da rodada, e não um dia: avançar o dia base de um
// em um deixaria 59 dos 60 dias de uma rodada de 60 passageiros em cima da
// anterior. Que a data caia daqui a anos não importa — é cenário sintético, e
// nada no domínio depende de ela estar perto.
func diaBaseDerivado(agora time.Time, bloco int) int {
	return diaBasePadrao + int(agora.Unix()/60)%blocosDoRelogio*bloco
}
