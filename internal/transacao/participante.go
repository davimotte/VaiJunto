package transacao

import (
	"time"

	"vaijunto/internal/dominio"
)

// Participante é o lado dono dos assentos do 2PC (PROJETO_P2.md, seção 7.2).
//
// Ele não decide nada: a decisão de preparar, confirmar ou abortar é tomada
// dentro da seção crítica do domínio, que é o único lugar capaz de olhar os
// contadores e gravar no log sem que ninguém entre no meio (D05, D22). O que
// esta camada acrescenta é o que o domínio não pode ter:
//
//   - o relógio local, que arma o prazo de dúvida da participação;
//   - um ponto único chamado pelos **dois** caminhos de entrada — os handlers
//     REST, quando o coordenador é outra empresa, e o coordenador local, quando
//     a empresa do passageiro também é dona de algum trecho (seção 7.1, passo
//     5). Participante de si mesma passa pelo mesmo código, sem rede;
//   - o lugar onde a terminação cooperativa (fase 6A) e o wait-die (fase 7)
//     entram, sem que a borda REST precise saber que existem.
//
// Não recebe context: aqui não há prazo a respeitar nem rede a esperar. O
// trabalho é tomar o mutex, gravar com fsync e devolver — a espera do 2PC é do
// coordenador, e a do wait-die vira resposta AGUARDE (seção 8.2), nunca um
// handler pendurado.
type Participante struct {
	estado *dominio.Estado
}

// NovoParticipante liga o participante ao estado da empresa.
func NovoParticipante(estado *dominio.Estado) *Participante {
	return &Participante{estado: estado}
}

// Preparar avalia um PREPARAR e, quando o voto é SIM, devolve os dados
// autoritativos das pernas locais (D34).
//
// O erro devolvido é um sentinela do domínio. Quem o traduz em voto NÃO com
// motivo, ou em erro HTTP, é a borda (seção 5.5): a mesma tabela serve ao
// caminho HTTP e à rede em memória, e por isso nenhuma das duas inventa um
// código seu.
func (p *Participante) Preparar(pedido dominio.PedidoPreparacao) ([]dominio.Perna, error) {
	// time.Now() cru, sem .In(FusoDasCidades()): este instante nunca é exibido,
	// só comparado com um prazo (D41), e In() descarta a leitura monotônica do
	// relógio. Com ela, o prazo de dúvida da fase 6A fica imune a ajuste de
	// relógio de parede. É o oposto do que a borda TCP faz, onde o instante vai
	// para a tela e o fuso importa.
	return p.estado.Preparar(pedido, time.Now())
}

// Confirmar aplica a decisão de confirmar e devolve a situação resultante
// (seção 4.4 da API).
func (p *Participante) Confirmar(txID string) (dominio.Situacao, error) {
	return p.estado.Confirmar(txID)
}

// Abortar aplica a decisão de abortar, liberando os assentos em espera, e grava
// o veto da D38 quando a transação é desconhecida (seção 4.5 da API).
func (p *Participante) Abortar(txID string) (dominio.Situacao, error) {
	return p.estado.Abortar(txID)
}

// Consultar responde a consulta da terminação cooperativa (seção 4.6 da API;
// seção 9.4), no papel que o prefixo do txID dá a esta empresa.
//
// Está no Participante, apesar de uma das respostas ser a do coordenador,
// porque esta é a porta única dos recursos de transação: a borda REST e a rede
// em memória entregam aqui, e nenhuma das duas precisa saber de que lado da
// transação esta empresa está. Quem decide o papel é o identificador, não quem
// chamou — a seção 4.6 é explícita em que a empresa do prefixo responde sempre
// como coordenadora, mesmo sendo também dona de algum trecho.
//
// Os dois lados são leituras diferentes do **mesmo** estado travado, e é por
// isso que nenhum dos dois precisa consultar o Coordenador: a decisão de uma
// transação é a reserva no log mais a marca de votação em memória, e as duas
// moram no Estado (seção 4.6).
//
// O erro é sentinela do domínio, como nos outros três recursos: só o caminho
// do participante pode falhar, e só porque o veto é uma **escrita** — se o
// fsync não acontece, a resposta DESCONHECIDA não pode sair (D22, regra 3).
func (p *Participante) Consultar(txID string) (RespostaConsulta, error) {
	empresaDoTx, err := dominio.EmpresaDoID(txID)
	if err != nil {
		return RespostaConsulta{}, err
	}

	if empresaDoTx == p.estado.Empresa() {
		// Somente leitura: o abort presumido não grava nada (D23).
		return RespostaConsulta{
			Papel:   PapelCoordenador,
			Decisao: p.estado.DecisaoDaTransacao(txID),
		}, nil
	}

	situacao, conhecida, err := p.estado.ConsultarParticipacao(txID)
	if err != nil {
		return RespostaConsulta{}, err
	}
	if !conhecida {
		// O veto já está no log: quando ConsultarParticipacao retorna, o fsync
		// aconteceu, e só então a resposta sai.
		return RespostaConsulta{Papel: PapelParticipante, Situacao: SituacaoDesconhecida}, nil
	}
	return RespostaConsulta{Papel: PapelParticipante, Situacao: string(situacao)}, nil
}
