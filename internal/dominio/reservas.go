package dominio

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

// Regras de reserva e de cancelamento (PROJETO.md, seção 7; PROTOCOL.md,
// seções 5.7, 5.9, 5.10 e 5.11).
//
// Como em caronas.go, nada aqui adquire o mutex: estas funções recebem o
// estado já travado por estado.go (D05). É essa disciplina que permite a
// afirmação central do projeto — cada operação inteira, validação e escrita,
// acontece dentro de **uma** seção crítica (D07) — sem que seja preciso
// nenhum mecanismo de rollback ou de compensação.

// ANTECEDENCIA_CANCELAMENTO é o prazo do passageiro para cancelar, contado da
// partida do primeiro trecho da reserva (D13).
//
// O prazo existe para proteger o motorista da desistência de última hora. Por
// isso ele é assimétrico e **não** se aplica ao cancelamento em cascata: quando
// é o motorista que cancela a carona, as reservas dos passageiros caem
// independentemente de quanto falta para a partida. Aplicá-lo lá invertaria o
// sentido da regra, prendendo o passageiro a um veículo que não vai sair.
const ANTECEDENCIA_CANCELAMENTO = 1 * time.Hour

// gerarIDReserva sorteia um identificador opaco no formato "res-91c2a07e"
// (PROTOCOL.md, seção 3), pelos mesmos motivos de gerarIDCarona: roda dentro da
// seção crítica, então conferir colisão contra o mapa é grátis.
func gerarIDReserva(e *Estado) (string, error) {
	var sufixo [4]byte
	for tentativa := 0; tentativa < 10; tentativa++ {
		if _, err := rand.Read(sufixo[:]); err != nil {
			return "", fmt.Errorf("%w: %v", ErrGeracaoDeID, err)
		}
		id := "res-" + hex.EncodeToString(sufixo[:])
		if _, existe := e.reservas[id]; !existe {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: espaço de identificadores esgotado", ErrGeracaoDeID)
}

// copia devolve uma cópia profunda da reserva, pelo mesmo motivo de
// (*Carona).copia: nenhum ponteiro para dentro do estado pode escapar da seção
// crítica.
func (r *Reserva) copia() Reserva {
	copiada := *r
	copiada.Itens = append([]ItemReserva(nil), r.Itens...)
	return copiada
}

// itinerarioDaReserva resolve os índices guardados na reserva contra as
// caronas, produzindo o itinerário completo com cidades, horários e preços.
//
// É a única função que traduz {carona, De, Ate} em dados legíveis, e por isso
// é o ponto de reúso das quatro operações: a reserva usa o intervalo para
// checar sobreposição, a listagem devolve o itinerário inteiro ao passageiro, e
// os dois cancelamentos precisam da partida do primeiro trecho.
//
// Uma reserva nunca referencia carona inexistente (identificadores não são
// removidos do estado), mas a checagem existe para que uma inconsistência
// futura vire um itinerário incompleto e visível, e não um pânico dentro da
// seção crítica — que derrubaria o servidor inteiro, e não só a conexão.
func itinerarioDaReserva(e *Estado, r *Reserva) Itinerario {
	it := Itinerario{Pernas: make([]PernaItinerario, 0, len(r.Itens))}

	for _, item := range r.Itens {
		c, ok := e.caronas[item.CaronaID]
		if !ok {
			continue
		}

		motorista := c.MotoristaID
		if u, ok := e.usuarios[c.MotoristaID]; ok {
			motorista = u.Nome
		}

		preco := 0
		for t := item.De; t < item.Ate; t++ {
			preco += c.PrecoTrecho[t]
		}

		it.Pernas = append(it.Pernas, PernaItinerario{
			CaronaID:      c.ID,
			Motorista:     motorista,
			De:            item.De,
			Ate:           item.Ate,
			Origem:        c.Rota[item.De],
			Destino:       c.Rota[item.Ate],
			Partida:       c.Horarios[item.De],
			Chegada:       c.Horarios[item.Ate],
			PrecoCentavos: preco,
		})
		it.PrecoTotalCentavos += preco
	}

	if len(it.Pernas) > 0 {
		it.Partida = it.Pernas[0].Partida
		it.Chegada = it.Pernas[len(it.Pernas)-1].Chegada
	}
	return it
}

// seSobrepoe responde se dois intervalos fechados de tempo têm algum instante
// em comum (D14).
//
// Os intervalos são fechados de propósito: duas viagens que se encostam
// exatamente no mesmo instante — uma chegando, outra partindo — não são
// viáveis para a mesma pessoa. É a mesma razão pela qual existe
// MARGEM_BALDEACAO, e negar o caso de encostar mantém as duas regras
// coerentes entre si.
func seSobrepoe(partidaA, chegadaA, partidaB, chegadaB time.Time) bool {
	return !chegadaA.Before(partidaB) && !chegadaB.Before(partidaA)
}

// reservar implementa o algoritmo de reserva atômica da seção 7 do PROJETO.md
// e responde a RESERVAR (PROTOCOL.md, seção 5.9).
//
// A estrutura da função **é** o argumento de corretude, e por isso os passos
// estão marcados: os passos 1 a 4 apenas leem e podem retornar erro a qualquer
// momento; o passo 6 apenas escreve e não pode mais falhar. Nenhuma escrita
// acontece antes de toda a validação passar, então não existe estado parcial a
// desfazer e nenhum rollback é necessário (RNF06).
//
// Tudo isso roda dentro da seção crítica aberta por Estado.Reservar, de forma
// que a disponibilidade lida no passo 3 é exatamente a que o passo 6 decrementa
// — é isso, e não a busca, que garante RNF05.
func reservar(e *Estado, passageiroID string, itens []ItemReserva, agora time.Time) (Reserva, Itinerario, error) {
	// Passo 1 — formato.
	if len(itens) == 0 {
		return Reserva{}, Itinerario{}, fmt.Errorf("%w: nenhum trecho informado", ErrItemInvalido)
	}

	caronas := make([]*Carona, len(itens))
	vistas := make(map[string]bool, len(itens))
	for i, item := range itens {
		c, ok := e.caronas[item.CaronaID]
		if !ok {
			return Reserva{}, Itinerario{}, fmt.Errorf("%w: %q", ErrCaronaNaoEncontrada, item.CaronaID)
		}
		if c.Cancelada {
			return Reserva{}, Itinerario{}, fmt.Errorf("%w: %q", ErrCaronaCancelada, item.CaronaID)
		}
		if item.De < 0 || item.De >= item.Ate || item.Ate > len(c.Rota)-1 {
			return Reserva{}, Itinerario{}, fmt.Errorf("%w: trecho [%d,%d) fora da rota de %q, que tem %d cidades",
				ErrItemInvalido, item.De, item.Ate, item.CaronaID, len(c.Rota))
		}
		// Embarcar duas vezes no mesmo veículo não é itinerário — é a mesma
		// regra que a busca aplica ao montar os candidatos (PROJETO.md, seção 6).
		if vistas[item.CaronaID] {
			return Reserva{}, Itinerario{}, fmt.Errorf("%w: a carona %q aparece duas vezes", ErrItinerarioInvalido, item.CaronaID)
		}
		vistas[item.CaronaID] = true
		caronas[i] = c
	}

	// Passo 2 — encadeamento no espaço e no tempo.
	//
	// As duas margens são as mesmas constantes da busca (D13). Se divergissem,
	// a busca ofereceria itinerários que a reserva recusa, e o passageiro veria
	// a opção sumir entre a consulta e a confirmação.
	for i := 1; i < len(itens); i++ {
		anterior, atual := caronas[i-1], caronas[i]
		desembarque := anterior.Rota[itens[i-1].Ate]
		embarque := atual.Rota[itens[i].De]
		if desembarque != embarque {
			return Reserva{}, Itinerario{}, fmt.Errorf("%w: o trecho %d desembarca em %q e o seguinte embarca em %q",
				ErrItinerarioInvalido, i-1, desembarque, embarque)
		}

		chegada := anterior.Horarios[itens[i-1].Ate]
		partida := atual.Horarios[itens[i].De]
		if partida.Before(chegada.Add(MARGEM_BALDEACAO)) {
			return Reserva{}, Itinerario{}, fmt.Errorf("%w: folga de %s entre o trecho %d e o %d, abaixo da margem de %s",
				ErrItinerarioInvalido, partida.Sub(chegada), i-1, i, MARGEM_BALDEACAO)
		}
		if partida.After(chegada.Add(ESPERA_MAXIMA_BALDEACAO)) {
			return Reserva{}, Itinerario{}, fmt.Errorf("%w: espera de %s entre o trecho %d e o %d, acima do teto de %s",
				ErrItinerarioInvalido, partida.Sub(chegada), i-1, i, ESPERA_MAXIMA_BALDEACAO)
		}
	}

	// Ainda no passo 2: nenhuma cidade se repete no itinerário (D09). É a mesma
	// regra do passo 3 da busca, e pelo mesmo motivo das margens acima: sem ela
	// aqui, o cliente confirmaria por chamada direta ao protocolo um itinerário
	// de ida e volta que a busca nunca ofereceria.
	//
	// Contam todas as cidades de cada item, inclusive as intermediárias por onde
	// o passageiro passa dentro do veículo. A de embarque de cada item fica de
	// fora porque é a de desembarque do item anterior — já marcada, e onde o
	// passageiro de fato está. O passo 1 já garantiu que [De, Ate] cabe na rota.
	visitadas := map[string]bool{caronas[0].Rota[itens[0].De]: true}
	for i, item := range itens {
		percorridas := caronas[i].Rota[item.De+1 : item.Ate+1]
		for _, cidade := range percorridas {
			if visitadas[cidade] {
				return Reserva{}, Itinerario{}, fmt.Errorf("%w: o itinerário passa duas vezes por %q (trecho %d)",
					ErrItinerarioInvalido, cidade, i)
			}
		}
		marcar(percorridas, visitadas, true)
	}

	// Passo 3 — disponibilidade, trecho a trecho (RF11, D10).
	for i, item := range itens {
		c := caronas[i]
		for t := item.De; t < item.Ate; t++ {
			if c.Livres[t] < 1 {
				return Reserva{}, Itinerario{}, &ErroSemAssento{
					CaronaID:     c.ID,
					IndiceTrecho: t,
					Origem:       c.Rota[t],
					Destino:      c.Rota[t+1],
				}
			}
		}
	}

	// Passo 4 — sobreposição com as reservas ativas do mesmo passageiro (D14).
	//
	// A comparação é sobre o itinerário inteiro, da primeira partida à última
	// chegada, e não trecho a trecho: o tempo de espera de uma baldeação também
	// é tempo em que o passageiro não pode estar em outra viagem.
	partida := caronas[0].Horarios[itens[0].De]
	chegada := caronas[len(itens)-1].Horarios[itens[len(itens)-1].Ate]

	conflitante := ""
	for _, r := range e.reservas {
		if !r.Ativa || r.PassageiroID != passageiroID {
			continue
		}
		outro := itinerarioDaReserva(e, r)
		if len(outro.Pernas) == 0 {
			continue
		}
		if !seSobrepoe(partida, chegada, outro.Partida, outro.Chegada) {
			continue
		}
		// Havendo mais de uma conflitante, reportar sempre a de menor
		// identificador: a iteração de mapa em Go é aleatória, e sem este
		// desempate a mesma tentativa citaria reservas diferentes a cada
		// execução, o que confunde o passageiro e torna o teste instável.
		if conflitante == "" || r.ID < conflitante {
			conflitante = r.ID
		}
	}
	if conflitante != "" {
		return Reserva{}, Itinerario{}, &ErroConflitoHorario{ReservaID: conflitante}
	}

	// Última coisa que ainda pode falhar, e por isso vem antes de qualquer
	// escrita: gerar o identificador depende de crypto/rand.
	id, err := gerarIDReserva(e)
	if err != nil {
		return Reserva{}, Itinerario{}, err
	}

	// Passo 6 — commit. Daqui para baixo nada retorna erro.
	for i, item := range itens {
		c := caronas[i]
		for t := item.De; t < item.Ate; t++ {
			c.Livres[t]--
		}
	}

	nova := &Reserva{
		ID:           id,
		PassageiroID: passageiroID,
		// Os itens vêm decodificados do JSON do cliente e entram copiados: o
		// estado não pode compartilhar memória com quem o alimentou.
		Itens:    append([]ItemReserva(nil), itens...),
		Ativa:    true,
		CriadaEm: agora,
	}
	e.reservas[id] = nova

	return nova.copia(), itinerarioDaReserva(e, nova), nil
}

// reservasDoPassageiro lista as reservas de um passageiro com os itinerários
// resolvidos (PROTOCOL.md, seção 5.10).
//
// A ordenação é explícita pelo mesmo motivo de caronasDoMotorista: a iteração
// de mapa em Go é aleatória, e sem ela a mesma consulta devolveria a lista em
// ordem diferente a cada chamada. Ordenar pela partida é o que o passageiro
// espera — a próxima viagem primeiro.
func reservasDoPassageiro(e *Estado, passageiroID string, incluirCanceladas bool) []ReservaDetalhada {
	lista := make([]ReservaDetalhada, 0, len(e.reservas))
	for _, r := range e.reservas {
		if r.PassageiroID != passageiroID {
			continue
		}
		if !r.Ativa && !incluirCanceladas {
			continue
		}
		lista = append(lista, ReservaDetalhada{
			ID:         r.ID,
			Ativa:      r.Ativa,
			CriadaEm:   r.CriadaEm,
			Itinerario: itinerarioDaReserva(e, r),
		})
	}

	sort.Slice(lista, func(i, j int) bool {
		if !lista[i].Itinerario.Partida.Equal(lista[j].Itinerario.Partida) {
			return lista[i].Itinerario.Partida.Before(lista[j].Itinerario.Partida)
		}
		return lista[i].ID < lista[j].ID
	})
	return lista
}

// devolverAssentos reativa a capacidade consumida por uma reserva, trecho a
// trecho, em **todas** as caronas que ela usa.
//
// É o passo que sustenta o lado inferior de I1 (Livres nunca abaixo do que a
// ocupação real exige) e a metade de RNF07 que fala de assento bloqueado para
// sempre. Ele devolve os assentos até das caronas canceladas: o assento de uma
// carona cancelada não vale nada, mas deixá-lo decrementado quebraria a
// invariante e mascararia um defeito de verdade no próximo teste que a
// verificasse.
//
// Não marca a reserva como inativa — quem chama decide isso —, mas só deve ser
// chamada junto com essa marcação, ou o assento seria devolvido duas vezes.
func devolverAssentos(e *Estado, r *Reserva) {
	for _, item := range r.Itens {
		c, ok := e.caronas[item.CaronaID]
		if !ok {
			continue
		}
		for t := item.De; t < item.Ate && t < len(c.Livres); t++ {
			c.Livres[t]++
		}
	}
}

// cancelarReserva desfaz uma reserva do passageiro (PROTOCOL.md, seção 5.11).
//
// Mesma forma da reserva: valida tudo, e só então escreve. O prazo é contado da
// partida do primeiro trecho e vale até ANTECEDENCIA_CANCELAMENTO antes dela
// (D13); o instante exato do limite ainda é permitido, porque o prazo é "até 1
// hora antes", e não "menos de 1 hora antes".
func cancelarReserva(e *Estado, reservaID, passageiroID string, agora time.Time) error {
	r, ok := e.reservas[reservaID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrReservaNaoEncontrada, reservaID)
	}
	if r.PassageiroID != passageiroID {
		return fmt.Errorf("%w: reserva %q", ErrNaoEDono, reservaID)
	}
	if !r.Ativa {
		return fmt.Errorf("%w: %q", ErrReservaJaCancelada, reservaID)
	}

	it := itinerarioDaReserva(e, r)
	if len(it.Pernas) > 0 {
		limite := it.Partida.Add(-ANTECEDENCIA_CANCELAMENTO)
		if agora.After(limite) {
			return &ErroPrazoCancelamento{Partida: it.Partida}
		}
	}

	r.Ativa = false
	devolverAssentos(e, r)
	return nil
}

// cancelarCarona cancela a carona do motorista e propaga o cancelamento às
// reservas que dependem dela (PROTOCOL.md, seção 5.7). Devolve quantas reservas
// caíram na cascata.
//
// Três pontos que a arguição costuma cobrar:
//
//   - a cascata devolve os assentos de **todos** os trechos de cada reserva
//     atingida, inclusive os de caronas que não foram canceladas. Uma reserva
//     de duas pernas que perde a primeira deixa de existir por inteiro, e o
//     assento que ela ocupava na segunda perna precisa voltar ao mercado — do
//     contrário ele fica órfão, reservado por ninguém e invendável para sempre;
//   - o prazo de uma hora do passageiro **não** se aplica aqui (D13). Ele
//     protege o motorista contra desistência de última hora, e usá-lo para
//     impedir a cascata inverteria a proteção;
//   - o motorista pode cancelar até o instante da partida, e não até uma hora
//     antes. É o prazo dele, e é diferente.
//
// Tudo em uma seção crítica só, como manda a seção 7 do PROJETO.md: em nenhum
// instante observável existe carona cancelada com reserva ativa pendurada (I4),
// nem assento devolvido pela metade (I1).
func cancelarCarona(e *Estado, caronaID, motoristaID string, agora time.Time) (int, error) {
	c, ok := e.caronas[caronaID]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrCaronaNaoEncontrada, caronaID)
	}
	if c.MotoristaID != motoristaID {
		return 0, fmt.Errorf("%w: carona %q", ErrNaoEDono, caronaID)
	}
	if c.Cancelada {
		return 0, fmt.Errorf("%w: %q", ErrCaronaCancelada, caronaID)
	}

	partida := c.Horarios[0]
	if agora.After(partida) {
		return 0, &ErroPrazoCancelamento{Partida: partida}
	}

	// A partir daqui nada mais pode falhar.
	c.Cancelada = true

	canceladas := 0
	for _, r := range e.reservas {
		if !r.Ativa {
			continue
		}
		usa := false
		for _, item := range r.Itens {
			if item.CaronaID == caronaID {
				usa = true
				break
			}
		}
		if !usa {
			continue
		}
		r.Ativa = false
		devolverAssentos(e, r)
		canceladas++
	}
	return canceladas, nil
}
