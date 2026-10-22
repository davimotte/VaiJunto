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

// dadosCaronaCancelada: quando existirem transações (Fase 8), o registro
// também lista as que o cancelamento atinge (seção 4.8).
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
