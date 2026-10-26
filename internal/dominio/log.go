package dominio

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Log é o destino durável dos registros (D22). A interface é definida aqui, e
// recebe bytes já serializados, para que o domínio não conheça arquivo nenhum
// (regra 8): internal/persistencia a implementa sem importar o domínio.
//
// Anexar só retorna nil depois que o registro está em disco (fsync). É esse
// retorno que autoriza o domínio a aplicar a mudança e o servidor a responder.
type Log interface {
	Anexar(registro []byte) error
}

// LogMemoria é o Log dos testes e do servidor sem --log. Não sobrevive a
// reinício.
type LogMemoria struct {
	mu        sync.Mutex
	registros [][]byte
}

// NovaLogMemoria cria um log vazio.
func NovaLogMemoria() *LogMemoria { return &LogMemoria{} }

// Anexar guarda uma cópia: o chamador pode reutilizar o buffer.
func (l *LogMemoria) Anexar(registro []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.registros = append(l.registros, append([]byte(nil), registro...))
	return nil
}

// Registros devolve cópias dos registros gravados, na ordem.
func (l *LogMemoria) Registros() [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	saida := make([][]byte, len(l.registros))
	for i, r := range l.registros {
		saida[i] = append([]byte(nil), r...)
	}
	return saida
}

// Tipos de registro implementados até esta fase (seção 4.8).
const (
	tipoCaronaPublicada = "CARONA_PUBLICADA"
	tipoCaronaCancelada = "CARONA_CANCELADA"
	tipoPreparado       = "PREPARADO"
	tipoConfirmado      = "CONFIRMADO"
	tipoAbortado        = "ABORTADO"
	// Os dois do coordenador (seção 4.8). O abort presumido (D23) explica por
	// que não existe um "DECISAO_ABORTAR": transação sem decisão gravada **é**
	// uma transação abortada, então gravar o abort seria gravar o padrão.
	tipoDecisaoConfirmar = "DECISAO_CONFIRMAR"
	tipoConcluida        = "CONCLUIDA"

	// Os quatro da Fase 8 (D39, seção 4.8): cancelamento e outbox.
	tipoPernasCanceladas     = "PERNAS_CANCELADAS"
	tipoCancelamentoDecidido = "CANCELAMENTO_DECIDIDO"
	tipoAvisoRegistrado      = "AVISO_REGISTRADO"
	tipoAvisoEntregue        = "AVISO_ENTREGUE"
)

// registro é o envelope de cada linha do log (seção 4.8). Lamport é o L da
// empresa no momento da gravação (D24).
type registro struct {
	Seq     uint64          `json:"seq"`
	Lamport uint64          `json:"lamport"`
	Tipo    string          `json:"tipo"`
	Dados   json.RawMessage `json:"dados"`
}

// dadosCaronaPublicada guarda a carona inteira, com o ID já gerado: o replay
// usa este ID e nunca gera outro (D27, D37). Os horários saem em RFC3339 com
// o deslocamento original, então voltam como o mesmo instante.
type dadosCaronaPublicada struct {
	Carona         string      `json:"carona"`
	Motorista      string      `json:"motorista"`
	Rota           []string    `json:"rota"`
	Horarios       []time.Time `json:"horarios"`
	Assentos       int         `json:"assentos"`
	PrecosCentavos []int       `json:"precos_centavos"`
}

// dadosCaronaCancelada guarda só o ID da carona. A cascata sobre as
// transações atingidas (D29) não entra aqui: cada uma gera o seu próprio
// PERNAS_CANCELADAS e, quando esta empresa coordena, o seu próprio
// CANCELAMENTO_DECIDIDO — um registro por transação, nunca uma lista, para
// que o replay repita sempre a mesma operação (seção 4.8).
type dadosCaronaCancelada struct {
	Carona string `json:"carona"`
}

// dadosPreparado é o registro que o participante grava antes de votar SIM
// (seção 4.8). Guarda a participação inteira, porque é dela que o replay
// reconstrói tanto a participação quanto os assentos descontados.
//
// PreparadaEm não entra: é instante local que arma o prazo de dúvida e, após um
// reboot, o prazo recomeça (seção 4.4).
//
// Os nomes de campo são os do exemplo da seção 4.8, e por isso o item usa
// "carona" onde a API usa "carona_id": log e API são contratos diferentes, com
// donos diferentes, e copiar um no outro por semelhança é como as divergências
// começam.
type dadosPreparado struct {
	Tx            string        `json:"tx"`
	Coordenador   string        `json:"coordenador"`
	Participantes []string      `json:"participantes"`
	Itens         []itemLog     `json:"itens"`
	Carimbo       carimboLog    `json:"carimbo"`
	Passageiro    passageiroLog `json:"passageiro"`
}

type itemLog struct {
	Carona string `json:"carona"`
	De     int    `json:"de"`
	Ate    int    `json:"ate"`
}

type carimboLog struct {
	Lamport uint64 `json:"lamport"`
	Empresa string `json:"empresa"`
}

type passageiroLog struct {
	Ref  string `json:"ref"`
	Nome string `json:"nome"`
}

// dadosTransacao é o corpo de CONFIRMADO e de ABORTADO: só o txID.
//
// Os dois não precisam de mais nada porque a participação já está no log, pelo
// PREPARADO — exceto o caso do veto (D38), em que o ABORTADO chega sem
// PREPARADO nenhum e o replay cria a participação abortada sem itens, que é
// exatamente o que um veto é (seção 4.4).
type dadosTransacao struct {
	Tx string `json:"tx"`
}

// dadosDecisaoConfirmar é o corpo de DECISAO_CONFIRMAR, o ponto de commit do
// coordenador (seção 4.8).
//
// Guarda a reserva inteira, **com as pernas**, e não só os índices. É a D42:
// as caronas das pernas remotas estão em outros servidores, então o replay não
// tem contra o que resolver {carona, De, Ate} — se as pernas não estivessem
// aqui, uma reserva com trecho de outra empresa voltaria ilegível do boot, e
// listar as reservas passaria a depender de todos os pares estarem no ar
// (RD05). CriadaEm também vai ao log porque é o instante da decisão, e o replay
// não pode inventá-lo.
type dadosDecisaoConfirmar struct {
	Tx            string     `json:"tx"`
	Passageiro    string     `json:"passageiro"`
	Participantes []string   `json:"participantes"`
	Itens         []itemLog  `json:"itens"`
	Pernas        []pernaLog `json:"pernas"`
	CriadaEm      time.Time  `json:"criada_em"`
}

// pernaLog é a Perna autoritativa como ela vai ao log (seção 3.3 da API).
type pernaLog struct {
	Carona        string    `json:"carona"`
	Empresa       string    `json:"empresa"`
	MotoristaNome string    `json:"motorista_nome"`
	De            int       `json:"de"`
	Ate           int       `json:"ate"`
	Cidades       []string  `json:"cidades"`
	Partida       time.Time `json:"partida"`
	Chegada       time.Time `json:"chegada"`
	PrecoCentavos int       `json:"preco_centavos"`
}

// dadosCancelamentoDecidido é o corpo de CANCELAMENTO_DECIDIDO (seção 10.1 e
// 10.2): o coordenador desativou a reserva Tx. Um registro por reserva, nunca
// uma lista — a cascata do motorista (10.2) grava um destes por reserva
// atingida que esta empresa coordene, e não um registro com vários txIDs:
// assim o replay repete sempre a mesma operação, sem um caso especial.
type dadosCancelamentoDecidido struct {
	Tx string `json:"tx"`
}

// dadosAvisoRegistrado é o corpo de AVISO_REGISTRADO (D39, seção 4.7): o
// aviso entra no outbox antes de qualquer tentativa de envio.
type dadosAvisoRegistrado struct {
	ID       string `json:"id"`
	Destino  string `json:"destino"`
	Tipo     string `json:"tipo"`
	Tx       string `json:"tx"`
	CaronaID string `json:"carona_id,omitempty"`
}

// dadosAvisoEntregue é o corpo de AVISO_ENTREGUE: o destino confirmou o
// recebimento, e o aviso sai do outbox.
type dadosAvisoEntregue struct {
	ID string `json:"id"`
}

func dadosCaronaPublicadaDe(c *Carona) dadosCaronaPublicada {
	return dadosCaronaPublicada{
		Carona:         c.ID,
		Motorista:      c.MotoristaID,
		Rota:           c.Rota,
		Horarios:       c.Horarios,
		Assentos:       c.Assentos,
		PrecosCentavos: c.PrecoTrecho,
	}
}

func dadosPreparadoDe(p PedidoPreparacao) dadosPreparado {
	itens := make([]itemLog, 0, len(p.Itens))
	for _, item := range p.Itens {
		itens = append(itens, itemLog{Carona: item.CaronaID, De: item.De, Ate: item.Ate})
	}
	return dadosPreparado{
		Tx:            p.TxID,
		Coordenador:   p.Coordenador,
		Participantes: p.Participantes,
		Itens:         itens,
		Carimbo:       carimboLog{Lamport: p.Carimbo.Lamport, Empresa: p.Carimbo.Empresa},
		Passageiro:    passageiroLog{Ref: p.Passageiro.Ref, Nome: p.Passageiro.Nome},
	}
}

func dadosDecisaoConfirmarDe(r *Reserva) dadosDecisaoConfirmar {
	itens := make([]itemLog, 0, len(r.Itens))
	for _, item := range r.Itens {
		itens = append(itens, itemLog{Carona: item.CaronaID, De: item.De, Ate: item.Ate})
	}
	pernas := make([]pernaLog, 0, len(r.Pernas))
	for _, p := range r.Pernas {
		pernas = append(pernas, pernaLog{
			Carona:        p.CaronaID,
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
	return dadosDecisaoConfirmar{
		Tx:            r.ID,
		Passageiro:    r.PassageiroID,
		Participantes: r.Participantes,
		Itens:         itens,
		Pernas:        pernas,
		CriadaEm:      r.CriadaEm,
	}
}

// gravar serializa o envelope e o entrega ao log. Roda com e.mu já travado
// (D05), por isso não tem lock próprio: é a mesma seção crítica que depois
// aplica a mudança.
//
// O seq só avança se o registro foi gravado, para a numeração do disco não
// ter buracos causados por falhas.
func gravar(e *Estado, tipo string, dados any) error {
	bruto, err := json.Marshal(dados)
	if err != nil {
		return fmt.Errorf("%w: serializar %s: %v", ErrFalhaNoLog, tipo, err)
	}
	// O relógio é só lido (seção 8.1): gravar em disco é evento local da
	// empresa, não troca de mensagem, e por isso não avança L. Quem o avança é
	// a borda REST, num ponto único (seção 5.7).
	linha, err := json.Marshal(registro{Seq: e.seq + 1, Lamport: e.lamport(), Tipo: tipo, Dados: bruto})
	if err != nil {
		return fmt.Errorf("%w: serializar envelope de %s: %v", ErrFalhaNoLog, tipo, err)
	}
	if err := e.log.Anexar(linha); err != nil {
		return fmt.Errorf("%w: %v", ErrFalhaNoLog, err)
	}
	e.seq++
	return nil
}
