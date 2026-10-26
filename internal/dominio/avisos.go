package dominio

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
)

// Outbox da D39 (seção 4.7 e 10 do PROJETO_P2.md): a fila de avisos que esta
// empresa já decidiu e precisa entregar, sem que o destino possa recusá-los.
//
// Como em reservas.go e transacoes.go, nada aqui adquire o mutex: estas
// funções recebem o estado já travado por quem chama (D05). A entrega de
// verdade — o HTTP, com retentativa — não mora aqui: é I/O de rede, e a
// regra 2 proíbe fazê-la com o lock preso. O domínio só grava o aviso
// (write-ahead, regra 3) e devolve ao chamador a lista de avisos recém-
// criados; quem os entrega é a camada 5 (internal/transacao), com o lock já
// solto.

// gerarIDAviso sorteia o identificador de um aviso, no formato "avs-91c2a07e"
// (mesmo padrão de GerarIDTransacao). Não leva prefixo de empresa: um aviso
// não é referenciado por ninguém fora desta empresa, diferente de carona e
// de transação.
func gerarIDAviso(e *Estado) (string, error) {
	for tentativas := 0; tentativas < 10; tentativas++ {
		var sufixo [4]byte
		if _, err := rand.Read(sufixo[:]); err != nil {
			return "", fmt.Errorf("%w: %v", ErrGeracaoDeID, err)
		}
		id := "avs-" + hex.EncodeToString(sufixo[:])
		if _, existe := e.outbox[id]; !existe {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: colisão repetida ao gerar ID de aviso", ErrGeracaoDeID)
}

// registrarAviso grava AVISO_REGISTRADO com fsync e só então insere o aviso
// no outbox em memória (regra 3): nenhum aviso existe para quem chama antes
// de estar em disco, e é esse par — gravar, depois aplicar — que o ponto de
// falha aviso-apos-registrar (TF8) prova sobreviver a uma queda.
func registrarAviso(e *Estado, destino string, tipo TipoAviso, txID, caronaID string) (*Aviso, error) {
	id, err := gerarIDAviso(e)
	if err != nil {
		return nil, err
	}
	if err := gravar(e, tipoAvisoRegistrado, dadosAvisoRegistrado{
		ID: id, Destino: destino, Tipo: string(tipo), Tx: txID, CaronaID: caronaID,
	}); err != nil {
		return nil, err
	}

	aviso := &Aviso{ID: id, Destino: destino, Tipo: tipo, TxID: txID, CaronaID: caronaID}
	e.outbox[id] = aviso
	return aviso, nil
}

// avisoEntregue grava AVISO_ENTREGUE e retira o aviso do outbox: o destino
// confirmou o recebimento, e a entrega não precisa mais repetir.
//
// Idempotente por construção: aviso que já saiu do outbox simplesmente não é
// encontrado, e a segunda chamada não tem nada a fazer — é o caso de duas
// tentativas de entrega que tiveram sucesso em paralelo (não deveria
// acontecer, porque cada Aviso só tem uma goroutine de entrega, mas a
// idempotência aqui é de graça e consistente com o resto do 2PC, D28).
func avisoEntregue(e *Estado, avisoID string) error {
	if _, existe := e.outbox[avisoID]; !existe {
		return nil
	}
	if err := gravar(e, tipoAvisoEntregue, dadosAvisoEntregue{ID: avisoID}); err != nil {
		return err
	}
	delete(e.outbox, avisoID)
	return nil
}

// avisosPendentes devolve cópias dos avisos ainda no outbox, ordenados por
// ID. É o que o boot usa para retomar a entrega (seção 9.3: "aviso no
// outbox → retoma o envio") e o que a auditoria usa para a invariante I-P2-3.
func avisosPendentes(e *Estado) []Aviso {
	lista := make([]Aviso, 0, len(e.outbox))
	for _, a := range e.outbox {
		lista = append(lista, a.copia())
	}
	sort.Slice(lista, func(i, j int) bool { return lista[i].ID < lista[j].ID })
	return lista
}

// avisarCaronaCancelada aplica o efeito de POST /avisos/carona-cancelada
// (seção 10.2 do PROJETO_P2.md; seção 4.8 do API_CONSORCIO.md): o dono de uma
// carona cancelada avisa o coordenador de uma reserva atingida, que decide o
// cancelamento do itinerário inteiro e avisa todos os participantes.
//
// Devolve a situação da reserva ("CANCELADA") e os avisos recém-criados, que
// quem chamou entrega fora do lock.
func avisarCaronaCancelada(e *Estado, txID, caronaID, quemAvisou string) (string, []Aviso, error) {
	coordenador, err := EmpresaDoID(txID)
	if err != nil || coordenador != e.empresa {
		return "", nil, fmt.Errorf("%w: %q não é coordenada por %q", ErrNaoCoordenador, txID, e.empresa)
	}

	r, existe := e.reservas[txID]
	if !existe {
		return "", nil, fmt.Errorf("%w: CARONA_CANCELADA de transação desconhecida %q", ErrViolacaoProtocolo, txID)
	}
	if !contem(r.Participantes, quemAvisou) {
		return "", nil, fmt.Errorf("%w: %q não participa de %q", ErrNaoParticipante, quemAvisou, txID)
	}

	if !r.Ativa {
		// Repetição do aviso, ou cancelamento já decidido por outro caminho:
		// mesma resposta, nada a gravar de novo.
		return "CANCELADA", nil, nil
	}

	if err := gravar(e, tipoCancelamentoDecidido, dadosCancelamentoDecidido{Tx: txID}); err != nil {
		return "", nil, err
	}
	r.Ativa = false

	if _, souParticipante := e.participacoes[txID]; souParticipante {
		if _, err := cancelarParticipacao(e, txID); err != nil {
			return "", nil, err
		}
	}

	avisos := make([]Aviso, 0, len(r.Participantes))
	for _, empresa := range r.Participantes {
		if empresa == e.empresa {
			continue
		}
		aviso, err := registrarAviso(e, empresa, AvisoCancelarPernas, txID, "")
		if err != nil {
			return "", nil, err
		}
		avisos = append(avisos, aviso.copia())
	}
	return "CANCELADA", avisos, nil
}
