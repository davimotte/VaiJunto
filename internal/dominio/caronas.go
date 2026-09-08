package dominio

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
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
