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
