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

// gerarIDCarona sorteia um identificador opaco no formato "car-3f2a8b1d"
// (PROTOCOL.md, seção 3).
//
// Roda dentro da seção crítica, então checar colisão contra o mapa é grátis e
// remove qualquer dúvida sobre duas publicações simultâneas gerarem o mesmo
// id. Um contador sequencial seria mais simples, mas colidiria com os ids
// fixos de dados/caronas.json ("car-1", "car-2", ...) depois de um reinício.
func gerarIDCarona(e *Estado) (string, error) {
	var sufixo [4]byte
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

// validarCarona confere as regras que toda carona precisa cumprir para entrar
// no estado, venha ela de PUBLICAR_CARONA ou de dados/caronas.json (D09, I6).
//
// Os horários vêm de quem informa as paradas, e nada garante por construção
// que eles cresçam. A busca (janela de baldeação), a reserva (encadeamento e
// sobreposição) e a invariante I3 contam com isso, e por isso a validação
// existe nas duas portas de entrada do estado, e não só na do protocolo.
//
// Fica de fora, de propósito, a regra da partida no futuro: ela é da
// publicação. A carga de boot lê dados com data fixa, e aplicá-la impediria o
// servidor de subir depois dessa data.
func validarCarona(rota []string, horarios []time.Time, assentos int, precos []int) error {
	if len(rota) < 2 {
		return fmt.Errorf("%w: %d parada(s), e uma carona precisa de ao menos duas", ErrRotaInvalida, len(rota))
	}
	if len(horarios) != len(rota) {
		return fmt.Errorf("%w: %d horários para %d paradas", ErrRotaInvalida, len(horarios), len(rota))
	}

	vistas := make(map[string]bool, len(rota))
	for i, cidade := range rota {
		if !CidadeConhecida(cidade) {
			return fmt.Errorf("%w: parada %d %q", ErrCidadeDesconhecida, i, cidade)
		}
		// Uma rota que volta a uma cidade faria a mesma cidade aparecer em
		// duas posições, e "embarcar em Salvador" deixaria de designar um
		// ponto só da carona.
		if vistas[cidade] {
			return fmt.Errorf("%w: %q aparece mais de uma vez na rota", ErrRotaInvalida, cidade)
		}
		vistas[cidade] = true
	}

	// Estritamente crescentes: há um horário por parada, sem espera no local
	// (D09), então dois horários iguais seriam um trecho de duração zero — o
	// carro em duas cidades no mesmo instante.
	for i := 1; i < len(horarios); i++ {
		if !horarios[i].After(horarios[i-1]) {
			return fmt.Errorf("%w: horário de %q (%s) não é posterior ao de %q (%s)",
				ErrRotaInvalida, rota[i], horarios[i], rota[i-1], horarios[i-1])
		}
	}

	trechos := len(rota) - 1
	if len(precos) != trechos {
		return fmt.Errorf("%w: %d preços para %d trechos", ErrRotaInvalida, len(precos), trechos)
	}
	for i, preco := range precos {
		if preco < 0 {
			return fmt.Errorf("%w: trecho %d com preço %d", ErrPrecoInvalido, i, preco)
		}
	}
	if assentos < 1 {
		return fmt.Errorf("%w: %d", ErrAssentosInvalidos, assentos)
	}
	return nil
}

// publicarCarona valida os dados de PUBLICAR_CARONA (PROTOCOL.md, seção 5.4)
// e insere a carona no estado.
//
// Como na reserva (D07), toda a validação vem antes de qualquer escrita: a
// carona só entra no mapa depois que nenhuma regra pode mais falhar, então
// não existe estado parcial a desfazer e não é preciso rollback.
//
// O motorista informa as paradas e o horário de cada uma (D09); nada é
// derivado aqui. As regras comuns a toda carona ficam em validarCarona, que a
// carga de boot também usa, e só a da partida no futuro é exclusiva da
// publicação.
//
// agora é recebido de fora em vez de lido de time.Now() aqui para que a regra
// "partida no futuro" seja testável sem depender do relógio da máquina.
func publicarCarona(e *Estado, motoristaID string, rota []string, horarios []time.Time, assentos int, precos []int, agora time.Time) (Carona, error) {
	if err := validarCarona(rota, horarios, assentos, precos); err != nil {
		return Carona{}, err
	}
	// Basta olhar a primeira parada: validarCarona já garantiu que as
	// seguintes vêm depois dela. Comparação por After, nunca por ==: instantes
	// iguais em fusos diferentes são o mesmo ponto no tempo (D11).
	if partida := horarios[0]; !partida.After(agora) {
		return Carona{}, fmt.Errorf("%w: %s não é posterior a %s", ErrPartidaInvalida, partida, agora)
	}

	id, err := gerarIDCarona(e)
	if err != nil {
		return Carona{}, err
	}

	livres := make([]int, len(rota)-1)
	for i := range livres {
		livres[i] = assentos
	}

	// Primeira e única escrita. Rota, horários e preços vêm de fora (foram
	// decodificados do JSON do cliente), então entram copiados: o estado não
	// pode compartilhar memória com quem o alimentou. Sem a cópia, o chamador
	// poderia reordenar os horários depois da validação, sem lock nenhum.
	carona := &Carona{
		ID:          id,
		MotoristaID: motoristaID,
		Rota:        append([]string(nil), rota...),
		Horarios:    append([]time.Time(nil), horarios...),
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
	// chega a Feira de Santana em 01/10 às 07:45 e car-7 parte de lá em 02/10
	// no mesmo horário, encadeamento válido no espaço e no tempo que
	// produziria um "itinerário" com 24 h de espera. Doze horas separa a
	// conexão noturna legítima da espera de um dia inteiro.
	ESPERA_MAXIMA_BALDEACAO = 12 * time.Hour

	// MAXIMO_ITINERARIOS limita a resposta da busca (D16; PROTOCOL.md, seção
	// 5.8).
	//
	// A resposta é uma única linha do protocolo, sujeita ao teto de 64 KB, e o
	// cliente aplica o mesmo teto ao ler: um itinerário de duas pernas ocupa
	// cerca de 1 KB, e sem limite uma busca com dezenas de combinações
	// derrubaria a conexão do passageiro. Dez cabem com folga mesmo com três
	// pernas cada. O corte só é seguro porque vem depois da ordenação.
	MAXIMO_ITINERARIOS = 10
)

// pernaCandidata é uma perna gerada no passo 2 da busca, antes de entrar em
// qualquer itinerário.
//
// Além da perna em si, guarda as cidades que o passageiro percorre nela, da
// de embarque à de desembarque, inclusive as intermediárias: é sobre elas que
// a busca em profundidade aplica a regra de não revisitar cidade (D09). A
// fatia aponta para dentro de Carona.Rota sem cópia, o que é seguro porque só
// é lida, e só dentro da seção crítica da busca.
type pernaCandidata struct {
	perna   PernaItinerario
	cidades []string
}

// gerarPernas executa o passo 2 do algoritmo de busca (PROJETO.md, seção 6):
// todo segmento contíguo de toda carona com assento livre, indexado pela
// cidade de embarque.
//
// A única poda é a de trecho sem assento, a única que lê estado mutável — e
// ela é só uma poda: a busca não reserva nem promete nada, e a reserva refaz
// a verificação inteira sob o lock (D07). Não há poda geométrica: as cidades
// não têm ordem nem sentido de viagem (D09), e uma poda pelo sentido das
// primeiras paradas descartaria caronas que servem à busca.
//
// Uma carona com k paradas gera no máximo k(k−1)/2 pernas.
func gerarPernas(e *Estado) map[string][]pernaCandidata {
	pernas := make(map[string][]pernaCandidata)

	for _, c := range e.caronas {
		if c.Cancelada || len(c.Rota) < 2 {
			continue
		}

		motorista := c.MotoristaID
		if u, ok := e.usuarios[c.MotoristaID]; ok {
			motorista = u.Nome
		}

		for a := 0; a < len(c.Rota)-1; a++ {
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

				pernas[c.Rota[a]] = append(pernas[c.Rota[a]], pernaCandidata{
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
					cidades: c.Rota[a : b+1],
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
	// Passo 1 — validar.
	if !CidadeConhecida(origem) {
		return nil, fmt.Errorf("%w: origem %q", ErrCidadeDesconhecida, origem)
	}
	if !CidadeConhecida(destino) {
		return nil, fmt.Errorf("%w: destino %q", ErrCidadeDesconhecida, destino)
	}
	if origem == destino {
		return nil, fmt.Errorf("%w: origem e destino são a mesma cidade %q", ErrRotaInvalida, origem)
	}

	// Passo 2 — gerar pernas candidatas.
	pernas := gerarPernas(e)

	// Passo 3 — busca em profundidade. O estado é a cidade atual, o instante
	// em que o passageiro fica livre nela e as cidades por onde ele já passou.
	var encontrados []Itinerario
	var acumulado []PernaItinerario
	caronasUsadas := make(map[string]bool)
	visitadas := map[string]bool{origem: true}

	var expandir func(cidadeAtual string, livreEm time.Time)
	expandir = func(cidadeAtual string, livreEm time.Time) {
		if cidadeAtual == destino {
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
			// Nenhuma cidade da perna pode ter sido visitada, exceto a de
			// embarque, que é onde o passageiro já está. Contam também as
			// intermediárias: passar por uma cidade dentro do carro é estar
			// nela (D09).
			if atravessaVisitada(candidata.cidades[1:], visitadas) {
				continue
			}

			caronasUsadas[p.CaronaID] = true
			marcar(candidata.cidades[1:], visitadas, true)
			acumulado = append(acumulado, p)
			expandir(p.Destino, p.Chegada)
			acumulado = acumulado[:len(acumulado)-1]
			marcar(candidata.cidades[1:], visitadas, false)
			caronasUsadas[p.CaronaID] = false
		}
	}
	// O conjunto de visitadas não é redundante com o encadeamento: horários
	// avançando e carona não repetida não impedem um itinerário de ir e voltar
	// (Salvador → Feira, Feira → Salvador, Salvador → Conquista encadeia). E é
	// ele que dá o teto da recursão: cada perna acrescenta ao menos uma cidade
	// nova, então um itinerário tem no máximo |cidades| − 1 pernas — teto que
	// vem de uma regra do domínio, e não de um limite arbitrário para conter
	// explosão combinatória.
	expandir(origem, time.Time{})

	// Passo 4 — ordenar e limitar (D16). A ordem das duas linhas é a regra: o
	// corte depois da ordenação nunca descarta um itinerário com menos trocas
	// enquanto mantém um com mais.
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

// atravessaVisitada responde se alguma das cidades já está no conjunto.
func atravessaVisitada(cidades []string, visitadas map[string]bool) bool {
	for _, cidade := range cidades {
		if visitadas[cidade] {
			return true
		}
	}
	return false
}

// marcar põe as cidades no conjunto de visitadas, ou as tira dele, ao entrar
// e ao sair de um ramo da busca em profundidade.
//
// Desmarcar é seguro porque atravessaVisitada garantiu, antes da marcação,
// que nenhuma dessas cidades estava no conjunto: tirá-las devolve o conjunto
// exatamente ao estado de antes do ramo.
func marcar(cidades []string, visitadas map[string]bool, valor bool) {
	for _, cidade := range cidades {
		if valor {
			visitadas[cidade] = true
		} else {
			delete(visitadas, cidade)
		}
	}
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

// ordenarItinerarios aplica a ordenação da D16 (PROTOCOL.md, seção 5.8):
// menos baldeações primeiro, empate por menor preço total, depois por chegada
// mais cedo.
//
// Baldeação vem antes de preço porque cada troca de veículo é um ponto em que
// o passageiro depende de dois motoristas cumprirem o horário; o preço só
// decide entre roteiros com o mesmo número de trocas. Comparar len(Pernas) é o
// mesmo que comparar baldeações, que são uma a menos que as pernas.
//
// O quarto critério não é uma regra de negócio: os três da D16 não formam
// ordem total (há itinerários que empatam nos três), e sem um desempate final
// a lista sairia em ordem diferente a cada chamada — e, com o limite de
// MAXIMO_ITINERARIOS, o corte poderia descartar um itinerário diferente a cada
// vez. A assinatura é arbitrária de propósito: o que importa não é *qual* dos
// empatados vem antes, e sim que venha sempre o mesmo.
func ordenarItinerarios(lista []Itinerario) {
	sort.Slice(lista, func(a, b int) bool {
		x, y := lista[a], lista[b]
		if len(x.Pernas) != len(y.Pernas) {
			return len(x.Pernas) < len(y.Pernas)
		}
		if x.PrecoTotalCentavos != y.PrecoTotalCentavos {
			return x.PrecoTotalCentavos < y.PrecoTotalCentavos
		}
		if !x.Chegada.Equal(y.Chegada) {
			return x.Chegada.Before(y.Chegada)
		}
		return assinaturaItinerario(x) < assinaturaItinerario(y)
	})
}
