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
func reaplicar(e *Estado, registros [][]byte) error {
	for i, bruto := range registros {
		n := i + 1
		var r registro
		if err := json.Unmarshal(bruto, &r); err != nil {
			return fmt.Errorf("dominio: log, registro %d: envelope ilegível: %w", n, err)
		}
		switch r.Tipo {
		case tipoCaronaPublicada:
			var d dadosCaronaPublicada
			if err := json.Unmarshal(r.Dados, &d); err != nil {
				return fmt.Errorf("dominio: log, registro %d (%s): dados ilegíveis: %w", n, r.Tipo, err)
			}
			if len(d.Rota) < 2 || len(d.Horarios) != len(d.Rota) || len(d.PrecosCentavos) != len(d.Rota)-1 {
				return fmt.Errorf("dominio: log, registro %d (%s): carona %q com rota, horários e preços incoerentes", n, r.Tipo, d.Carona)
			}
			if _, existe := e.caronas[d.Carona]; existe {
				return fmt.Errorf("dominio: log, registro %d (%s): carona %q já existe", n, r.Tipo, d.Carona)
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
				return fmt.Errorf("dominio: log, registro %d (%s): dados ilegíveis: %w", n, r.Tipo, err)
			}
			c, ok := e.caronas[d.Carona]
			if !ok {
				return fmt.Errorf("dominio: log, registro %d (%s): carona %q não existe", n, r.Tipo, d.Carona)
			}
			c.Cancelada = true
		default:
			return fmt.Errorf("dominio: log, registro %d: tipo desconhecido %q", n, r.Tipo)
		}
		// O seq do disco é a verdade: o próximo registro continua dele.
		if r.Seq > e.seq {
			e.seq = r.Seq
		}
	}
	return nil
}
