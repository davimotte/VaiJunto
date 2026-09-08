package dominio

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Este arquivo é a camada 5 do PROJETO.md (seção 5.1): regras de negócio
// sobre o estado **já travado**.
//
// Todas as funções aqui são propositalmente não exportadas e nenhuma delas
// chama e.mu.Lock(). Quem trava é a camada 6, em estado.go, único ponto do
// programa que toca o mutex (D05). Deixá-las inacessíveis de fora do pacote
// transforma a regra em algo que o compilador garante: não existe caminho
// pelo qual um chamador externo execute uma regra de domínio sem passar pela
// camada que adquire o lock, e por isso não há como reentrar no mesmo mutex.

// PassageiroConfirmado é um passageiro ocupando um trecho de uma carona,
// como o motorista o vê em DETALHAR_CARONA (RF04).
type PassageiroConfirmado struct {
	ReservaID string
	Usuario   string
	Nome      string
}

// copia devolve uma cópia profunda da carona.
//
// É o que impede que um ponteiro para dentro do estado escape da seção
// crítica: sem a cópia, a camada de roteamento leria Livres enquanto outra
// goroutine o escreve com o lock na mão, o que é exatamente a corrida que o
// mutex existe para evitar — e o -race apontaria.
func (c *Carona) copia() Carona {
	copiada := *c
	copiada.Rota = append([]string(nil), c.Rota...)
	copiada.Horarios = append([]time.Time(nil), c.Horarios...)
	copiada.PrecoTrecho = append([]int(nil), c.PrecoTrecho...)
	copiada.Livres = append([]int(nil), c.Livres...)
	return copiada
}

// autenticar confere usuário e senha da carga inicial (D12).
//
// A mesma resposta para usuário inexistente e senha errada é deliberada: dizer
// qual dos dois falhou entregaria a existência da conta a quem perguntasse.
// Devolve uma cópia do usuário, e não o ponteiro do estado, pelo motivo de
// copia acima.
func autenticar(e *Estado, usuario, senha string) (Usuario, error) {
	u, ok := e.usuarios[usuario]
	if !ok || u.Senha != senha {
		return Usuario{}, fmt.Errorf("%w: %q", ErrCredenciaisInvalidas, usuario)
	}
	return *u, nil
}

// gerarIDCarona sorteia um identificador opaco no formato "car-3f2a"
// (PROTOCOL.md, seção 3).
//
// Roda dentro da seção crítica, então checar colisão contra o mapa é grátis e
// remove qualquer dúvida sobre duas publicações simultâneas gerarem o mesmo
// id. Um contador sequencial seria mais simples, mas colidiria com os ids
// fixos de dados/caronas.json ("car-1" a "car-7") depois de um reinício.
func gerarIDCarona(e *Estado) (string, error) {
	var sufixo [2]byte
	for tentativa := 0; tentativa < 10; tentativa++ {
		if _, err := rand.Read(sufixo[:]); err != nil {
			return "", fmt.Errorf("%w: %v", ErrGeracaoDeID, err)
		}
		id := "car-" + hex.EncodeToString(sufixo[:])
		if _, existe := e.caronas[id]; !existe {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: espaço de identificadores esgotado", ErrGeracaoDeID)
}

// publicarCarona valida os dados de PUBLICAR_CARONA (PROTOCOL.md, seção 5.4)
// e insere a carona no estado.
//
// Como na reserva (D07), toda a validação vem antes de qualquer escrita: a
// carona só entra no mapa depois que nenhuma regra pode mais falhar, então
// não existe estado parcial a desfazer e não é preciso rollback.
//
// agora é recebido de fora em vez de lido de time.Now() aqui para que a regra
// "partida no futuro" seja testável sem depender do relógio da máquina.
func publicarCarona(e *Estado, motoristaID, origem, destino string, partida time.Time, assentos int, precos []int, agora time.Time) (Carona, error) {
	rota, horarios, err := DerivarRotaEHorarios(origem, destino, partida)
	if err != nil {
		return Carona{}, err
	}

	trechos := len(rota) - 1
	if len(precos) != trechos {
		return Carona{}, fmt.Errorf("%w: %d preços para %d trechos", ErrRotaInvalida, len(precos), trechos)
	}
	for i, preco := range precos {
		if preco < 0 {
			return Carona{}, fmt.Errorf("%w: trecho %d com preço %d", ErrPrecoInvalido, i, preco)
		}
	}
	if assentos < 1 {
		return Carona{}, fmt.Errorf("%w: %d", ErrAssentosInvalidos, assentos)
	}
	// Comparação por After, nunca por ==: instantes iguais em fusos
	// diferentes são o mesmo ponto no tempo (D11).
	if !partida.After(agora) {
		return Carona{}, fmt.Errorf("%w: %s não é posterior a %s", ErrPartidaInvalida, partida, agora)
	}

	id, err := gerarIDCarona(e)
	if err != nil {
		return Carona{}, err
	}

	livres := make([]int, trechos)
	for i := range livres {
		livres[i] = assentos
	}

	// Primeira e única escrita. As fatias de preço vêm de fora (foram
	// decodificadas do JSON do cliente), então entram copiadas: o estado não
	// pode compartilhar memória com quem o alimentou.
	carona := &Carona{
		ID:          id,
		MotoristaID: motoristaID,
		Rota:        rota,
		Horarios:    horarios,
		Assentos:    assentos,
		PrecoTrecho: append([]int(nil), precos...),
		Livres:      livres,
		Cancelada:   false,
	}
	e.caronas[id] = carona

	return carona.copia(), nil
}

// caronasDoMotorista devolve as caronas publicadas por motoristaID
// (PROTOCOL.md, seção 5.5), canceladas incluídas apenas quando pedido.
//
// A ordenação é explícita porque a iteração de um mapa em Go é aleatória por
// construção: sem ela, o mesmo estado geraria listas em ordens diferentes a
// cada chamada, o que confunde na demonstração e torna o teste instável.
func caronasDoMotorista(e *Estado, motoristaID string, incluirCanceladas bool) []Carona {
	lista := make([]Carona, 0, len(e.caronas))
	for _, c := range e.caronas {
		if c.MotoristaID != motoristaID {
			continue
		}
		if c.Cancelada && !incluirCanceladas {
			continue
		}
		lista = append(lista, c.copia())
	}

	sort.Slice(lista, func(i, j int) bool {
		if !lista[i].Horarios[0].Equal(lista[j].Horarios[0]) {
			return lista[i].Horarios[0].Before(lista[j].Horarios[0])
		}
		return lista[i].ID < lista[j].ID
	})
	return lista
}

// detalharCarona devolve a carona e, para cada trecho, os passageiros
// confirmados nele (PROTOCOL.md, seção 5.6; RF04).
//
// A checagem de dono é do domínio, e não do roteador: "esta carona é do joão"
// é uma propriedade do estado, não do protocolo. O que o roteador decide é
// apenas que perfil pode chamar a operação.
func detalharCarona(e *Estado, caronaID, motoristaID string) (Carona, [][]PassageiroConfirmado, error) {
	c, ok := e.caronas[caronaID]
	if !ok {
		return Carona{}, nil, fmt.Errorf("%w: %q", ErrCaronaNaoEncontrada, caronaID)
	}
	if c.MotoristaID != motoristaID {
		return Carona{}, nil, fmt.Errorf("%w: carona %q", ErrNaoEDono, caronaID)
	}

	trechos := len(c.Rota) - 1
	porTrecho := make([][]PassageiroConfirmado, trechos)
	for i := range porTrecho {
		porTrecho[i] = []PassageiroConfirmado{}
	}

	// Varredura linear sobre as reservas em vez de um índice carona → reservas
	// mantido junto com o estado: DETALHAR_CARONA é operação de motorista,
	// rara e fora do caminho crítico da reserva, e um índice a mais seria
	// mais uma estrutura a manter consistente dentro da seção crítica.
	for _, r := range e.reservas {
		if !r.Ativa {
			continue
		}
		for _, item := range r.Itens {
			if item.CaronaID != caronaID {
				continue
			}
			passageiro := PassageiroConfirmado{ReservaID: r.ID, Usuario: r.PassageiroID}
			if u, ok := e.usuarios[r.PassageiroID]; ok {
				passageiro.Nome = u.Nome
			}
			for t := item.De; t < item.Ate && t < trechos; t++ {
				porTrecho[t] = append(porTrecho[t], passageiro)
			}
		}
	}

	for t := range porTrecho {
		sort.Slice(porTrecho[t], func(i, j int) bool {
			return porTrecho[t][i].ReservaID < porTrecho[t][j].ReservaID
		})
	}

	return c.copia(), porTrecho, nil
}

// Regras temporais do PROJETO.md (D13). São constantes únicas, usadas tanto
// pela busca quanto pela reserva: se a busca e a reserva usassem valores
// diferentes, a busca ofereceria itinerários que a reserva recusa, e o
// passageiro veria uma opção sumir entre a consulta e a confirmação.
const (
	// MARGEM_BALDEACAO é a folga **mínima** entre a chegada de um trecho e a
	// partida do seguinte.
	MARGEM_BALDEACAO = 30 * time.Minute

	// ESPERA_MAXIMA_BALDEACAO é a folga **máxima** da mesma conexão.
	//
	// Existe porque o filtro de data vale só para a primeira perna, o que
	// permite a baldeação atravessar a meia-noite (PROJETO.md, seção 6). Sem
	// um teto, esse mesmo afrouxamento deixa entrar caronas de dias
	// seguintes como perna intermediária: no cenário da seção 9.2, car-1
	// chega a Jequié em 15/09 às 11:00 e car-6 parte de lá em 16/09 ao meio-
	// dia, encadeamento válido no espaço e no tempo que produziria um
	// "itinerário" com 25 h de espera. Doze horas separa a conexão noturna
	// legítima da espera de um dia inteiro.
	ESPERA_MAXIMA_BALDEACAO = 12 * time.Hour

	// MAXIMO_ITINERARIOS limita a resposta da busca (PROTOCOL.md, seção 5.8).
	MAXIMO_ITINERARIOS = 20
)

// pernaCandidata é uma perna gerada no passo 2 da busca, antes de entrar em
// qualquer itinerário. Guarda os índices de cidade **no corredor** (e não na
// rota da carona) porque é por eles que a busca em profundidade encadeia as
// pernas: duas caronas diferentes chegam à mesma cidade em posições
// diferentes das suas rotas.
type pernaCandidata struct {
	perna                       PernaItinerario
	cidadeOrigem, cidadeDestino int
}

// sentidoDaCarona devolve +1 quando a carona percorre o corredor no sentido
// crescente de índice e -1 no decrescente. A rota é sempre derivada do
// corredor (D09), então basta olhar as duas primeiras cidades.
func sentidoDaCarona(c *Carona) int {
	primeira, _ := IndiceCidade(c.Rota[0])
	segunda, _ := IndiceCidade(c.Rota[1])
	if segunda > primeira {
		return 1
	}
	return -1
}

// gerarPernas executa o passo 2 do algoritmo de busca (PROJETO.md, seção 6):
// todo segmento contíguo de toda carona que sirva ao trajeto pedido.
//
// Três podas, nesta ordem: sentido oposto ao da busca, cidade fora do
// intervalo [menor, maior] entre origem e destino, e trecho sem assento
// livre. A terceira é a única que lê estado mutável — e ela é só uma poda:
// a busca não reserva nem promete nada, e a reserva refaz a verificação
// inteira sob o lock (D07).
//
// Com 4 cidades no corredor, cada carona gera no máximo 6 pernas.
func gerarPernas(e *Estado, sentido, menor, maior int) map[int][]pernaCandidata {
	pernas := make(map[int][]pernaCandidata)

	for _, c := range e.caronas {
		if c.Cancelada || len(c.Rota) < 2 {
			continue
		}
		if sentidoDaCarona(c) != sentido {
			continue
		}

		motorista := c.MotoristaID
		if u, ok := e.usuarios[c.MotoristaID]; ok {
			motorista = u.Nome
		}

		for a := 0; a < len(c.Rota)-1; a++ {
			cidadeA, _ := IndiceCidade(c.Rota[a])
			if cidadeA < menor || cidadeA > maior {
				continue
			}
			// O preço acumula ao longo de b: percorrer os trechos de novo a
			// cada par (a,b) recalcularia a mesma soma várias vezes.
			preco := 0
			for b := a + 1; b < len(c.Rota); b++ {
				if c.Livres[b-1] < 1 {
					// Sem assento no trecho b-1, nenhum b maior serve:
					// todos os segmentos seguintes o contêm.
					break
				}
				preco += c.PrecoTrecho[b-1]

				cidadeB, _ := IndiceCidade(c.Rota[b])
				if cidadeB < menor || cidadeB > maior {
					continue
				}
				pernas[cidadeA] = append(pernas[cidadeA], pernaCandidata{
					perna: PernaItinerario{
						CaronaID:      c.ID,
						Motorista:     motorista,
						De:            a,
						Ate:           b,
						Origem:        c.Rota[a],
						Destino:       c.Rota[b],
						Partida:       c.Horarios[a],
						Chegada:       c.Horarios[b],
						PrecoCentavos: preco,
					},
					cidadeOrigem:  cidadeA,
					cidadeDestino: cidadeB,
				})
			}
		}
	}

	// A iteração de um mapa em Go é aleatória por construção, então sem esta
	// ordenação a busca visitaria as pernas em ordem diferente a cada
	// chamada. Não muda o conjunto de itinerários encontrados, mas fixa a
	// ordem em que eles são gerados, que é o que torna o resultado
	// reproduzível na demonstração e o teste estável.
	for cidade := range pernas {
		lista := pernas[cidade]
		sort.Slice(lista, func(i, j int) bool {
			if lista[i].perna.CaronaID != lista[j].perna.CaronaID {
				return lista[i].perna.CaronaID < lista[j].perna.CaronaID
			}
			if lista[i].perna.De != lista[j].perna.De {
				return lista[i].perna.De < lista[j].perna.De
			}
			return lista[i].perna.Ate < lista[j].perna.Ate
		})
	}
	return pernas
}

// mesmaData responde se instante cai no dia civil de data, lido no fuso de
// data.
//
// O fuso vem de quem chamou, e não de time.Local: "15/09" é uma pergunta que
// só tem sentido dentro de um fuso, e deixá-lo implícito faria a mesma busca
// devolver listas diferentes conforme a configuração da máquina — dentro de
// um contêiner, inclusive (PROJETO.md, seção 10.1).
func mesmaData(instante, data time.Time) bool {
	a1, m1, d1 := instante.In(data.Location()).Date()
	a2, m2, d2 := data.Date()
	return a1 == a2 && m1 == m2 && d1 == d2
}

// buscarItinerarios implementa o algoritmo da seção 6 do PROJETO.md e
// responde a BUSCAR_ITINERARIOS (PROTOCOL.md, seção 5.8).
//
// Roda sob o mesmo mutex de todas as outras operações (D04) e **não escreve
// nada**: lê Livres apenas para podar pernas inviáveis. É essa escolha que
// dispensa reserva em duas fases (D07) — nenhum assento fica bloqueado por
// uma consulta que o passageiro nunca confirma. O preço da escolha é que o
// resultado pode estar desatualizado quando ele confirmar, e por isso a
// reserva revalida tudo.
func buscarItinerarios(e *Estado, origem, destino string, data time.Time) ([]Itinerario, error) {
	// Passo 1 — normalizar.
	i, ok := IndiceCidade(origem)
	if !ok {
		return nil, fmt.Errorf("%w: origem %q", ErrCidadeDesconhecida, origem)
	}
	j, ok := IndiceCidade(destino)
	if !ok {
		return nil, fmt.Errorf("%w: destino %q", ErrCidadeDesconhecida, destino)
	}
	if i == j {
		return nil, fmt.Errorf("%w: origem e destino são a mesma cidade %q", ErrRotaInvalida, origem)
	}

	sentido := 1
	menor, maior := i, j
	if j < i {
		sentido = -1
		menor, maior = j, i
	}

	// Passo 2 — gerar pernas candidatas.
	pernas := gerarPernas(e, sentido, menor, maior)

	// Passo 3 — busca em profundidade. O estado é a cidade atual e o instante
	// em que o passageiro fica livre nela.
	var encontrados []Itinerario
	var acumulado []PernaItinerario
	caronasUsadas := make(map[string]bool)

	var expandir func(cidadeAtual int, livreEm time.Time)
	expandir = func(cidadeAtual int, livreEm time.Time) {
		if cidadeAtual == j {
			encontrados = append(encontrados, montarItinerario(acumulado))
			return
		}
		for _, candidata := range pernas[cidadeAtual] {
			p := candidata.perna
			if len(acumulado) == 0 {
				// O filtro de data vale só para a primeira perna: é o que
				// permite a baldeação atravessar a meia-noite.
				if !mesmaData(p.Partida, data) {
					continue
				}
			} else {
				// Janela de conexão (D13): folga suficiente para trocar de
				// veículo, sem virar espera de um dia.
				if p.Partida.Before(livreEm.Add(MARGEM_BALDEACAO)) {
					continue
				}
				if p.Partida.After(livreEm.Add(ESPERA_MAXIMA_BALDEACAO)) {
					continue
				}
			}
			// Embarcar duas vezes no mesmo veículo não é itinerário.
			if caronasUsadas[p.CaronaID] {
				continue
			}

			caronasUsadas[p.CaronaID] = true
			acumulado = append(acumulado, p)
			expandir(candidata.cidadeDestino, p.Chegada)
			acumulado = acumulado[:len(acumulado)-1]
			caronasUsadas[p.CaronaID] = false
		}
	}
	// A recursão termina sem precisar marcar cidades visitadas: toda perna
	// avança no sentido da busca, então a cidade atual é estritamente
	// monotônica e nenhuma se repete. Com 4 cidades em linha, o itinerário
	// tem no máximo 3 pernas — teto que vem da topologia (D09), e não de um
	// limite arbitrário para conter explosão combinatória.
	expandir(i, time.Time{})

	// Passo 4 — ordenar e limitar.
	ordenarItinerarios(encontrados)
	if len(encontrados) > MAXIMO_ITINERARIOS {
		encontrados = encontrados[:MAXIMO_ITINERARIOS]
	}
	if encontrados == nil {
		// Lista vazia, e não nil: "nenhum itinerário" é resposta OK com
		// "itinerarios":[] (PROTOCOL.md, seção 5.8), e nil serializaria como
		// null.
		encontrados = []Itinerario{}
	}
	return encontrados, nil
}

// montarItinerario fecha um itinerário a partir das pernas acumuladas.
//
// Copia a fatia: acumulado é reaproveitado pela recursão a cada ramo, e
// guardar a fatia original faria os itinerários já encontrados mudarem sob os
// pés da busca.
func montarItinerario(acumulado []PernaItinerario) Itinerario {
	pernas := append([]PernaItinerario(nil), acumulado...)
	total := 0
	for _, p := range pernas {
		total += p.PrecoCentavos
	}
	return Itinerario{
		PrecoTotalCentavos: total,
		Partida:            pernas[0].Partida,
		Chegada:            pernas[len(pernas)-1].Chegada,
		Pernas:             pernas,
	}
}

// assinaturaItinerario reduz o itinerário à sequência de trechos que o
// identifica ("car-1:0-1|car-3:0-2"). Serve só de critério final de
// desempate.
func assinaturaItinerario(it Itinerario) string {
	var b strings.Builder
	for i, p := range it.Pernas {
		if i > 0 {
			b.WriteByte('|')
		}
		fmt.Fprintf(&b, "%s:%d-%d", p.CaronaID, p.De, p.Ate)
	}
	return b.String()
}

// ordenarItinerarios aplica a ordenação do PROTOCOL.md (seção 5.8): preço
// total crescente, empate por chegada mais cedo, depois por menos baldeações.
//
// O quarto critério não está no protocolo e não é uma regra de negócio: os
// três documentados não formam ordem total (no cenário da seção 9.2 há pares
// que empatam nos três), e sem um desempate final a lista sairia em ordem
// diferente a cada chamada. A assinatura é arbitrária de propósito — o que
// importa não é *qual* dos empatados vem antes, e sim que venha sempre o
// mesmo.
func ordenarItinerarios(lista []Itinerario) {
	sort.Slice(lista, func(a, b int) bool {
		x, y := lista[a], lista[b]
		if x.PrecoTotalCentavos != y.PrecoTotalCentavos {
			return x.PrecoTotalCentavos < y.PrecoTotalCentavos
		}
		if !x.Chegada.Equal(y.Chegada) {
			return x.Chegada.Before(y.Chegada)
		}
		if len(x.Pernas) != len(y.Pernas) {
			return len(x.Pernas) < len(y.Pernas)
		}
		return assinaturaItinerario(x) < assinaturaItinerario(y)
	})
}
