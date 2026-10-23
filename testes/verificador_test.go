package testes

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"vaijunto/internal/dominio"
)

// Verificador das invariantes do P2 (PROJETO_P2.md, seção 11.1), consultando
// GET /auditoria de cada empresa (seção 11.2).
//
// Por que não serve o verificador do P1 (verificarInvariantes, em
// concorrencia_test.go): ele reconstrói as invariantes pelo próprio protocolo
// dos clientes, e no P2 duas coisas ficaram fora do alcance dele. Assentos em
// espera e participações não aparecem para os clientes; e, principalmente, uma
// parte do estado correto passou a ser **a concordância entre empresas**, que
// só pode ser observada olhando todas ao mesmo tempo.
//
// Os dois convivem: o do P1 continua valendo dentro de cada empresa (I2 a I6), e
// este cobre o que é novo.
//
// Fala HTTP, e não Go, de propósito: o mesmo verificador serve ao teste em
// processo e ao teste de sistema da fase 11, que roda contra contêineres. Se ele
// lesse o dominio.Estado direto, a fase 11 precisaria de um segundo verificador,
// e os dois divergiriam.

// auditoria é a resposta de GET /auditoria (seção 4.9 da API).
//
// Tipos de leitura próprios, e não os da federação: o verificador é um cliente
// como outro qualquer e enxerga só o JSON. Compartilhar os tipos do servidor
// faria o teste passar por construção num campo renomeado nas duas pontas ao
// mesmo tempo.
type auditoria struct {
	Empresa       string             `json:"empresa"`
	Lamport       uint64             `json:"lamport"`
	Caronas       []caronaAuditada   `json:"caronas"`
	Participacoes []participacaoLida `json:"participacoes"`
	Reservas      []reservaAuditada  `json:"reservas"`
	Outbox        []json.RawMessage  `json:"outbox"`
}

type caronaAuditada struct {
	ID          string `json:"id"`
	Assentos    int    `json:"assentos"`
	Cancelada   bool   `json:"cancelada"`
	Livres      []int  `json:"livres"`
	Confirmados []int  `json:"confirmados"`
	EmEspera    []int  `json:"em_espera"`
}

type participacaoLida struct {
	Tx          string `json:"tx"`
	Coordenador string `json:"coordenador"`
	Situacao    string `json:"situacao"`
	Carimbo     struct {
		Lamport uint64 `json:"lamport"`
		Empresa string `json:"empresa"`
	} `json:"carimbo"`
	Passageiro map[string]any `json:"passageiro"`
	Itens      []struct {
		CaronaID string `json:"carona_id"`
		De       int    `json:"de"`
		Ate      int    `json:"ate"`
	} `json:"itens"`
}

type reservaAuditada struct {
	ID            string   `json:"id"`
	Passageiro    string   `json:"passageiro"`
	Ativa         bool     `json:"ativa"`
	Concluida     bool     `json:"concluida"`
	Participantes []string `json:"participantes"`
}

// lerAuditoria busca o retrato de uma empresa.
func lerAuditoria(t *testing.T, urlBase string) auditoria {
	t.Helper()
	a, err := tentarAuditoria(urlBase)
	if err != nil {
		t.Fatalf("auditoria de %s: %v", urlBase, err)
	}
	return a
}

// tentarAuditoria é a versão que devolve erro, para quem espera a quiescência
// num laço e não pode falhar na primeira tentativa.
func tentarAuditoria(urlBase string) (auditoria, error) {
	resp, err := http.Get(urlBase + "/auditoria")
	if err != nil {
		return auditoria{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return auditoria{}, fmt.Errorf("status %d (a empresa subiu sem --auditoria?)", resp.StatusCode)
	}
	corpo, err := io.ReadAll(resp.Body)
	if err != nil {
		return auditoria{}, err
	}
	var a auditoria
	if err := json.Unmarshal(corpo, &a); err != nil {
		return auditoria{}, fmt.Errorf("decodificar %s: %w", corpo, err)
	}
	return a, nil
}

// auditoriasDe consulta todas as empresas do consórcio.
func auditoriasDe(t *testing.T, consorcio map[string]*empresa) map[string]auditoria {
	t.Helper()
	todas := make(map[string]auditoria, len(consorcio))
	for nome, e := range consorcio {
		todas[nome] = lerAuditoria(t, e.urlBase())
	}
	return todas
}

// prazoDeQuiescencia é o teto da espera por quiescência nos testes.
//
// A quiescência da seção 11.1 é definida por prazos da D41 ("o maior
// encadeamento de prazos, com folga"), mas aqui ela é **observada**, e não
// esperada: o laço pergunta à auditoria se a fase 2 terminou. Isso é o que
// mantém o teste rápido no caso comum e estável sob -count=20, em que um sleep
// fixo seria ou lento ou insuficiente.
const prazoDeQuiescencia = 10 * time.Second

// esperarQuiescencia espera até que nenhuma transação esteja em curso: toda
// reserva confirmada está concluída e nenhuma participação está em PREPARADO.
//
// É a condição da seção 11.1 para as invariantes que só valem "após a
// quiescência": enquanto a fase 2 está no ar, uma empresa pode legitimamente
// estar em PREPARADO enquanto outra já está CONFIRMADO, e cobrar I-P2-5 nesse
// instante acusaria uma violação que não existe.
func esperarQuiescencia(t *testing.T, consorcio map[string]*empresa) {
	t.Helper()
	limite := time.Now().Add(prazoDeQuiescencia)
	var pendente string
	for time.Now().Before(limite) {
		pendente = ""
		for nome, e := range consorcio {
			a, err := tentarAuditoria(e.urlBase())
			if err != nil {
				pendente = fmt.Sprintf("%s: %v", nome, err)
				break
			}
			for _, pt := range a.Participacoes {
				if pt.Situacao == string(dominio.SituacaoPreparado) {
					pendente = fmt.Sprintf("%s tem %s em PREPARADO", nome, pt.Tx)
					break
				}
			}
			if pendente != "" {
				break
			}
			for _, r := range a.Reservas {
				if r.Ativa && !r.Concluida {
					pendente = fmt.Sprintf("%s tem a reserva %s sem concluir a fase 2", nome, r.ID)
					break
				}
			}
			if pendente != "" {
				break
			}
		}
		if pendente == "" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("quiescência não alcançada em %s: %s", prazoDeQuiescencia, pendente)
}

// verificarInvariantesP2 confere as cinco invariantes da seção 11.1 sobre os
// retratos das empresas.
//
// A I-P2-3 entrou com a fase 6A. Antes dela, afirmá-la seria afirmar algo
// sobre código que não existia: sem a terminação cooperativa, uma participação
// em PREPARADO cujo coordenador sumiu não tinha caminho de volta nenhum, e a
// invariante vale justamente "após recuperação e quiescência".
func verificarInvariantesP2(t *testing.T, todas map[string]auditoria) {
	t.Helper()
	verificarIP21(t, todas)
	verificarIP22(t, todas)
	verificarIP23(t, todas)
	verificarIP24(t, todas)
	verificarIP25(t, todas)
}

// verificarIP23 confere que nenhum assento ficou preso: nenhuma participação
// em PREPARADO e outbox vazio, em todas as empresas.
//
// É a metade da RD04 que a I-P2-1 não cobre. A I-P2-1 limita por cima — nada
// passa da capacidade —, e esta garante que o que saiu da conta dos livres
// volta: um assento em espera é um assento que ninguém pode comprar e que
// ninguém pagou, e deixá-lo assim para sempre é exatamente o que o enunciado
// proíbe.
//
// Só vale **após recuperação e quiescência**, e por isso toda chamada a
// verificarInvariantesP2 vem depois de esperarQuiescencia: enquanto a fase 2
// está no ar, PREPARADO é um estado legítimo e transitório.
//
// O outbox sai sempre vazio nesta fase, porque ele nasce com os cancelamentos
// da D39, na fase 8. A verificação já está escrita para que a fase 8 não
// precise lembrar de acrescentá-la.
func verificarIP23(t *testing.T, todas map[string]auditoria) {
	t.Helper()
	for nome, a := range todas {
		for _, pt := range a.Participacoes {
			if pt.Situacao == string(dominio.SituacaoPreparado) {
				t.Errorf("I-P2-3: %s ainda tem a participação %s em PREPARADO após a quiescência: o assento continua preso",
					nome, pt.Tx)
			}
		}
		if len(a.Outbox) != 0 {
			t.Errorf("I-P2-3: %s tem %d aviso(s) no outbox após a quiescência", nome, len(a.Outbox))
		}
	}
}

// verificarIP21 é a invariante mestra local: em cada dono, para toda carona e
// trecho, Livres + confirmados + em espera == Assentos, com Livres >= 0.
//
// É a conta que prova as duas metades do enunciado de uma vez: o lado esquerdo
// nunca passa da capacidade (RD02, nenhum assento vendido duas vezes) e nada
// some da conta (RD04, nenhum assento bloqueado para sempre).
func verificarIP21(t *testing.T, todas map[string]auditoria) {
	t.Helper()
	for nome, a := range todas {
		for _, c := range a.Caronas {
			if len(c.Livres) != len(c.Confirmados) || len(c.Livres) != len(c.EmEspera) {
				t.Errorf("I-P2-1 em %s, carona %s: contadores com tamanhos diferentes (%d, %d, %d)",
					nome, c.ID, len(c.Livres), len(c.Confirmados), len(c.EmEspera))
				continue
			}
			for trecho := range c.Livres {
				livres, confirmados, espera := c.Livres[trecho], c.Confirmados[trecho], c.EmEspera[trecho]
				if livres < 0 {
					t.Errorf("I-P2-1 em %s, carona %s, trecho %d: livres = %d, negativo",
						nome, c.ID, trecho, livres)
				}
				if soma := livres + confirmados + espera; soma != c.Assentos {
					t.Errorf("I-P2-1 em %s, carona %s, trecho %d: %d livres + %d confirmados + %d em espera = %d, want %d assentos",
						nome, c.ID, trecho, livres, confirmados, espera, soma, c.Assentos)
				}
			}
		}
	}
}

// verificarIP22 confere a primeira metade da I-P2-2, que vale **sempre**: se
// algum participante está em CONFIRMADO, o coordenador tem a decisão gravada.
//
// É o passo 1 do argumento da seção 7.4 observado de fora. A segunda metade
// (após a quiescência, todos os participantes de uma transação decidida estão
// CONFIRMADO ou CANCELADO) é a I-P2-5, verificada abaixo.
//
// O coordenador de uma transação é a empresa do prefixo do txID (D27), e é por
// isso que esta verificação não precisa perguntar a ninguém quem coordena o quê.
func verificarIP22(t *testing.T, todas map[string]auditoria) {
	t.Helper()
	for nome, a := range todas {
		for _, pt := range a.Participacoes {
			if pt.Situacao != string(dominio.SituacaoConfirmado) {
				continue
			}
			coordenador, err := dominio.EmpresaDoID(pt.Tx)
			if err != nil {
				t.Errorf("I-P2-2 em %s: txID %q sem prefixo de empresa: %v", nome, pt.Tx, err)
				continue
			}
			if coordenador != pt.Coordenador {
				t.Errorf("I-P2-2 em %s, tx %s: coordenador declarado %q difere do prefixo %q",
					nome, pt.Tx, pt.Coordenador, coordenador)
			}
			visao, temRetrato := todas[coordenador]
			if !temRetrato {
				// O coordenador está fora do consórcio observado: não há o que
				// conferir, e não é violação.
				continue
			}
			if reservaDe(visao, pt.Tx) == nil {
				t.Errorf("I-P2-2: %s está CONFIRMADO em %s, mas o coordenador %s não tem a decisão gravada para %s",
					pt.Tx, nome, coordenador, pt.Tx)
			}
		}
	}
}

// verificarIP24 confere a privacidade entre empresas (D32): nenhuma empresa
// guarda dado de outra além de referência opaca e nome de exibição.
//
// Duas afirmações, uma por tipo de vazamento possível:
//
//   - o passageiro de uma participação tem **exatamente** os campos ref e nome.
//     Um campo a mais aqui — a senha, o perfil, o histórico — é vazamento, e é
//     por isso que a verificação é por conjunto de chaves e não por presença;
//   - nenhuma carona de uma empresa leva prefixo de outra. Uma empresa guarda
//     só as caronas dela; das outras, nem cópia da busca federada (D26).
func verificarIP24(t *testing.T, todas map[string]auditoria) {
	t.Helper()
	for nome, a := range todas {
		for _, pt := range a.Participacoes {
			chaves := make([]string, 0, len(pt.Passageiro))
			for chave := range pt.Passageiro {
				chaves = append(chaves, chave)
			}
			sort.Strings(chaves)
			if esperado := []string{"nome", "ref"}; strings.Join(chaves, ",") != strings.Join(esperado, ",") {
				t.Errorf("I-P2-4 em %s, tx %s: passageiro tem os campos %v, want %v",
					nome, pt.Tx, chaves, esperado)
			}
		}
		for _, c := range a.Caronas {
			dona, err := dominio.EmpresaDoID(c.ID)
			if err != nil {
				t.Errorf("I-P2-4 em %s: carona %q sem prefixo de empresa: %v", nome, c.ID, err)
				continue
			}
			if dona != nome {
				t.Errorf("I-P2-4: %s guarda a carona %s, que é da %s", nome, c.ID, dona)
			}
		}
	}
}

// verificarIP25 confere o acordo entre coordenador e donos, que só vale após a
// quiescência: reserva ativa no coordenador se e somente se há participação
// CONFIRMADO em todos os donos.
//
// É a invariante que o enunciado chama de "nenhum itinerário confirmado pela
// metade", e a razão de ela existir é que, no P2, uma parte do estado correto é
// a concordância entre empresas — nenhuma delas, sozinha, pode observá-la.
func verificarIP25(t *testing.T, todas map[string]auditoria) {
	t.Helper()
	for nome, a := range todas {
		for _, r := range a.Reservas {
			if !r.Ativa {
				continue
			}
			for _, participante := range r.Participantes {
				visao, temRetrato := todas[participante]
				if !temRetrato {
					continue
				}
				pt := participacaoDe(visao, r.ID)
				if pt == nil {
					t.Errorf("I-P2-5: %s tem a reserva %s ativa, mas %s não conhece a transação",
						nome, r.ID, participante)
					continue
				}
				if pt.Situacao != string(dominio.SituacaoConfirmado) && pt.Situacao != string(dominio.SituacaoCancelado) {
					t.Errorf("I-P2-5: %s tem a reserva %s ativa, mas em %s a participação está em %s",
						nome, r.ID, participante, pt.Situacao)
				}
			}
		}
	}
}

func reservaDe(a auditoria, txID string) *reservaAuditada {
	for i := range a.Reservas {
		if a.Reservas[i].ID == txID {
			return &a.Reservas[i]
		}
	}
	return nil
}

func participacaoDe(a auditoria, txID string) *participacaoLida {
	for i := range a.Participacoes {
		if a.Participacoes[i].Tx == txID {
			return &a.Participacoes[i]
		}
	}
	return nil
}
