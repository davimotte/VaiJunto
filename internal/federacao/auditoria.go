package federacao

import (
	"vaijunto/internal/dominio"
)

// Tipos de fio de GET /auditoria (seção 4.9 da API).
//
// O recurso devolve o estado completo da empresa para o verificador de
// invariantes da seção 11.2: caronas com os contadores por trecho,
// participações, reservas e outbox. É somente leitura, não participa de nenhuma
// decisão e só existe com a flag --auditoria.
//
// Por que ele precisa existir: no P1 o verificador reconstruía as invariantes
// pelo próprio protocolo dos clientes. No P2 isso não basta — assentos em
// espera e participações não aparecem para os clientes, e uma parte do estado
// correto passou a ser **a concordância entre empresas**, que só se observa
// olhando todas.
//
// Tipos de fio próprios, e não os do domínio, pelo mesmo motivo de todo o resto
// desta borda: o JSON é contrato com o verificador e tem os nomes da seção 4.9,
// enquanto os tipos do domínio seguem as conveniências do domínio. Serializar o
// domínio direto amarraria o contrato a um nome de campo interno.
type auditoriaDeFioResposta struct {
	Empresa       string                 `json:"empresa"`
	Lamport       uint64                 `json:"lamport"`
	Caronas       []caronaAuditadaDeFio  `json:"caronas"`
	Participacoes []participacaoDeFio    `json:"participacoes"`
	Reservas      []reservaAuditadaDeFio `json:"reservas"`
	Outbox        []avisoDeFio           `json:"outbox"`
}

// caronaAuditadaDeFio são os contadores de uma carona. A I-P2-1 é verificada
// linha a linha: livres + confirmados + em_espera == assentos.
type caronaAuditadaDeFio struct {
	ID          string `json:"id"`
	Assentos    int    `json:"assentos"`
	Cancelada   bool   `json:"cancelada"`
	Livres      []int  `json:"livres"`
	Confirmados []int  `json:"confirmados"`
	EmEspera    []int  `json:"em_espera"`
}

// participacaoDeFio é a visão de dono de uma transação.
//
// O campo passageiro é a própria verificação da I-P2-4: só referência opaca e
// nome de exibição, e nada mais, de um passageiro de outra empresa (D32).
type participacaoDeFio struct {
	Tx          string           `json:"tx"`
	Coordenador string           `json:"coordenador"`
	Situacao    string           `json:"situacao"`
	Carimbo     carimboDeFioLido `json:"carimbo"`
	Passageiro  passageiroLido   `json:"passageiro"`
	Itens       []itemAuditado   `json:"itens"`
}

// reservaAuditadaDeFio é a visão de coordenador: o que esta empresa decidiu
// confirmar para os seus próprios passageiros.
type reservaAuditadaDeFio struct {
	ID            string   `json:"id"`
	Passageiro    string   `json:"passageiro"`
	Ativa         bool     `json:"ativa"`
	Concluida     bool     `json:"concluida"`
	Participantes []string `json:"participantes"`
}

// avisoDeFio é uma entrada do outbox (seção 4.7, D39): um aviso de
// cancelamento ainda não confirmado pelo destino. A lista fica vazia fora da
// janela de propagação da seção 10.3 — é o que a I-P2-3 verifica após a
// quiescência.
type avisoDeFio struct {
	ID      string `json:"id"`
	Destino string `json:"destino"`
	Tipo    string `json:"tipo"`
	Tx      string `json:"tx"`
}

type carimboDeFioLido struct {
	Lamport uint64 `json:"lamport"`
	Empresa string `json:"empresa"`
}

type passageiroLido struct {
	Ref  string `json:"ref"`
	Nome string `json:"nome"`
}

type itemAuditado struct {
	CaronaID string `json:"carona_id"`
	De       int    `json:"de"`
	Ate      int    `json:"ate"`
}

// auditoriaDeFio converte o retrato do domínio nos tipos da seção 4.9.
//
// O lamport vem do carimbo do próprio retrato e não de uma segunda leitura do
// relógio: a auditoria é um instante só, e misturar nela um valor lido depois
// daria ao verificador um retrato que nunca existiu.
func auditoriaDeFio(a dominio.Auditoria) auditoriaDeFioResposta {
	resposta := auditoriaDeFioResposta{
		Empresa:       a.Empresa,
		Lamport:       a.Lamport,
		Caronas:       make([]caronaAuditadaDeFio, 0, len(a.Caronas)),
		Participacoes: make([]participacaoDeFio, 0, len(a.Participacoes)),
		Reservas:      make([]reservaAuditadaDeFio, 0, len(a.Reservas)),
		Outbox:        make([]avisoDeFio, 0, len(a.Outbox)),
	}

	for _, c := range a.Caronas {
		resposta.Caronas = append(resposta.Caronas, caronaAuditadaDeFio{
			ID:          c.ID,
			Assentos:    c.Assentos,
			Cancelada:   c.Cancelada,
			Livres:      c.Livres,
			Confirmados: c.Confirmados,
			EmEspera:    c.EmEspera,
		})
	}

	for _, pt := range a.Participacoes {
		itens := make([]itemAuditado, 0, len(pt.Itens))
		for _, item := range pt.Itens {
			itens = append(itens, itemAuditado{CaronaID: item.CaronaID, De: item.De, Ate: item.Ate})
		}
		resposta.Participacoes = append(resposta.Participacoes, participacaoDeFio{
			Tx:          pt.TxID,
			Coordenador: pt.Coordenador,
			Situacao:    string(pt.Situacao),
			Carimbo:     carimboDeFioLido{Lamport: pt.Carimbo.Lamport, Empresa: pt.Carimbo.Empresa},
			Passageiro:  passageiroLido{Ref: pt.Passageiro.Ref, Nome: pt.Passageiro.Nome},
			Itens:       itens,
		})
	}

	for _, r := range a.Reservas {
		resposta.Reservas = append(resposta.Reservas, reservaAuditadaDeFio{
			ID:            r.ID,
			Passageiro:    r.PassageiroID,
			Ativa:         r.Ativa,
			Concluida:     r.Concluida,
			Participantes: r.Participantes,
		})
	}

	for _, av := range a.Outbox {
		resposta.Outbox = append(resposta.Outbox, avisoDeFio{
			ID:      av.ID,
			Destino: av.Destino,
			Tipo:    string(av.Tipo),
			Tx:      av.TxID,
		})
	}

	return resposta
}
