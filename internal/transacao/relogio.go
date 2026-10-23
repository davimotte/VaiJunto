// Package transacao é a camada 5 do PROJETO_P2.md (seção 5.2): coordenação
// distribuída. Nesta fase ela tem só o relógio de Lamport (D24, seção 8.1); o
// 2PC, o wait-die, a terminação e o outbox entram nas fases 4 a 8.
//
// O pacote importa internal/dominio e nunca o contrário (seção 5.3).
package transacao

import "sync"

// Relogio é o contador L desta empresa (D24, seção 8.1).
//
// O mutex é dele e não tem nada a ver com o do estado (D04): quem mexe no
// relógio é a camada REST, que trabalha fora da seção crítica do domínio
// (regra 2 do CLAUDE.md). Tomar o lock do estado para somar 1 num contador
// acoplaria o envio de uma requisição ao lock que protege os assentos.
//
// O tipo é uint64 porque L só cresce; não há valor negativo a representar, e o
// cabeçalho da API é "inteiro decimal ≥ 0".
type Relogio struct {
	mu sync.Mutex
	l  uint64
}

// NovoRelogio cria um relógio em zero. Quem já tem log avança com Restaurar,
// no boot.
func NovoRelogio() *Relogio { return &Relogio{} }

// Agora lê L sem alterá-lo.
//
// Dois usos, ambos de leitura pura:
//   - o valor que vai no cabeçalho X-VaiJunto-Lamport ao enviar. A seção 8.1
//     incrementa ao receber e ao criar uma transação, nunca ao enviar: a
//     mensagem carrega o que o remetente sabia no momento em que a montou;
//   - o campo lamport de cada registro do log (seção 4.8). Gravar em disco é
//     evento local da empresa e não é troca de mensagem, então não move o
//     relógio.
func (r *Relogio) Agora() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.l
}

// AoCriarTransacao incrementa L e devolve o novo valor, para o carimbo de uma
// transação que nasce aqui (D24; seção 7.1, passo 3).
//
// É a outra metade da regra da seção 8.1, cuja primeira metade é AoReceber:
// incrementa-se ao receber **e ao criar uma transação**. Sem o incremento aqui,
// duas transações criadas em sequência no mesmo servidor sairiam com o mesmo
// carimbo, e a ordem total que o wait-die precisa (fase 7) deixaria de ser
// total — o desempate por empresa não resolve, porque a empresa é a mesma.
//
// O carimbo é fixado uma vez por reserva, e não por tentativa: as três
// tentativas da D36 compartilham o mesmo, para que a transação fique
// relativamente mais velha a cada disputa e não haja inanição (seção 8.5).
func (r *Relogio) AoCriarTransacao() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.l++
	return r.l
}

// AoReceber incorpora o valor que veio de outro servidor: L ← max(L, recebido) + 1,
// e devolve o novo L.
//
// É o único caminho pelo qual um número de fora entra no relógio, e é o que
// torna a ordem por carimbo compatível com a causalidade (seção 8.1): depois
// desta chamada, qualquer transação criada aqui tem carimbo maior que o de
// tudo o que o remetente já conhecia.
//
// O +1 acontece mesmo quando o valor recebido é menor que L — o recebimento é
// um evento, e dois eventos distintos no mesmo servidor não podem ter o mesmo
// carimbo.
func (r *Relogio) AoReceber(recebido uint64) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if recebido > r.l {
		r.l = recebido
	}
	r.l++
	return r.l
}

// Restaurar leva o relógio ao maior valor encontrado no log, no boot
// (seção 4.8, último parágrafo).
//
// Usa max, e não atribuição, para que o relógio nunca ande para trás: se o
// chamador passar um valor menor que o atual, nada acontece. Um relógio que
// recuasse reemitiria carimbos já usados, e duas transações diferentes
// passariam a comparar como iguais em servidores diferentes.
func (r *Relogio) Restaurar(valor uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if valor > r.l {
		r.l = valor
	}
}
