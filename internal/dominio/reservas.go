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

// GerarIDTransacao sorteia o identificador de uma transação, no formato
// "<empresa>:res-91c2a07e" (D27; seção 4.2).
//
// A mesma família de prefixo "res-" serve à transação e à reserva porque cada
// tentativa de reserva **é** uma transação com identificador próprio (D36), e a
// reserva confirmada fica com o identificador da tentativa que venceu.
//
// Diferente do gerador do P1, não recebe o Estado e não confere colisão contra
// o mapa de reservas: o coordenador precisa do identificador **antes** de tocar
// no estado, porque o txID vai no PREPARAR e a votação acontece inteira fora de
// qualquer seção crítica (regra 2). A colisão continua coberta onde importa —
// ConfirmarReserva recusa um txID que já exista, e aí seria violação de
// protocolo, não azar.
func GerarIDTransacao(empresa string) (string, error) {
	if err := ValidarEmpresa(empresa); err != nil {
		return "", err
	}
	var sufixo [4]byte
	if _, err := rand.Read(sufixo[:]); err != nil {
		return "", fmt.Errorf("%w: %v", ErrGeracaoDeID, err)
	}
	return prefixar(empresa, "res-"+hex.EncodeToString(sufixo[:])), nil
}

// copia devolve uma cópia profunda da reserva, pelo mesmo motivo de
// (*Carona).copia: nenhum ponteiro para dentro do estado pode escapar da seção
// crítica.
func (r *Reserva) copia() Reserva {
	copiada := *r
	copiada.Itens = append([]ItemReserva(nil), r.Itens...)
	copiada.Participantes = append([]string(nil), r.Participantes...)
	copiada.Pernas = make([]Perna, len(r.Pernas))
	for i, p := range r.Pernas {
		copiada.Pernas[i] = p
		// Cidades é fatia dentro da perna: copiar só o slice externo deixaria
		// as duas reservas apontando para o mesmo arranjo de cidades.
		copiada.Pernas[i].Cidades = append([]string(nil), p.Cidades...)
	}
	return copiada
}

// itinerarioDasPernas monta o itinerário legível a partir da cópia das pernas
// que a reserva guarda (D42).
//
// No P1 esta função resolvia {carona, De, Ate} contra e.caronas, para não
// guardar duas vezes o mesmo dado (PROJETO.md, seção 4). No P2 isso não serve
// mais: as caronas das pernas remotas estão em **outros** servidores, e procurar
// por carona de outra empresa em e.caronas não encontra nada. Listar as
// reservas não pode depender de todos os pares estarem no ar (RD05), então a
// fonte passa a ser a cópia autoritativa que veio nos votos SIM.
//
// A cópia não diverge do original porque rota, horários e preços são imutáveis
// após a publicação (D09). O único dado mutável, o cancelamento, chega ao
// coordenador pelo aviso da D39.
//
// É função pura sobre a cópia: não lê o estado e por isso nem precisaria do
// lock. Fica aqui por coerência com o resto do arquivo.
func itinerarioDasPernas(pernas []Perna) Itinerario {
	it := Itinerario{Pernas: make([]PernaItinerario, 0, len(pernas))}
	for _, p := range pernas {
		if len(p.Cidades) < 2 {
			// Perna malformada nunca é gravada; a checagem existe para que uma
			// inconsistência futura vire um itinerário incompleto e visível, e
			// não um pânico dentro da seção crítica — que derrubaria o servidor
			// inteiro, e não só a conexão.
			continue
		}
		it.Pernas = append(it.Pernas, PernaItinerario{
			CaronaID:      p.CaronaID,
			Empresa:       p.Empresa,
			Motorista:     p.MotoristaNome,
			De:            p.De,
			Ate:           p.Ate,
			Origem:        p.Cidades[0],
			Destino:       p.Cidades[len(p.Cidades)-1],
			Partida:       p.Partida,
			Chegada:       p.Chegada,
			PrecoCentavos: p.PrecoCentavos,
		})
		it.PrecoTotalCentavos += p.PrecoCentavos
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

// ValidarItens é o passo 1 do algoritmo do coordenador (seção 7.1): o que se
// pode conferir na lista pedida sem saber nada das caronas.
//
// Fica no domínio, e não na borda TCP, porque é regra de negócio: "de < ate" e
// "nenhuma carona repetida" valem venha o pedido de onde vier. O que **não**
// está aqui é se a empresa de cada carona pertence ao consórcio — essa lista é
// configuração do servidor, e quem a tem é o coordenador (camada 5).
//
// Embarcar duas vezes no mesmo veículo não é itinerário: é a mesma regra que a
// busca aplica ao montar os candidatos (PROJETO.md, seção 6).
func ValidarItens(itens []ItemReserva) error {
	if len(itens) == 0 {
		return fmt.Errorf("%w: nenhum trecho informado", ErrItemInvalido)
	}

	vistas := make(map[string]bool, len(itens))
	for i, item := range itens {
		if item.CaronaID == "" {
			return fmt.Errorf("%w: o trecho %d não informa a carona", ErrItemInvalido, i)
		}
		if _, err := EmpresaDoID(item.CaronaID); err != nil {
			return err
		}
		if item.De < 0 || item.De >= item.Ate {
			return fmt.Errorf("%w: trecho [%d,%d) inválido em %q", ErrItemInvalido, item.De, item.Ate, item.CaronaID)
		}
		if vistas[item.CaronaID] {
			return fmt.Errorf("%w: a carona %q aparece duas vezes", ErrItinerarioInvalido, item.CaronaID)
		}
		vistas[item.CaronaID] = true
	}
	return nil
}

// ValidarEncadeamento é o passo 7 do algoritmo do coordenador (seção 7.1): o
// encadeamento no espaço e no tempo do itinerário inteiro, verificado sobre os
// dados **autoritativos** que os donos devolveram nos votos SIM (D34).
//
// É o passo 2 do RESERVAR do P1 (PROJETO.md, seção 7), agora do lado de quem
// coordena. A divisão da D34 é esta: cada participante valida o que só ele sabe
// (a carona existe, não está cancelada, os índices cabem, há assento), e o
// coordenador valida o que envolve mais de uma empresa — e por isso nenhuma
// empresa sozinha poderia validar.
//
// O coordenador **não** confia no resultado da busca, que pode estar
// desatualizado: os horários conferidos aqui são os que o dono afirmou agora,
// no voto, e não os que a busca mostrou ao passageiro.
//
// As duas margens são as mesmas constantes da busca (D13). Se divergissem, a
// busca ofereceria itinerários que a reserva recusa, e o passageiro veria a
// opção sumir entre a consulta e a confirmação.
//
// É função pura: roda fora do lock, porque só trabalha sobre a cópia das
// pernas. A versão da busca (caronas.go) continua separada de propósito — lá o
// encadeamento é poda incremental dentro da DFS, que valida a próxima perna
// enquanto expande; aqui é conferência final de um itinerário completo.
func ValidarEncadeamento(pernas []Perna) error {
	if len(pernas) == 0 {
		return fmt.Errorf("%w: itinerário sem pernas", ErrItinerarioInvalido)
	}
	for i, p := range pernas {
		if len(p.Cidades) < 2 {
			return fmt.Errorf("%w: a perna %d não informa as cidades percorridas", ErrItinerarioInvalido, i)
		}
	}

	for i := 1; i < len(pernas); i++ {
		anterior, atual := pernas[i-1], pernas[i]
		desembarque := anterior.Cidades[len(anterior.Cidades)-1]
		embarque := atual.Cidades[0]
		if desembarque != embarque {
			return fmt.Errorf("%w: o trecho %d desembarca em %q e o seguinte embarca em %q",
				ErrItinerarioInvalido, i-1, desembarque, embarque)
		}

		chegada, partida := anterior.Chegada, atual.Partida
		if partida.Before(chegada.Add(MARGEM_BALDEACAO)) {
			return fmt.Errorf("%w: folga de %s entre o trecho %d e o %d, abaixo da margem de %s",
				ErrItinerarioInvalido, partida.Sub(chegada), i-1, i, MARGEM_BALDEACAO)
		}
		if partida.After(chegada.Add(ESPERA_MAXIMA_BALDEACAO)) {
			return fmt.Errorf("%w: espera de %s entre o trecho %d e o %d, acima do teto de %s",
				ErrItinerarioInvalido, partida.Sub(chegada), i-1, i, ESPERA_MAXIMA_BALDEACAO)
		}
	}

	// Nenhuma cidade se repete no itinerário (D09), contando as intermediárias
	// por onde o passageiro passa dentro do veículo — é para isso que a Perna
	// carrega Cidades inteira, e não só origem e destino. A de embarque de cada
	// perna fica de fora porque é a de desembarque da anterior: já marcada, e
	// onde o passageiro de fato está.
	visitadas := map[string]bool{pernas[0].Cidades[0]: true}
	for i, p := range pernas {
		percorridas := p.Cidades[1:]
		for _, cidade := range percorridas {
			if visitadas[cidade] {
				return fmt.Errorf("%w: o itinerário passa duas vezes por %q (trecho %d)",
					ErrItinerarioInvalido, cidade, i)
			}
		}
		marcar(percorridas, visitadas, true)
	}
	return nil
}

// marcarReservaEmAndamento é o passo 2 do coordenador (D35): no máximo uma
// reserva em andamento por passageiro.
//
// No P1 o mutex global impedia que duas reservas simultâneas do mesmo
// passageiro furassem a checagem de sobreposição (T8). No P2 entre a votação e
// a decisão passa-se tempo de rede, e as duas poderiam passar na checagem: a
// seção crítica da validação não é mais a mesma da gravação. A marca restaura a
// garantia sem precisar segurar o lock durante a votação, que é proibido
// (regra 2).
//
// Nada disso vai ao log: o coordenador não grava nada antes de decidir (D23).
func marcarReservaEmAndamento(e *Estado, passageiroID string) error {
	if e.emAndamento[passageiroID] {
		return fmt.Errorf("%w: %q", ErrReservaEmAndamento, passageiroID)
	}
	e.emAndamento[passageiroID] = true
	return nil
}

// liberarReservaEmAndamento retira a marca da D35. O coordenador a chama num
// defer, para que ela saia em **qualquer** saída do algoritmo — erro, prazo
// esgotado, abort ou pânico.
func liberarReservaEmAndamento(e *Estado, passageiroID string) {
	delete(e.emAndamento, passageiroID)
}

// confirmarReserva é o passo 8 do coordenador, o **ponto de commit** da
// transação (seção 7.1): grava DECISAO_CONFIRMAR com fsync e cria a reserva
// com a cópia das pernas (D42).
//
// Duas coisas acontecem aqui numa seção crítica só, e a ordem é o argumento de
// corretude:
//
//  1. a sobreposição com as reservas ativas do passageiro é conferida (passo 4
//     do P1, D14) — ela é a última coisa que ainda pode falhar;
//  2. só então o registro vai ao log, e só depois de o fsync voltar a reserva
//     entra no estado.
//
// Validar e escrever na mesma seção crítica é a regra 5 ("valide tudo antes de
// escrever qualquer coisa"): nenhuma escrita acontece antes de toda a validação
// passar, então não existe estado parcial a desfazer. A D35 já garantiria que
// nenhuma outra reserva deste passageiro entra no meio, mas conferir aqui
// dispensa esse argumento — a invariante passa a ser local a esta função.
//
// O OK ao passageiro só pode sair depois do retorno desta função, porque é o
// fsync do DECISAO_CONFIRMAR que torna a decisão durável (regra 3). A partir
// daqui a transação está confirmada mesmo que o servidor caia: a fase 2 é
// retomada no boot (seção 9.3).
//
// **Não mexe em Livres.** Os assentos se movem só pelos registros do dono
// (PREPARADO, CONFIRMADO, ABORTADO): esta reserva é a visão do coordenador, e
// descontar aqui também seria descontar o mesmo assento duas vezes quando a
// empresa é coordenadora e participante da mesma transação.
func confirmarReserva(e *Estado, txID, passageiroID string, participantes []string, itens []ItemReserva, pernas []Perna, agora time.Time) (Reserva, Itinerario, error) {
	if _, existe := e.reservas[txID]; existe {
		// O txID é novo a cada tentativa (D36) e nasce aqui; repetição é bug do
		// coordenador, não caso de idempotência.
		return Reserva{}, Itinerario{}, fmt.Errorf("%w: a reserva %q já existe", ErrViolacaoProtocolo, txID)
	}

	it := itinerarioDasPernas(pernas)
	if len(it.Pernas) == 0 {
		return Reserva{}, Itinerario{}, fmt.Errorf("%w: nenhuma perna autoritativa", ErrItinerarioInvalido)
	}

	// Passo 4 do P1 — sobreposição com as reservas ativas do mesmo passageiro
	// (D14). A comparação é sobre o itinerário inteiro, da primeira partida à
	// última chegada, e não trecho a trecho: o tempo de espera de uma baldeação
	// também é tempo em que o passageiro não pode estar em outra viagem.
	conflitante := ""
	for _, r := range e.reservas {
		if !r.Ativa || r.PassageiroID != passageiroID {
			continue
		}
		outro := itinerarioDasPernas(r.Pernas)
		if len(outro.Pernas) == 0 {
			continue
		}
		if !seSobrepoe(it.Partida, it.Chegada, outro.Partida, outro.Chegada) {
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

	nova := &Reserva{
		ID:           txID,
		PassageiroID: passageiroID,
		// Tudo entra copiado: o estado não pode compartilhar memória com quem o
		// alimentou, e as pernas vêm de fora, da resposta dos participantes.
		Itens:         append([]ItemReserva(nil), itens...),
		Participantes: append([]string(nil), participantes...),
		Ativa:         true,
		CriadaEm:      agora,
	}
	nova.Pernas = make([]Perna, len(pernas))
	for i, p := range pernas {
		nova.Pernas[i] = p
		nova.Pernas[i].Cidades = append([]string(nil), p.Cidades...)
	}

	// Write-ahead: o registro vai ao log, com fsync, antes de a reserva entrar
	// no estado (regra 3). Falha no log não aplica nada.
	if err := gravar(e, tipoDecisaoConfirmar, dadosDecisaoConfirmarDe(nova)); err != nil {
		return Reserva{}, Itinerario{}, err
	}

	e.reservas[txID] = nova
	return nova.copia(), it, nil
}

// concluirReserva marca o fim da fase 2 (seção 7.1, passo 9): todos os
// participantes confirmaram o recebimento do CONFIRMAR.
//
// É só um fato registrado, sem efeito sobre assentos. Serve à auditoria e à
// recuperação: uma reserva confirmada e **não** concluída é o que faz o boot
// retomar a fase 2 (seção 9.3).
func concluirReserva(e *Estado, txID string) error {
	r, existe := e.reservas[txID]
	if !existe {
		return fmt.Errorf("%w: reserva %q desconhecida", ErrReservaNaoEncontrada, txID)
	}
	if r.Concluida {
		// Idempotente: a fase 2 pode terminar duas vezes se a repetição e a
		// retomada do boot se cruzarem.
		return nil
	}
	if err := gravar(e, tipoConcluida, dadosTransacao{Tx: txID}); err != nil {
		return err
	}
	r.Concluida = true
	return nil
}

// reservasDoPassageiro lista as reservas de um passageiro com os itinerários
// resolvidos (PROTOCOL.md, seção 5.10).
//
// A ordenação é explícita pelo mesmo motivo de caronasDoMotorista: a iteração
// de mapa em Go é aleatória, e sem ela a mesma consulta devolveria a lista em
// ordem diferente a cada chamada. Ordenar pela partida é o que o passageiro
// espera — a próxima viagem primeiro.
//
// As ativas vêm antes das canceladas, e só depois de ordenar a lista é cortada
// em MAXIMO_ITENS_LISTAGEM (D16): o limite descarta primeiro o histórico, e
// nunca uma viagem que o passageiro ainda vai fazer.
func reservasDoPassageiro(e *Estado, passageiroID string, incluirCanceladas bool) []ReservaDetalhada {
	// Sem capacidade prévia: len(e.reservas) é o histórico de todos os
	// passageiros, e reservá-lo a cada listagem custava uma alocação do tamanho
	// do histórico inteiro (PROJETO.md, seção 8.3).
	var lista []ReservaDetalhada
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
			Itinerario: itinerarioDasPernas(r.Pernas),
		})
	}

	sort.Slice(lista, func(i, j int) bool {
		if lista[i].Ativa != lista[j].Ativa {
			return lista[i].Ativa
		}
		if !lista[i].Itinerario.Partida.Equal(lista[j].Itinerario.Partida) {
			return lista[i].Itinerario.Partida.Before(lista[j].Itinerario.Partida)
		}
		return lista[i].ID < lista[j].ID
	})
	if len(lista) > MAXIMO_ITENS_LISTAGEM {
		lista = lista[:MAXIMO_ITENS_LISTAGEM]
	}
	return lista
}

// devolverItens devolve a capacidade consumida por uma lista de itens, trecho a
// trecho, em **todas** as caronas que eles usam.
//
// É o passo que sustenta o lado inferior da I-P2-1 (Livres nunca abaixo do que a
// ocupação real exige) e a metade de RNF07 que fala de assento bloqueado para
// sempre. Devolve os assentos até das caronas canceladas: o assento de uma
// carona cancelada não vale nada, mas deixá-lo decrementado quebraria a
// invariante e mascararia um defeito de verdade no próximo teste que a
// verificasse.
//
// Opera sobre itens, e não sobre uma Reserva, porque quem chama é sempre uma
// Participacao: o abort do participante (seção 7.2) e o cancelamento
// (cancelarParticipacao, D39) liberam os assentos em espera ou confirmados de
// uma participação, que tem itens, mas não é uma Reserva — a devolução da
// reserva inteira passa por cancelarParticipacao em cada empresa dona,
// nunca por um desconto direto no coordenador.
//
// Não marca nada como inativo — quem chama decide isso —, mas só deve ser
// chamada junto com essa marcação, ou o assento seria devolvido duas vezes.
func devolverItens(e *Estado, itens []ItemReserva) {
	for _, item := range itens {
		c, ok := e.caronas[item.CaronaID]
		if !ok {
			continue
		}
		for t := item.De; t < item.Ate && t < len(c.Livres); t++ {
			c.Livres[t]++
		}
	}
}

// cancelarReserva desfaz uma reserva do passageiro (PROTOCOL.md, seção 5.11;
// PROJETO_P2.md, seção 10.1; D39).
//
// Cancelar não pode ser recusado por ninguém — diferente de confirmar, que
// precisa da unanimidade do 2PC —, então a atomicidade entre empresas não
// vem de votação: vem de decisão unilateral mais entrega confiável (outbox).
// Tudo o que esta empresa decide acontece numa seção crítica só: grava
// CANCELAMENTO_DECIDIDO, desativa a reserva, cancela a perna local se esta
// empresa também participa, e enfileira um aviso CANCELAR_PERNAS para cada
// participante remoto. Devolve esses avisos para quem chamou entregá-los
// fora do lock (regra 2) — a entrega em si, com retentativa, é da camada 5.
//
// Enquanto o aviso não chega, o assento continua ocupado no participante
// remoto: é conservador de propósito (seção 10.3), para nunca causar venda
// dupla — a devolução lá chega quando o POST /transacoes/{tx}/cancelar for
// confirmado, nunca antes.
func cancelarReserva(e *Estado, reservaID, passageiroID string, agora time.Time) ([]Aviso, error) {
	r, ok := e.reservas[reservaID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrReservaNaoEncontrada, reservaID)
	}
	if r.PassageiroID != passageiroID {
		return nil, fmt.Errorf("%w: reserva %q", ErrNaoEDono, reservaID)
	}
	if !r.Ativa {
		return nil, fmt.Errorf("%w: %q", ErrReservaJaCancelada, reservaID)
	}

	it := itinerarioDasPernas(r.Pernas)
	if len(it.Pernas) > 0 {
		limite := it.Partida.Add(-ANTECEDENCIA_CANCELAMENTO)
		if agora.After(limite) {
			return nil, &ErroPrazoCancelamento{Partida: it.Partida}
		}
	}

	return decidirCancelamentoDaReserva(e, r)
}

// decidirCancelamentoDaReserva grava CANCELAMENTO_DECIDIDO, desativa a
// reserva, cancela a perna local quando esta empresa também participa, e
// enfileira um aviso CANCELAR_PERNAS a cada participante remoto.
//
// É o passo comum entre cancelarReserva (10.1, com o prazo do passageiro já
// conferido por quem chama) e a cascata de cancelarCarona (10.2, sem esse
// prazo — a assimetria da D13): as duas só diferem em QUEM decide cancelar e
// SOB QUE PRAZO, nunca no efeito da decisão em si.
func decidirCancelamentoDaReserva(e *Estado, r *Reserva) ([]Aviso, error) {
	if err := gravar(e, tipoCancelamentoDecidido, dadosCancelamentoDecidido{Tx: r.ID}); err != nil {
		return nil, err
	}
	r.Ativa = false

	if _, souParticipante := e.participacoes[r.ID]; souParticipante {
		if _, err := cancelarParticipacao(e, r.ID); err != nil {
			return nil, err
		}
	}

	avisos := make([]Aviso, 0, len(r.Participantes))
	for _, empresa := range r.Participantes {
		if empresa == e.empresa {
			continue
		}
		aviso, err := registrarAviso(e, empresa, AvisoCancelarPernas, r.ID, "")
		if err != nil {
			return nil, err
		}
		avisos = append(avisos, aviso.copia())
	}
	return avisos, nil
}

// usaCarona responde se algum item consome a carona indicada.
func usaCarona(itens []ItemReserva, caronaID string) bool {
	for _, item := range itens {
		if item.CaronaID == caronaID {
			return true
		}
	}
	return false
}

// cancelarCarona cancela a carona do motorista e propaga o cancelamento às
// reservas que dependem dela (PROTOCOL.md, seção 5.7; PROJETO_P2.md,
// seção 10.2; D29, D39). Devolve quantas transações tiveram a perna local
// cancelada **nesta chamada** — é o estrago imediato que o motorista observa,
// não quantas reservas já estão, de fato, fechadas em coordenadores remotos
// (essas ainda dependem do aviso chegar, seção 10.3) — mais os avisos
// recém-criados, que quem chamou entrega fora do lock.
//
// Quatro pontos que costumam passar despercebidos:
//
//   - uma participação em PREPARADO usando a carona recusa o cancelamento
//     por inteiro, com ErrCaronaEmTransacao: cancelar não pode abortar quem
//     já votou SIM (D39), então o motorista tenta de novo em alguns segundos;
//   - a cascata cancela **todas** as pernas locais de cada transação
//     atingida, mesmo em outra carona desta empresa — do contrário um
//     assento ficaria órfão, reservado por ninguém e invendável para sempre;
//   - o prazo de uma hora do passageiro (D13) **não** se aplica aqui: ele
//     protege o motorista contra desistência de última hora, e usá-lo para
//     impedir a cascata inverteria a proteção;
//   - quando esta empresa coordena a transação atingida, a reserva é
//     decidida cancelada aqui mesmo; quando não, um aviso CARONA_CANCELADA é
//     enfileirado para o coordenador remoto decidir (D29).
//
// Tudo em uma seção crítica só, como manda a seção 7 do PROJETO.md: em nenhum
// instante observável existe carona cancelada com participação PREPARADO
// pendurada, nem assento devolvido pela metade.
func cancelarCarona(e *Estado, caronaID, motoristaID string, agora time.Time) (int, []Aviso, error) {
	c, ok := e.caronas[caronaID]
	if !ok {
		return 0, nil, fmt.Errorf("%w: %q", ErrCaronaNaoEncontrada, caronaID)
	}
	if c.MotoristaID != motoristaID {
		return 0, nil, fmt.Errorf("%w: carona %q", ErrNaoEDono, caronaID)
	}
	if c.Cancelada {
		return 0, nil, fmt.Errorf("%w: %q", ErrCaronaCancelada, caronaID)
	}

	partida := c.Horarios[0]
	if agora.After(partida) {
		return 0, nil, &ErroPrazoCancelamento{Partida: partida}
	}

	// D39: participação em PREPARADO usando a carona recusa por inteiro, sem
	// gravar nada. Esperar o PREPARADO decidir é mais simples do que abortar
	// quem já votou SIM, e a espera é de segundos.
	for _, pt := range e.participacoes {
		if pt.Situacao == SituacaoPreparado && usaCarona(pt.Itens, caronaID) {
			return 0, nil, fmt.Errorf("%w: %q", ErrCaronaEmTransacao, caronaID)
		}
	}

	// Transações atingidas: participações CONFIRMADO desta carona (o dono
	// decide por elas) e reservas ativas desta empresa cujo itinerário usa a
	// carona (a cascata entre caronas da mesma empresa, D29) — a interseção é
	// o caso comum de empresa coordenadora e participante da mesma transação.
	atingidas := map[string]bool{}
	for _, pt := range e.participacoes {
		if pt.Situacao == SituacaoConfirmado && usaCarona(pt.Itens, caronaID) {
			atingidas[pt.TxID] = true
		}
	}
	for _, r := range e.reservas {
		if r.Ativa && usaCarona(r.Itens, caronaID) {
			atingidas[r.ID] = true
		}
	}

	// D22, regra 3: o registro é gravado antes de qualquer mudança.
	if err := gravar(e, tipoCaronaCancelada, dadosCaronaCancelada{Carona: caronaID}); err != nil {
		return 0, nil, err
	}
	c.Cancelada = true

	txIDs := make([]string, 0, len(atingidas))
	for txID := range atingidas {
		txIDs = append(txIDs, txID)
	}
	sort.Strings(txIDs)

	canceladas := 0
	var todosOsAvisos []Aviso
	for _, txID := range txIDs {
		if _, souParticipante := e.participacoes[txID]; souParticipante {
			if _, err := cancelarParticipacao(e, txID); err != nil {
				return 0, nil, err
			}
			canceladas++
		}

		coordenador, err := EmpresaDoID(txID)
		if err != nil {
			return 0, nil, err
		}
		if coordenador != e.empresa {
			// Não coordenamos esta transação: avisa quem coordena, sem o
			// prazo do passageiro (assimetria D13) — a decisão é dele.
			aviso, err := registrarAviso(e, coordenador, AvisoCaronaCancelada, txID, caronaID)
			if err != nil {
				return 0, nil, err
			}
			todosOsAvisos = append(todosOsAvisos, aviso.copia())
			continue
		}

		// Coordenamos: decide o cancelamento da reserva agora, sem o prazo de
		// uma hora do passageiro (D13) — ele protege o motorista, não o
		// contrário, e aqui quem cancela é o motorista.
		r, existe := e.reservas[txID]
		if !existe || !r.Ativa {
			continue // já decidida por outro caminho, nada a fazer de novo
		}
		avisos, err := decidirCancelamentoDaReserva(e, r)
		if err != nil {
			return 0, nil, err
		}
		todosOsAvisos = append(todosOsAvisos, avisos...)
	}

	return canceladas, todosOsAvisos, nil
}
