package dominio

import (
	"errors"
	"testing"
	"time"
)

// Testes das regras de reserva e de cancelamento (PROJETO.md, seção 7;
// PROTOCOL.md, seções 5.7, 5.9 e 5.11).
//
// Rodam sobre o cenário versionado de dados/caronas.json, pelo mesmo motivo do
// teste de busca: um cenário reconstruído em código passaria a valer sobre
// dados que ninguém executa. Os horários citados nos comentários são os da
// seção 9.2, escritos em dados/caronas.json.
//
// A diferença destes testes para os cenários T1, T2, T5 e T8 de testes/ é o
// relógio. Aqui `agora` é um parâmetro, o que permite fixar o instante em que a
// operação acontece e cobrir as regras de prazo de forma determinística —
// coisa que um teste por socket não consegue, porque a borda lê time.Now().

// as devolve um instante de 01/10/2026, o dia das caronas do cenário, no fuso
// de Brasília. Serve para posicionar a operação em relação às partidas.
func as(hora, minuto int) time.Time {
	return time.Date(2026, 10, 1, hora, minuto, 0, 0, fusoBrasilia())
}

// reservar faz, dentro de uma empresa só, o que o coordenador faz pela rede
// (seção 7.1): prepara, valida o encadeamento sobre as pernas que o dono
// devolveu e confirma.
//
// Existe porque o Reservar do P1 não existe mais (D07 revogada): as regras que
// estes testes cobrem continuam todas valendo, mas se espalharam pelos passos
// do 2PC. Reunir os passos aqui mantém os casos legíveis e, principalmente,
// mantém a **regressão**: cada itinerário que o P1 recusava tem de continuar
// recusado, agora pelo passo que herdou a regra.
//
// Os passos estão na ordem do coordenador de verdade, e o abort também: uma
// tentativa que falha no encadeamento ou na sobreposição já tinha descontado
// assentos no PREPARAR, e é o ABORTAR que os devolve. É por isso que os testes
// de recusa podem afirmar, depois, que nenhum contador se moveu.
func reservar(e *Estado, passageiroID string, itens []ItemReserva, agora time.Time) (Reserva, Itinerario, error) {
	if err := ValidarItens(itens); err != nil {
		return Reserva{}, Itinerario{}, err
	}
	txID, err := GerarIDTransacao(e.Empresa())
	if err != nil {
		return Reserva{}, Itinerario{}, err
	}
	participantes := []string{e.Empresa()}

	pernas, err := e.Preparar(PedidoPreparacao{
		TxID:          txID,
		Coordenador:   e.Empresa(),
		Participantes: participantes,
		Carimbo:       Carimbo{Lamport: 1, Empresa: e.Empresa()},
		Passageiro:    Passageiro{Ref: e.Empresa() + ":" + passageiroID, Nome: passageiroID},
		Itens:         itens,
	}, agora)
	if err != nil {
		return Reserva{}, Itinerario{}, err
	}

	if err := ValidarEncadeamento(pernas); err != nil {
		_, _ = e.Abortar(txID)
		return Reserva{}, Itinerario{}, err
	}

	r, it, err := e.ConfirmarReserva(txID, passageiroID, participantes, itens, pernas, agora)
	if err != nil {
		_, _ = e.Abortar(txID)
		return Reserva{}, Itinerario{}, err
	}
	if _, err := e.Confirmar(txID); err != nil {
		return Reserva{}, Itinerario{}, err
	}
	return r, it, nil
}

// reservarNoCenario é o atalho das reservas bem-sucedidas, que aparecem como
// preparação em quase todos os testes de cancelamento.
func reservarNoCenario(t *testing.T, e *Estado, passageiro string, itens ...ItemReserva) Reserva {
	t.Helper()
	r, _, err := reservar(e, passageiro, itens, as(0, 0))
	if err != nil {
		t.Fatalf("reservar(%s, %v): %v", passageiro, itens, err)
	}
	return r
}

// desativarReservaNoTeste marca uma reserva como inativa sem passar pelo
// cancelamento de verdade (D39) — mais rápido para testes que só precisam de
// muitas reservas inativas, como o do limite de listagem, e que não têm
// nenhum interesse no efeito sobre assentos ou outbox.
//
// Mexe no estado direto porque é teste do mesmo pacote.
func desativarReservaNoTeste(t *testing.T, e *Estado, reservaID string) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	r, existe := e.reservas[reservaID]
	if !existe {
		t.Fatalf("desativarReservaNoTeste: reserva %q não existe", reservaID)
	}
	r.Ativa = false
}

// item monta um ItemReserva, só para deixar as tabelas de caso legíveis.
func item(caronaID string, de, ate int) ItemReserva {
	return ItemReserva{CaronaID: caronaID, De: de, Ate: ate}
}

// livresDe lê os contadores por trecho de uma carona pela camada de estado.
func livresDe(t *testing.T, e *Estado, motorista, caronaID string) []int {
	t.Helper()
	c, _, err := e.DetalharCarona(caronaID, motorista)
	if err != nil {
		t.Fatalf("DetalharCarona(%s): %v", caronaID, err)
	}
	return c.Livres
}

// TestReservar_BaldeacaoDaCargaDecrementaSoOsTrechosUsados confere o caminho
// feliz do exemplo do PROTOCOL.md (seção 5.9): car-1 de Salvador a Jequié mais
// car-2 até Vitória da Conquista, 10000 centavos no total — o itinerário #3 da
// seção 9.2 do PROJETO.md, a baldeação do enunciado.
//
// A afirmação que importa é a última: car-3 e car-4, que partem de Feira de
// Santana, cidade por onde car-1 passa, não podem ter perdido assento.
// Disponibilidade é por trecho (RF11, D10), e uma implementação que
// decrementasse a carona inteira, ou que errasse a faixa [De, Ate), apareceria
// aqui como assento sumindo onde ninguém embarcou.
func TestReservar_BaldeacaoDaCargaDecrementaSoOsTrechosUsados(t *testing.T) {
	e := estadoDoCenario(t)

	reserva, itinerario, err := reservar(e, "maria", []ItemReserva{item("vaijunto:car-1", 0, 2), item("vaijunto:car-2", 0, 1)}, as(0, 0))
	if err != nil {
		t.Fatalf("Reservar: %v", err)
	}

	if reserva.ID == "" || !reserva.Ativa || reserva.PassageiroID != "maria" {
		t.Fatalf("reserva mal formada: %+v", reserva)
	}
	if itinerario.PrecoTotalCentavos != 10000 {
		t.Errorf("preço total = %d, want 10000", itinerario.PrecoTotalCentavos)
	}
	if !itinerario.Partida.Equal(as(6, 0)) || !itinerario.Chegada.Equal(as(14, 30)) {
		t.Errorf("intervalo do itinerário = [%v, %v], want [06:00, 14:30]", itinerario.Partida, itinerario.Chegada)
	}

	// car-1 tem 3 assentos e os dois trechos foram usados; car-2 tem 2 e só
	// tem um trecho.
	if got := livresDe(t, e, "joao", "vaijunto:car-1"); got[0] != 2 || got[1] != 2 {
		t.Errorf("car-1 livres = %v, want [2 2]", got)
	}
	if got := livresDe(t, e, "carlos", "vaijunto:car-2"); got[0] != 1 {
		t.Errorf("car-2 livres = %v, want [1]", got)
	}
	if got := livresDe(t, e, "ana", "vaijunto:car-3"); got[0] != 2 {
		t.Errorf("car-3 livres = %v, want [2] — a reserva mexeu em carona que não usou", got)
	}
	if got := livresDe(t, e, "ana", "vaijunto:car-4"); got[0] != 1 {
		t.Errorf("car-4 livres = %v, want [1] — a reserva mexeu em carona que não usou", got)
	}
}

// TestReservar_Recusas percorre os passos 1 e 2 do algoritmo da seção 7 do
// PROJETO.md, um caso por regra.
//
// Os três últimos casos são os controles negativos do cenário da seção 9.2
// aplicados à reserva, e não à busca: car-6 fica 15 minutos depois de car-1,
// car-7 fica 24 horas depois, e car-8 leva o passageiro de volta a Salvador
// antes de car-5. Eles precisam ser recusados aqui também — a busca e a reserva
// aplicam as mesmas regras de propósito (D09, D13), e uma reserva mais frouxa
// que a busca deixaria o cliente confirmar, por chamada direta ao protocolo, um
// itinerário que a busca nunca ofereceria.
func TestReservar_Recusas(t *testing.T) {
	casos := []struct {
		nome     string
		itens    []ItemReserva
		esperado error
	}{
		{"lista vazia", nil, ErrItemInvalido},
		{"carona inexistente", []ItemReserva{item("vaijunto:car-99", 0, 1)}, ErrCaronaNaoEncontrada},
		// Novo no P2: sem prefixo de empresa não há como saber a quem pedir o
		// assento (D27), e a recusa vem antes de qualquer transação.
		{"identificador sem empresa", []ItemReserva{item("car-1", 0, 1)}, ErrIDSemEmpresa},
		{"de igual a ate", []ItemReserva{item("vaijunto:car-1", 1, 1)}, ErrItemInvalido},
		{"de maior que ate", []ItemReserva{item("vaijunto:car-1", 2, 1)}, ErrItemInvalido},
		{"de negativo", []ItemReserva{item("vaijunto:car-1", -1, 1)}, ErrItemInvalido},
		{"ate além da rota", []ItemReserva{item("vaijunto:car-1", 0, 3)}, ErrItemInvalido},
		{"mesma carona duas vezes", []ItemReserva{item("vaijunto:car-1", 0, 1), item("vaijunto:car-1", 1, 2)}, ErrItinerarioInvalido},
		{"não encadeia no espaço", []ItemReserva{item("vaijunto:car-1", 0, 1), item("vaijunto:car-2", 0, 1)}, ErrItinerarioInvalido},
		{"folga abaixo da margem (car-6)", []ItemReserva{item("vaijunto:car-1", 0, 2), item("vaijunto:car-6", 0, 1)}, ErrItinerarioInvalido},
		{"espera acima do teto (car-7)", []ItemReserva{item("vaijunto:car-1", 0, 2), item("vaijunto:car-7", 2, 3)}, ErrItinerarioInvalido},
		{"volta a cidade visitada (car-8)", []ItemReserva{item("vaijunto:car-1", 0, 1), item("vaijunto:car-8", 0, 1), item("vaijunto:car-5", 1, 2)}, ErrItinerarioInvalido},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			e := estadoDoCenario(t)

			_, _, err := reservar(e, "maria", caso.itens, as(0, 0))
			if !errors.Is(err, caso.esperado) {
				t.Fatalf("erro = %v, want %v", err, caso.esperado)
			}

			// Nenhuma recusa pode ter escrito nada (passo 5 da seção 7).
			if got := livresDe(t, e, "joao", "vaijunto:car-1"); got[0] != 3 || got[1] != 3 {
				t.Errorf("car-1 livres = %v, want [3 3]: a recusa escreveu antes de validar tudo", got)
			}
		})
	}
}

// TestReservar_RecusaItinerarioQueVoltaACidade leva à reserva o contraexemplo
// da seção 6 do PROJETO.md: A (Salvador → Feira), B (Feira → Salvador) e C
// (Salvador → Conquista) encadeiam no espaço e no tempo, e são três caronas
// distintas — passam em todas as regras da reserva exceto a de cidade
// repetida (seção 7, passo 2).
//
// A busca nunca oferece esse itinerário, mas o cliente pode mandá-lo direto
// pelo protocolo. Uma reserva mais frouxa que a busca aceitaria o que a busca
// recusa, pelo mesmo motivo que as margens de baldeação são constantes únicas
// (D13).
//
// As caronas são montadas em código, e não lidas de dados/: o cenário da seção
// 9.2 não tem nenhum par de caronas capaz de voltar a uma cidade.
func TestReservar_RecusaItinerarioQueVoltaACidade(t *testing.T) {
	e := estadoDeTeste()
	a := publicarNoDia(t, e, []string{"Salvador", "Feira de Santana"}, as(6, 0), as(8, 0))
	b := publicarNoDia(t, e, []string{"Feira de Santana", "Salvador"}, as(8, 30), as(10, 30))
	c := publicarNoDia(t, e, []string{"Salvador", "Vitória da Conquista"}, as(11, 0), as(18, 30))

	_, _, err := reservar(e, "maria", []ItemReserva{item(a, 0, 1), item(b, 0, 1), item(c, 0, 1)}, as(0, 0))
	if !errors.Is(err, ErrItinerarioInvalido) {
		t.Fatalf("erro = %v, want ErrItinerarioInvalido", err)
	}

	// Nenhuma escrita antes de toda a validação passar (seção 7, passo 5).
	for _, id := range []string{a, b, c} {
		if got := livresDe(t, e, "joao", id); got[0] != 2 {
			t.Errorf("%s livres = %v, want [2]: a recusa escreveu antes de validar tudo", id, got)
		}
	}
	if n := len(e.ReservasDoPassageiro("maria", true)); n != 0 {
		t.Errorf("recusa criou %d reserva(s)", n)
	}
}

// TestReservar_RecusaPernaQueAtravessaCidadeVisitada confere D-e na reserva:
// contam as cidades por onde o passageiro passa dentro do veículo, e não só as
// de embarque e desembarque.
//
// A leva de Salvador a Jequié; B vai de Jequié a Feira passando por Salvador; C
// segue de Feira a Conquista. O item B de 0 a 2 embarca e desembarca em
// cidades novas, mas atravessa Salvador.
//
// O controle positivo usa o mesmo estado: B de 1 a 2, embarcando já em
// Salvador, seguido de C, não revisita nada e precisa ser aceito. É o que
// mostra que a regra recusa o trecho que atravessa a cidade, e não a carona.
func TestReservar_RecusaPernaQueAtravessaCidadeVisitada(t *testing.T) {
	e := estadoDeTeste()
	a := publicarNoDia(t, e, []string{"Salvador", "Jequié"}, as(6, 0), as(8, 0))
	b := publicarNoDia(t, e, []string{"Jequié", "Salvador", "Feira de Santana"}, as(8, 30), as(9, 30), as(10, 30))
	c := publicarNoDia(t, e, []string{"Feira de Santana", "Vitória da Conquista"}, as(11, 0), as(14, 0))

	_, _, err := reservar(e, "maria", []ItemReserva{item(a, 0, 1), item(b, 0, 2), item(c, 0, 1)}, as(0, 0))
	if !errors.Is(err, ErrItinerarioInvalido) {
		t.Fatalf("erro = %v, want ErrItinerarioInvalido", err)
	}
	if got := livresDe(t, e, "joao", b); got[0] != 2 || got[1] != 2 {
		t.Errorf("%s livres = %v, want [2 2]: a recusa escreveu antes de validar tudo", b, got)
	}

	if _, _, err := reservar(e, "maria", []ItemReserva{item(b, 1, 2), item(c, 0, 1)}, as(0, 0)); err != nil {
		t.Fatalf("B de Salvador a Feira + C deveria ser aceito: %v", err)
	}
	if got := livresDe(t, e, "joao", b); got[0] != 2 || got[1] != 1 {
		t.Errorf("%s livres = %v, want [2 1]: só o trecho Salvador → Feira foi reservado", b, got)
	}
}

// TestReservar_SemAssentoNaoDecrementaAPernaDisponivel é o argumento de
// atomicidade no nível do domínio, e o par determinístico do cenário T2.
//
// car-4 tem um único assento. Com ele já ocupado, um itinerário que use car-1 e
// car-4 tem que falhar **inteiro**: car-1, que tinha assento de sobra, não pode
// ter sido decrementado no caminho. É a separação entre os passos 1–4 e o passo
// 6 que garante isso, e é por ela que nenhum rollback é necessário (RNF06).
func TestReservar_SemAssentoNaoDecrementaAPernaDisponivel(t *testing.T) {
	e := estadoDoCenario(t)

	// maria toma o assento único de car-4 (Feira de Santana → Jequié).
	reservarNoCenario(t, e, "maria", item("vaijunto:car-4", 0, 1))

	// pedro tenta chegar lá via car-1 até Feira (06:00 → 07:45) e car-4 a
	// partir das 08:15, folga de 30 min, encadeamento válido. Só falta assento.
	_, _, err := reservar(e, "pedro", []ItemReserva{item("vaijunto:car-1", 0, 1), item("vaijunto:car-4", 0, 1)}, as(0, 0))
	if !errors.Is(err, ErrSemAssento) {
		t.Fatalf("erro = %v, want ErrSemAssento", err)
	}

	var detalhe *ErroSemAssento
	if !errors.As(err, &detalhe) {
		t.Fatalf("erro %v não carrega ErroSemAssento; PROTOCOL.md 5.9 exige dizer qual trecho esgotou", err)
	}
	if detalhe.CaronaID != "vaijunto:car-4" || detalhe.IndiceTrecho != 0 {
		t.Errorf("detalhe = %+v, want car-4 trecho 0", detalhe)
	}

	if got := livresDe(t, e, "joao", "vaijunto:car-1"); got[0] != 3 {
		t.Errorf("car-1 livres = %v, want [3 3]: houve reserva parcial", got)
	}
}

// TestReservar_ConflitoDeHorarioDoMesmoPassageiro confere D14, e é o par
// determinístico do cenário T8.
//
// car-1 ocupa maria das 06:00 às 10:45 e car-4 vai das 08:15 às 11:15: os
// intervalos se cruzam, e ninguém viaja em dois veículos ao mesmo tempo. A
// comparação é sobre o itinerário inteiro, não trecho a trecho.
//
// O último caso é o complemento necessário: car-2 parte às 12:15, depois de a
// primeira reserva terminar, e precisa ser aceito. Uma implementação que
// recusasse qualquer segunda reserva também passaria nas duas primeiras
// afirmações.
func TestReservar_ConflitoDeHorarioDoMesmoPassageiro(t *testing.T) {
	e := estadoDoCenario(t)

	primeira := reservarNoCenario(t, e, "maria", item("vaijunto:car-1", 0, 2))

	_, _, err := reservar(e, "maria", []ItemReserva{item("vaijunto:car-4", 0, 1)}, as(0, 0))
	if !errors.Is(err, ErrConflitoHorario) {
		t.Fatalf("erro = %v, want ErrConflitoHorario", err)
	}

	var detalhe *ErroConflitoHorario
	if !errors.As(err, &detalhe) {
		t.Fatalf("erro %v não carrega ErroConflitoHorario; PROTOCOL.md 5.9 exige dizer qual reserva conflita", err)
	}
	if detalhe.ReservaID != primeira.ID {
		t.Errorf("reserva conflitante = %q, want %q", detalhe.ReservaID, primeira.ID)
	}

	// O mesmo passageiro em outro veículo, mas em período que não se cruza.
	if _, _, err := reservar(e, "maria", []ItemReserva{item("vaijunto:car-2", 0, 1)}, as(0, 0)); err != nil {
		t.Fatalf("reserva em período livre recusada: %v", err)
	}

	// E o conflito é por passageiro: pedro não é afetado pela reserva de maria.
	if _, _, err := reservar(e, "pedro", []ItemReserva{item("vaijunto:car-4", 0, 1)}, as(0, 0)); err != nil {
		t.Fatalf("reserva de outro passageiro recusada: %v", err)
	}
}

// TestCancelarReserva_DevolveAssentosDeTodosOsTrechos confere o efeito da
// seção 5.11 do PROTOCOL.md e da seção 10.1 do PROJETO_P2.md (D39): a reserva
// fica inativa e cada trecho que ela consumia recupera um assento, nas duas
// caronas — aqui as duas são desta mesma empresa, que é dona e coordenadora
// ao mesmo tempo, então a perna local é cancelada sem nenhum aviso de rede.
func TestCancelarReserva_DevolveAssentosDeTodosOsTrechos(t *testing.T) {
	e := estadoDoCenario(t)

	reserva := reservarNoCenario(t, e, "maria", item("vaijunto:car-1", 0, 2), item("vaijunto:car-2", 0, 1))

	// 04:00 do dia da viagem: duas horas antes da partida das 06:00, dentro do
	// prazo de uma hora.
	if _, err := e.CancelarReserva(reserva.ID, "maria", as(4, 0)); err != nil {
		t.Fatalf("CancelarReserva: %v", err)
	}

	if got := livresDe(t, e, "joao", "vaijunto:car-1"); got[0] != 3 || got[1] != 3 {
		t.Errorf("car-1 livres = %v, want [3 3]", got)
	}
	if got := livresDe(t, e, "carlos", "vaijunto:car-2"); got[0] != 2 {
		t.Errorf("car-2 livres = %v, want [2]", got)
	}

	ativas := e.ReservasDoPassageiro("maria", false)
	if len(ativas) != 0 {
		t.Errorf("maria ainda tem %d reserva(s) ativa(s)", len(ativas))
	}
	todas := e.ReservasDoPassageiro("maria", true)
	if len(todas) != 1 || todas[0].Ativa {
		t.Errorf("a reserva cancelada deveria aparecer inativa em incluir_canceladas: %+v", todas)
	}
}

// TestCancelarReserva_PrazoDoPassageiro fixa a fronteira de
// ANTECEDENCIA_CANCELAMENTO (D13) sobre a partida das 06:00 de car-1.
//
// O instante exato do limite conta como dentro do prazo: a regra é "até 1 hora
// antes da partida", e não "menos de 1 hora antes". Testar apenas um caso longe
// da fronteira deixaria passar um erro de um segundo em qualquer direção, que é
// a forma que este bug realmente toma.
func TestCancelarReserva_PrazoDoPassageiro(t *testing.T) {
	casos := []struct {
		nome      string
		agora     time.Time
		permitido bool
	}{
		{"bem antes", as(0, 0), true},
		{"no limite exato", as(5, 0), true},
		{"um minuto depois do limite", as(5, 1), false},
		{"depois da partida", as(7, 0), false},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			e := estadoDoCenario(t)
			reserva := reservarNoCenario(t, e, "maria", item("vaijunto:car-1", 0, 2))

			_, err := e.CancelarReserva(reserva.ID, "maria", caso.agora)
			if caso.permitido {
				if err != nil {
					t.Fatalf("dentro do prazo: CancelarReserva: %v", err)
				}
				return
			}

			if !errors.Is(err, ErrPrazoCancelamentoExpirado) {
				t.Fatalf("erro = %v, want ErrPrazoCancelamentoExpirado", err)
			}

			var detalhe *ErroPrazoCancelamento
			if !errors.As(err, &detalhe) || !detalhe.Partida.Equal(as(6, 0)) {
				t.Errorf("detalhe do prazo = %+v, want partida 06:00", detalhe)
			}
			// Recusa não escreve: o assento continua ocupado.
			if got := livresDe(t, e, "joao", "vaijunto:car-1"); got[0] != 2 {
				t.Errorf("car-1 livres = %v, want [2 2]: a recusa devolveu assento", got)
			}
		})
	}
}

// TestCancelarReserva_Recusas cobre os outros três erros da seção 5.11.
func TestCancelarReserva_Recusas(t *testing.T) {
	e := estadoDoCenario(t)
	reserva := reservarNoCenario(t, e, "maria", item("vaijunto:car-1", 0, 2))

	if _, err := e.CancelarReserva("res-inexistente", "maria", as(0, 0)); !errors.Is(err, ErrReservaNaoEncontrada) {
		t.Errorf("reserva inexistente: erro = %v, want ErrReservaNaoEncontrada", err)
	}
	if _, err := e.CancelarReserva(reserva.ID, "pedro", as(0, 0)); !errors.Is(err, ErrNaoEDono) {
		t.Errorf("reserva de outro passageiro: erro = %v, want ErrNaoEDono", err)
	}
	// A recusa não pode ter escrito nada.
	if got := livresDe(t, e, "joao", "vaijunto:car-1"); got[0] != 2 {
		t.Errorf("car-1 livres = %v, want [2 2]: a recusa devolveu assento", got)
	}

	// Dono e prazo corretos: cancela de verdade.
	if _, err := e.CancelarReserva(reserva.ID, "maria", as(0, 0)); err != nil {
		t.Fatalf("CancelarReserva: %v", err)
	}
	if got := livresDe(t, e, "joao", "vaijunto:car-1"); got[0] != 3 {
		t.Errorf("car-1 livres = %v, want [3 3] após o cancelamento", got)
	}

	// ErrReservaJaCancelada: segunda tentativa sobre a mesma reserva.
	if _, err := e.CancelarReserva(reserva.ID, "maria", as(0, 0)); !errors.Is(err, ErrReservaJaCancelada) {
		t.Errorf("reserva já cancelada: erro = %v, want ErrReservaJaCancelada", err)
	}
}

// TestCancelarCarona_PrazoDoMotoristaVaiAteAPartida fixa a outra fronteira de
// D13, e a assimetria entre os dois prazos.
//
// O motorista pode cancelar **até a partida**, e não até uma hora antes: às
// 05:30 o passageiro já não pode mais desistir de car-1, mas o motorista ainda
// pode cancelar. São prazos diferentes de propósito, porque protegem lados
// diferentes.
func TestCancelarCarona_PrazoDoMotoristaVaiAteAPartida(t *testing.T) {
	casos := []struct {
		nome      string
		agora     time.Time
		permitido bool
	}{
		{"meia hora antes da partida", as(5, 30), true},
		{"no instante exato da partida", as(6, 0), true},
		{"um minuto depois da partida", as(6, 1), false},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			e := estadoDoCenario(t)

			_, _, err := e.CancelarCarona("vaijunto:car-1", "joao", caso.agora)
			if caso.permitido {
				if err != nil {
					t.Fatalf("CancelarCarona: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrPrazoCancelamentoExpirado) {
				t.Fatalf("erro = %v, want ErrPrazoCancelamentoExpirado", err)
			}
		})
	}
}

// TestCancelarCarona_CascataDevolveAssentosDeCaronasVizinhas é o teste central
// da seção 5.7, e o par determinístico do cenário T5.
//
// maria viaja em car-1 e car-2; pedro só em car-2. O motorista cancela car-1 às
// 05:50, dez minutos antes da partida — instante escolhido de propósito, porque
// é o intervalo em que o prazo do passageiro **já expirou**. As três afirmações
// que o teste faz são as três regras que se encontram aqui:
//
//   - a cascata ignora o prazo de uma hora (D13): a reserva de maria cai mesmo
//     ela já não podendo cancelá-la sozinha;
//   - o assento de maria em car-2 volta, embora car-2 não tenha sido cancelada.
//     Sem isso ele ficaria órfão: reservado por ninguém e invendável para
//     sempre, o que quebra I1 e RNF07;
//   - a reserva de pedro, que não usa car-1, não é tocada. A cascata atinge as
//     reservas que dependem da carona, e não todas as reservas do sistema.
func TestCancelarCarona_CascataDevolveAssentosDeCaronasVizinhas(t *testing.T) {
	e := estadoDoCenario(t)

	daMaria := reservarNoCenario(t, e, "maria", item("vaijunto:car-1", 0, 2), item("vaijunto:car-2", 0, 1))
	doPedro := reservarNoCenario(t, e, "pedro", item("vaijunto:car-2", 0, 1))

	// car-2 tem 2 assentos e os dois estão ocupados neste ponto.
	if got := livresDe(t, e, "carlos", "vaijunto:car-2"); got[0] != 0 {
		t.Fatalf("preparação: car-2 livres = %v, want [0]", got)
	}
	// maria já não conseguiria cancelar sozinha às 05:50.
	if _, err := e.CancelarReserva(daMaria.ID, "maria", as(5, 50)); !errors.Is(err, ErrPrazoCancelamentoExpirado) {
		t.Fatalf("preparação: o prazo da maria deveria estar expirado, erro = %v", err)
	}

	canceladas, _, err := e.CancelarCarona("vaijunto:car-1", "joao", as(5, 50))
	if err != nil {
		t.Fatalf("CancelarCarona: %v", err)
	}
	if canceladas != 1 {
		t.Errorf("reservas canceladas = %d, want 1", canceladas)
	}

	// I1 na carona cancelada: o assento volta mesmo não valendo mais nada,
	// porque a invariante é o que o verificador confere depois.
	if got := livresDe(t, e, "joao", "vaijunto:car-1"); got[0] != 3 || got[1] != 3 {
		t.Errorf("car-1 livres = %v, want [3 3]", got)
	}
	// O ponto do teste: car-2 recupera o assento da maria e mantém o do pedro.
	if got := livresDe(t, e, "carlos", "vaijunto:car-2"); got[0] != 1 {
		t.Errorf("car-2 livres = %v, want [1]: a cascata deixou assento órfão na carona vizinha", got)
	}

	if ativas := e.ReservasDoPassageiro("maria", false); len(ativas) != 0 {
		t.Errorf("a reserva da maria sobreviveu ao cancelamento da carona: %+v", ativas)
	}
	ativasPedro := e.ReservasDoPassageiro("pedro", false)
	if len(ativasPedro) != 1 || ativasPedro[0].ID != doPedro.ID {
		t.Errorf("a reserva do pedro, que não usa car-1, foi atingida pela cascata: %+v", ativasPedro)
	}
}

// TestCancelarCarona_Recusas cobre os erros restantes da seção 5.7 e confere
// que uma recusa não cancela reserva nenhuma.
func TestCancelarCarona_Recusas(t *testing.T) {
	e := estadoDoCenario(t)
	reservarNoCenario(t, e, "maria", item("vaijunto:car-1", 0, 2))

	if _, _, err := e.CancelarCarona("car-99", "joao", as(0, 0)); !errors.Is(err, ErrCaronaNaoEncontrada) {
		t.Errorf("carona inexistente: erro = %v, want ErrCaronaNaoEncontrada", err)
	}
	// car-1 é do joao.
	if _, _, err := e.CancelarCarona("vaijunto:car-1", "carlos", as(0, 0)); !errors.Is(err, ErrNaoEDono) {
		t.Errorf("carona de outro motorista: erro = %v, want ErrNaoEDono", err)
	}
	if ativas := e.ReservasDoPassageiro("maria", false); len(ativas) != 1 {
		t.Errorf("uma recusa cancelou reservas: maria tem %d ativa(s), want 1", len(ativas))
	}

	// car-3 não tem reserva nenhuma, então o cancelamento passa normalmente.
	if _, _, err := e.CancelarCarona("vaijunto:car-3", "ana", as(0, 0)); err != nil {
		t.Fatalf("CancelarCarona(car-3): %v", err)
	}
	if _, _, err := e.CancelarCarona("vaijunto:car-3", "ana", as(0, 0)); !errors.Is(err, ErrCaronaCancelada) {
		t.Errorf("segundo cancelamento: erro = %v, want ErrCaronaCancelada", err)
	}
	// A reserva da maria, em car-1, não foi tocada por nada disso.
	if got := livresDe(t, e, "joao", "vaijunto:car-1"); got[0] != 2 {
		t.Errorf("car-1 livres = %v, want [2 2]", got)
	}
}

// TestCancelarCarona_EmTransacao_Recusa confere a recusa da D39 (seção 10.2):
// uma participação em PREPARADO usando a carona bloqueia o cancelamento por
// inteiro, sem gravar nada — cancelar não pode abortar quem já votou SIM.
func TestCancelarCarona_EmTransacao_Recusa(t *testing.T) {
	e := estadoDoCenario(t)
	prepararNoCenario(t, e, txDeTeste, item("vaijunto:car-1", 0, 1))

	_, _, err := e.CancelarCarona("vaijunto:car-1", "joao", as(0, 0))
	if !errors.Is(err, ErrCaronaEmTransacao) {
		t.Fatalf("erro = %v, want ErrCaronaEmTransacao", err)
	}

	// A recusa não gravou nada: a carona segue válida e o assento, em espera.
	c, _, err := e.DetalharCarona("vaijunto:car-1", "joao")
	if err != nil {
		t.Fatalf("DetalharCarona: %v", err)
	}
	if c.Cancelada {
		t.Errorf("a recusa marcou a carona como cancelada")
	}
	if got := livresDe(t, e, "joao", "vaijunto:car-1"); got[0] != 2 {
		t.Errorf("car-1 livres = %v, want [2 2]: o assento em espera voltou ao mercado", got)
	}

	// Confirmada a transação, o cancelamento passa a valer normalmente.
	if _, err := e.Confirmar(txDeTeste); err != nil {
		t.Fatalf("Confirmar: %v", err)
	}
	if _, _, err := e.CancelarCarona("vaijunto:car-1", "joao", as(0, 0)); err != nil {
		t.Fatalf("CancelarCarona após confirmar: %v", err)
	}
}

// TestReservar_CaronaCanceladaReprova confere o passo 3 da seção 7: carona
// cancelada reprova a reserva, mesmo com assentos livres sobrando — e eles
// sobram justamente porque a cascata os devolveu.
func TestReservar_CaronaCanceladaReprova(t *testing.T) {
	e := estadoDoCenario(t)

	if _, _, err := e.CancelarCarona("vaijunto:car-1", "joao", as(0, 0)); err != nil {
		t.Fatalf("CancelarCarona: %v", err)
	}

	_, _, err := reservar(e, "maria", []ItemReserva{item("vaijunto:car-1", 0, 2)}, as(0, 0))
	if !errors.Is(err, ErrCaronaCancelada) {
		t.Fatalf("erro = %v, want ErrCaronaCancelada", err)
	}
}

// TestReservasDoPassageiro_LimiteCortaDepoisDeOrdenar confere o limite das
// listagens (D16) nas reservas: no máximo MAXIMO_ITENS_LISTAGEM, as ativas
// antes das canceladas, e o corte depois da ordenação.
//
// Foi o T4 que expôs a falta deste limite: um passageiro com centenas de
// reservas canceladas recebia uma linha acima de 64 KB. Como no teste das
// caronas, as canceladas são as de partida mais cedo, para que uma ordenação
// só por partida as pusesse na frente das ativas.
func TestReservasDoPassageiro_LimiteCortaDepoisDeOrdenar(t *testing.T) {
	e := estadoDeTeste()
	const total, canceladas = MAXIMO_ITENS_LISTAGEM + 5, 10

	// Uma carona por dia: as reservas nunca se sobrepõem no tempo (D14).
	ids := make([]string, total)
	for i := range ids {
		partida := as(6, 0).AddDate(0, 0, i)
		c, err := e.PublicarCarona("joao", []string{"Salvador", "Feira de Santana"},
			[]time.Time{partida, partida.Add(2 * time.Hour)}, 2, []int{3000}, agoraDaPublicacao())
		if err != nil {
			t.Fatalf("PublicarCarona %d: %v", i, err)
		}
		r, _, err := reservar(e, "maria", []ItemReserva{item(c.ID, 0, 1)}, agoraDaPublicacao())
		if err != nil {
			t.Fatalf("Reservar %d: %v", i, err)
		}
		ids[i] = r.ID
	}
	// As reservas são desativadas direto no estado, e não por CancelarReserva,
	// que na fase 5 responde recusa provisória (D39 chega na fase 8). O que
	// este teste afirma é a ordenação e o corte da listagem, não o caminho do
	// cancelamento — é o mesmo recurso de cancelarParticipacaoNoTeste, que
	// monta uma situação CANCELADO à mão por não haver quem a produza ainda.
	for _, id := range ids[:canceladas] {
		desativarReservaNoTeste(t, e, id)
	}

	lista := e.ReservasDoPassageiro("maria", true)
	if len(lista) != MAXIMO_ITENS_LISTAGEM {
		t.Fatalf("len = %d, want %d", len(lista), MAXIMO_ITENS_LISTAGEM)
	}
	ativas := total - canceladas
	for i, r := range lista {
		if querAtiva := i < ativas; r.Ativa != querAtiva {
			t.Fatalf("posição %d: ativa = %v, want %v — as reservas ativas vêm antes das canceladas",
				i, r.Ativa, querAtiva)
		}
	}

	if n := len(e.ReservasDoPassageiro("maria", false)); n != ativas {
		t.Fatalf("sem as canceladas: len = %d, want %d", n, ativas)
	}
}

// TestValidarEncadeamento_RecusaOsControlesNegativosDoFixture prende as duas
// leituras da regra de encadeamento ao mesmo resultado.
//
// A regra existe em dois lugares de propósito (ver o comentário de
// ValidarEncadeamento): na busca ela é poda incremental dentro da DFS, e aqui é
// conferência final sobre um itinerário completo. São algoritmos diferentes
// sobre a mesma regra, e é exatamente o arranjo em que uma divergência passa
// despercebida — a busca ofereceria um itinerário que a reserva recusa, e o
// passageiro veria a opção sumir entre a consulta e a confirmação.
//
// Os três casos são os controles negativos do fixture do P1 (seção 12.4), os
// mesmos que a busca já recusa: car-6 viola a margem, car-7 estoura o teto de
// espera, car-8 fecha um ciclo. Se um dia a busca passar a aceitar qualquer um
// deles, este teste continua provando que a reserva não aceita.
func TestValidarEncadeamento_RecusaOsControlesNegativosDoFixture(t *testing.T) {
	// Pernas montadas com os horários do fixture, no formato autoritativo que
	// os donos devolvem no voto (seção 3.3 da API).
	perna := func(carona string, cidades []string, partida, chegada time.Time) Perna {
		return Perna{
			CaronaID: carona, Empresa: "vaijunto", MotoristaNome: "João Silva",
			De: 0, Ate: len(cidades) - 1, Cidades: cidades,
			Partida: partida, Chegada: chegada, PrecoCentavos: 3000,
		}
	}

	casos := []struct {
		nome   string
		pernas []Perna
	}{
		{
			// car-1 chega a Jequié 10:45; esta parte 11:00, 15 min depois.
			"folga abaixo da margem (car-6)",
			[]Perna{
				perna("vaijunto:car-1", []string{"Salvador", "Feira de Santana", "Jequié"}, as(6, 0), as(10, 45)),
				perna("vaijunto:car-6", []string{"Jequié", "Vitória da Conquista"}, as(11, 0), as(13, 0)),
			},
		},
		{
			// Parte no dia seguinte: 24 h de espera, acima do teto de 12 h.
			"espera acima do teto (car-7)",
			[]Perna{
				perna("vaijunto:car-1", []string{"Salvador", "Feira de Santana", "Jequié"}, as(6, 0), as(10, 45)),
				perna("vaijunto:car-7", []string{"Jequié", "Vitória da Conquista"},
					as(10, 45).AddDate(0, 0, 1), as(13, 0).AddDate(0, 0, 1)),
			},
		},
		{
			// Volta a Salvador, por onde o itinerário já passou.
			"ciclo pela cidade de origem (car-8)",
			[]Perna{
				perna("vaijunto:car-1", []string{"Salvador", "Feira de Santana"}, as(6, 0), as(7, 45)),
				perna("vaijunto:car-8", []string{"Feira de Santana", "Salvador"}, as(9, 0), as(11, 0)),
			},
		},
		{
			// Encadeia no tempo, mas não no espaço.
			"desembarque diferente do embarque seguinte",
			[]Perna{
				perna("vaijunto:car-1", []string{"Salvador", "Feira de Santana"}, as(6, 0), as(7, 45)),
				perna("vaijunto:car-2", []string{"Jequié", "Vitória da Conquista"}, as(9, 0), as(11, 0)),
			},
		},
		{
			// Cidade intermediária repetida: o passageiro passa duas vezes por
			// Feira de Santana, uma delas sem desembarcar. É o caso que só as
			// Cidades completas da Perna permitem pegar (D42, seção 3.3).
			"cidade intermediária repetida",
			[]Perna{
				perna("vaijunto:car-1", []string{"Salvador", "Feira de Santana"}, as(6, 0), as(7, 45)),
				perna("vaijunto:car-5", []string{"Feira de Santana", "Jequié"}, as(9, 0), as(11, 0)),
				perna("vaijunto:car-3", []string{"Jequié", "Feira de Santana", "Vitória da Conquista"},
					as(12, 0), as(16, 0)),
			},
		},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			if err := ValidarEncadeamento(caso.pernas); !errors.Is(err, ErrItinerarioInvalido) {
				t.Fatalf("erro = %v, want ErrItinerarioInvalido", err)
			}
		})
	}
}

// TestValidarEncadeamento_AceitaOItinerarioDaDemonstracao é o contraponto: as
// margens do cenário da seção 12.2, que a demonstração D-1 percorre, passam.
//
// Sem este caso o teste acima poderia ser satisfeito por uma função que recusa
// tudo.
func TestValidarEncadeamento_AceitaOItinerarioDaDemonstracao(t *testing.T) {
	// 90 min de folga em cada baldeação, como na seção 12.2.
	pernas := []Perna{
		{CaronaID: "vaijunto:car-1", Empresa: "vaijunto", De: 0, Ate: 2,
			Cidades: []string{"Salvador", "Feira de Santana", "Vitória da Conquista"},
			Partida: as(5, 0), Chegada: as(12, 30), PrecoCentavos: 9500},
		{CaronaID: "rotacapixaba:car-1", Empresa: "rotacapixaba", De: 0, Ate: 2,
			Cidades: []string{"Vitória da Conquista", "Vitória", "Cachoeiro de Itapemirim"},
			Partida: as(14, 0), Chegada: as(6, 30).AddDate(0, 0, 1), PrecoCentavos: 21500},
		{CaronaID: "rotafluminense:car-1", Empresa: "rotafluminense", De: 0, Ate: 2,
			Cidades: []string{"Cachoeiro de Itapemirim", "Rio de Janeiro", "Petrópolis"},
			Partida: as(8, 0).AddDate(0, 0, 1), Chegada: as(14, 30).AddDate(0, 0, 1), PrecoCentavos: 12000},
	}
	if err := ValidarEncadeamento(pernas); err != nil {
		t.Fatalf("o itinerário da demonstração foi recusado: %v", err)
	}
}
