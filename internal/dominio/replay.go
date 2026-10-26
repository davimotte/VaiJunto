package dominio

import (
	"encoding/json"
	"fmt"
)

// reaplicar refaz o estado a partir do log (D37, seção 9.2). Roda no boot,
// com e.mu já travado.
//
// Três regras:
//   - NÃO revalida. As regras temporais da D13 dependem do relógio; revalidar
//     rejeitaria fatos que foram aceitos no passado (uma carona cuja partida
//     já passou). Quem validou foi a operação original, antes de gravar.
//   - Usa os IDs do registro e nunca gera ID novo.
//   - Não grava no log.
//
// "Sem revalidar" não é "sem checar": um registro que o domínio não entende
// (tipo desconhecido, dados ilegíveis, ID repetido, cancelamento de carona
// que não existe) não é um fato passado, é corrupção, e o boot é recusado com
// o número do registro.
//
// Devolve o maior carimbo de Lamport visto, para o boot restaurar o relógio
// (seção 4.8). É o maior, e não o do último registro, porque o campo é o L do
// momento da gravação e nada obriga o log a ser crescente nele: o relógio
// avança por mensagem recebida, não por registro gravado.
func reaplicar(e *Estado, registros [][]byte) (uint64, error) {
	var maiorLamport uint64
	for i, bruto := range registros {
		n := i + 1
		var r registro
		if err := json.Unmarshal(bruto, &r); err != nil {
			return 0, fmt.Errorf("dominio: log, registro %d: envelope ilegível: %w", n, err)
		}
		switch r.Tipo {
		case tipoCaronaPublicada:
			var d dadosCaronaPublicada
			if err := json.Unmarshal(r.Dados, &d); err != nil {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): dados ilegíveis: %w", n, r.Tipo, err)
			}
			if len(d.Rota) < 2 || len(d.Horarios) != len(d.Rota) || len(d.PrecosCentavos) != len(d.Rota)-1 {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): carona %q com rota, horários e preços incoerentes", n, r.Tipo, d.Carona)
			}
			if _, existe := e.caronas[d.Carona]; existe {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): carona %q já existe", n, r.Tipo, d.Carona)
			}
			livres := make([]int, len(d.Rota)-1)
			for j := range livres {
				livres[j] = d.Assentos
			}
			e.caronas[d.Carona] = &Carona{
				ID:          d.Carona,
				MotoristaID: d.Motorista,
				Rota:        d.Rota,
				Horarios:    d.Horarios,
				Assentos:    d.Assentos,
				PrecoTrecho: d.PrecosCentavos,
				Livres:      livres,
			}
		case tipoCaronaCancelada:
			var d dadosCaronaCancelada
			if err := json.Unmarshal(r.Dados, &d); err != nil {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): dados ilegíveis: %w", n, r.Tipo, err)
			}
			c, ok := e.caronas[d.Carona]
			if !ok {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): carona %q não existe", n, r.Tipo, d.Carona)
			}
			c.Cancelada = true
		case tipoPreparado:
			var d dadosPreparado
			if err := json.Unmarshal(r.Dados, &d); err != nil {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): dados ilegíveis: %w", n, r.Tipo, err)
			}
			if _, repetida := e.participacoes[d.Tx]; repetida {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): transação %q já tem participação", n, r.Tipo, d.Tx)
			}
			if len(d.Itens) == 0 {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): transação %q sem itens", n, r.Tipo, d.Tx)
			}
			// O PREPARADO só foi gravado depois de o passo 3 encontrar
			// Livres >= 1 em todo trecho pedido (seção 7.2), então um log
			// coerente nunca leva o contador abaixo de zero. Se levar, não é um
			// fato do passado que o replay deva aceitar (D37, regra 2): é
			// corrupção, e subir com Livres negativo quebraria I-P2-1 em
			// silêncio.
			itens := make([]ItemReserva, 0, len(d.Itens))
			for _, item := range d.Itens {
				c, existe := e.caronas[item.Carona]
				if !existe {
					return 0, fmt.Errorf("dominio: log, registro %d (%s): carona %q não existe", n, r.Tipo, item.Carona)
				}
				if item.De < 0 || item.De >= item.Ate || item.Ate > len(c.Rota)-1 {
					return 0, fmt.Errorf("dominio: log, registro %d (%s): trecho [%d,%d) fora da rota de %q",
						n, r.Tipo, item.De, item.Ate, item.Carona)
				}
				for t := item.De; t < item.Ate; t++ {
					if c.Livres[t] < 1 {
						return 0, fmt.Errorf("dominio: log, registro %d (%s): carona %q, trecho %d sem assento para descontar",
							n, r.Tipo, item.Carona, t)
					}
					c.Livres[t]--
				}
				itens = append(itens, ItemReserva{CaronaID: item.Carona, De: item.De, Ate: item.Ate})
			}
			// PreparadaEm fica zerada: é instante local, não vai ao log, e o
			// prazo de dúvida recomeça no boot (seção 4.4). Quem passa a armá-lo
			// é a recuperação da fase 6A.
			e.participacoes[d.Tx] = &Participacao{
				TxID:          d.Tx,
				Coordenador:   d.Coordenador,
				Participantes: d.Participantes,
				Itens:         itens,
				Carimbo:       Carimbo{Lamport: d.Carimbo.Lamport, Empresa: d.Carimbo.Empresa},
				Passageiro:    Passageiro{Ref: d.Passageiro.Ref, Nome: d.Passageiro.Nome},
				Situacao:      SituacaoPreparado,
			}

		case tipoConfirmado:
			var d dadosTransacao
			if err := json.Unmarshal(r.Dados, &d); err != nil {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): dados ilegíveis: %w", n, r.Tipo, err)
			}
			// confirmar só grava a partir de PREPARADO (seção 7.2), então
			// qualquer outra situação aqui — inclusive CONFIRMADO repetido —
			// significa log corrompido. Os assentos continuam descontados: o em
			// espera virou confirmado e a conta de I-P2-1 não muda.
			pt, conhecida := e.participacoes[d.Tx]
			if !conhecida || pt.Situacao != SituacaoPreparado {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): transação %q não está preparada", n, r.Tipo, d.Tx)
			}
			pt.Situacao = SituacaoConfirmado

		case tipoAbortado:
			var d dadosTransacao
			if err := json.Unmarshal(r.Dados, &d); err != nil {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): dados ilegíveis: %w", n, r.Tipo, err)
			}
			pt, conhecida := e.participacoes[d.Tx]
			switch {
			case !conhecida:
				// Veto (D38): ABORTADO sem PREPARADO antes. Não há assento a
				// devolver, e a participação nasce abortada e sem itens.
				e.participacoes[d.Tx] = &Participacao{TxID: d.Tx, Situacao: SituacaoAbortado}
			case pt.Situacao == SituacaoPreparado:
				devolverItens(e, pt.Itens)
				pt.Situacao = SituacaoAbortado
			default:
				return 0, fmt.Errorf("dominio: log, registro %d (%s): transação %q está em %s", n, r.Tipo, d.Tx, pt.Situacao)
			}

		case tipoDecisaoConfirmar:
			var d dadosDecisaoConfirmar
			if err := json.Unmarshal(r.Dados, &d); err != nil {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): dados ilegíveis: %w", n, r.Tipo, err)
			}
			if _, repetida := e.reservas[d.Tx]; repetida {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): reserva %q repetida", n, r.Tipo, d.Tx)
			}
			if len(d.Pernas) == 0 {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): reserva %q sem pernas", n, r.Tipo, d.Tx)
			}
			itens := make([]ItemReserva, 0, len(d.Itens))
			for _, item := range d.Itens {
				itens = append(itens, ItemReserva{CaronaID: item.Carona, De: item.De, Ate: item.Ate})
			}
			pernas := make([]Perna, 0, len(d.Pernas))
			for _, p := range d.Pernas {
				pernas = append(pernas, Perna{
					CaronaID:      p.Carona,
					Empresa:       p.Empresa,
					MotoristaNome: p.MotoristaNome,
					De:            p.De,
					Ate:           p.Ate,
					Cidades:       p.Cidades,
					Partida:       p.Partida,
					Chegada:       p.Chegada,
					PrecoCentavos: p.PrecoCentavos,
				})
			}
			// **Não toca em Livres.** Esta é a visão do coordenador; os assentos
			// se movem só pelos registros do dono (PREPARADO, CONFIRMADO,
			// ABORTADO). Quando a empresa é coordenadora e participante da mesma
			// transação, os dois registros estão neste mesmo arquivo, cada um
			// com o seu efeito — descontar aqui também seria contar o mesmo
			// assento duas vezes.
			e.reservas[d.Tx] = &Reserva{
				ID:            d.Tx,
				PassageiroID:  d.Passageiro,
				Itens:         itens,
				Pernas:        pernas,
				Participantes: d.Participantes,
				Ativa:         true,
				CriadaEm:      d.CriadaEm,
			}

		case tipoConcluida:
			var d dadosTransacao
			if err := json.Unmarshal(r.Dados, &d); err != nil {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): dados ilegíveis: %w", n, r.Tipo, err)
			}
			// CONCLUIDA só é gravada para reserva que existe (seção 7.1, passo
			// 9), então reserva desconhecida aqui é log corrompido.
			reserva, conhecida := e.reservas[d.Tx]
			if !conhecida {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): reserva %q desconhecida", n, r.Tipo, d.Tx)
			}
			reserva.Concluida = true

		case tipoPernasCanceladas:
			var d dadosTransacao
			if err := json.Unmarshal(r.Dados, &d); err != nil {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): dados ilegíveis: %w", n, r.Tipo, err)
			}
			// Só é gravado a partir de PREPARADO ou CONFIRMADO (seção 7.2/10.1),
			// então qualquer outra situação aqui é log corrompido.
			pt, conhecida := e.participacoes[d.Tx]
			if !conhecida || (pt.Situacao != SituacaoPreparado && pt.Situacao != SituacaoConfirmado) {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): transação %q não está preparada nem confirmada", n, r.Tipo, d.Tx)
			}
			devolverItens(e, pt.Itens)
			pt.Situacao = SituacaoCancelado

		case tipoCancelamentoDecidido:
			var d dadosCancelamentoDecidido
			if err := json.Unmarshal(r.Dados, &d); err != nil {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): dados ilegíveis: %w", n, r.Tipo, err)
			}
			// Só é gravado para reserva ativa que esta empresa coordena
			// (seções 10.1 e 10.2); reserva desconhecida ou já inativa aqui é
			// log corrompido.
			reserva, conhecida := e.reservas[d.Tx]
			if !conhecida || !reserva.Ativa {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): reserva %q desconhecida ou já cancelada", n, r.Tipo, d.Tx)
			}
			reserva.Ativa = false

		case tipoAvisoRegistrado:
			var d dadosAvisoRegistrado
			if err := json.Unmarshal(r.Dados, &d); err != nil {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): dados ilegíveis: %w", n, r.Tipo, err)
			}
			if _, repetido := e.outbox[d.ID]; repetido {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): aviso %q já existe", n, r.Tipo, d.ID)
			}
			e.outbox[d.ID] = &Aviso{
				ID:       d.ID,
				Destino:  d.Destino,
				Tipo:     TipoAviso(d.Tipo),
				TxID:     d.Tx,
				CaronaID: d.CaronaID,
			}

		case tipoAvisoEntregue:
			var d dadosAvisoEntregue
			if err := json.Unmarshal(r.Dados, &d); err != nil {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): dados ilegíveis: %w", n, r.Tipo, err)
			}
			if _, existe := e.outbox[d.ID]; !existe {
				return 0, fmt.Errorf("dominio: log, registro %d (%s): aviso %q desconhecido", n, r.Tipo, d.ID)
			}
			delete(e.outbox, d.ID)

		default:
			return 0, fmt.Errorf("dominio: log, registro %d: tipo desconhecido %q", n, r.Tipo)
		}
		// O seq do disco é a verdade: o próximo registro continua dele.
		if r.Seq > e.seq {
			e.seq = r.Seq
		}
		if r.Lamport > maiorLamport {
			maiorLamport = r.Lamport
		}
	}
	return maiorLamport, nil
}
