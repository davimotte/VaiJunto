package testes

// Contrato de protocolo das operações de reserva e cancelamento
// (PROTOCOL.md, seções 5.7, 5.9, 5.10 e 5.11), exercitado por socket.
//
// Os testes de internal/dominio já cobrem as regras; o que se confere aqui é o
// que só existe na fronteira: os códigos da seção 6, o campo "dados" que
// acompanha os três erros detalhados, e o encaixe entre buscar, reservar,
// listar e cancelar em uma única conexão, do jeito que o cliente de menu vai
// usar (D15).

import (
	"encoding/json"
	"testing"
	"time"

	"vaijunto/internal/protocolo"
)

// TestSessaoCompletaDoPassageiro percorre o exemplo da seção 7 do PROTOCOL.md
// em uma conexão só: buscar, reservar o itinerário escolhido, listar e
// cancelar.
//
// O passageiro devolve ao servidor exatamente os campos carona_id, de e ate
// que recebeu da busca, sem nunca digitar identificador (D15). É o que este
// teste imita: os trechos do pedido saem do resultado da busca, e não de
// constantes escritas aqui.
func TestSessaoCompletaDoPassageiro(t *testing.T) {
	c := conectar(t, subirServidor(t))
	c.entrar("maria", "abcd")

	var busca protocolo.BuscarItinerariosResposta
	c.exigirOK(protocolo.TipoBuscarItinerarios, protocolo.BuscarItinerariosRequisicao{
		Origem:  "Salvador",
		Destino: "Vitória da Conquista",
		Data:    "2026-09-15",
	}, &busca)
	if len(busca.Itinerarios) == 0 {
		t.Fatalf("a busca do cenário da seção 9.2 não devolveu itinerário")
	}

	escolhido := busca.Itinerarios[0]
	itens := make([]protocolo.ItemReserva, 0, len(escolhido.Trechos))
	for _, perna := range escolhido.Trechos {
		itens = append(itens, trecho(perna.CaronaID, perna.De, perna.Ate))
	}

	var confirmada protocolo.ReservarResposta
	c.exigirOK(protocolo.TipoReservar, reserva(itens...), &confirmada)
	if confirmada.ReservaID == "" {
		t.Fatalf("RESERVAR devolveu reserva_id vazio")
	}
	if confirmada.PrecoTotalCentavos != escolhido.PrecoTotalCentavos {
		t.Errorf("preço da reserva = %d, want %d (o mesmo que a busca anunciou)",
			confirmada.PrecoTotalCentavos, escolhido.PrecoTotalCentavos)
	}

	var listadas protocolo.ListarMinhasReservasResposta
	c.exigirOK(protocolo.TipoListarMinhasReservas, protocolo.ListarMinhasReservasRequisicao{}, &listadas)
	if len(listadas.Reservas) != 1 {
		t.Fatalf("maria tem %d reservas ativas, want 1", len(listadas.Reservas))
	}

	guardada := listadas.Reservas[0]
	if guardada.ReservaID != confirmada.ReservaID || !guardada.Ativa {
		t.Errorf("reserva listada = %+v, want a reserva %q ativa", guardada, confirmada.ReservaID)
	}
	if len(guardada.Trechos) != len(escolhido.Trechos) {
		t.Errorf("reserva com %d trechos, want %d", len(guardada.Trechos), len(escolhido.Trechos))
	}
	if !guardada.Partida.Equal(escolhido.Partida) || !guardada.Chegada.Equal(escolhido.Chegada) {
		t.Errorf("intervalo da reserva = [%v, %v], want [%v, %v]",
			guardada.Partida, guardada.Chegada, escolhido.Partida, escolhido.Chegada)
	}
	// O nome do motorista precisa vir resolvido: o passageiro escolhe com quem
	// viaja, e o identificador de login não aparece na tela (D15).
	if guardada.Trechos[0].Motorista == "" {
		t.Errorf("trecho sem nome de motorista: %+v", guardada.Trechos[0])
	}

	var cancelada protocolo.CancelarReservaResposta
	c.exigirOK(protocolo.TipoCancelarReserva,
		protocolo.CancelarReservaRequisicao{ReservaID: confirmada.ReservaID}, &cancelada)
	if cancelada.ReservaID != confirmada.ReservaID {
		t.Errorf("cancelamento devolveu %q, want %q", cancelada.ReservaID, confirmada.ReservaID)
	}

	// Depois de cancelada, some da listagem padrão e reaparece inativa quando
	// pedida com incluir_canceladas.
	c.exigirOK(protocolo.TipoListarMinhasReservas, protocolo.ListarMinhasReservasRequisicao{}, &listadas)
	if len(listadas.Reservas) != 0 {
		t.Errorf("reserva cancelada continua na listagem padrão: %+v", listadas.Reservas)
	}
	c.exigirOK(protocolo.TipoListarMinhasReservas,
		protocolo.ListarMinhasReservasRequisicao{IncluirCanceladas: true}, &listadas)
	if len(listadas.Reservas) != 1 || listadas.Reservas[0].Ativa {
		t.Errorf("com incluir_canceladas a reserva deveria aparecer inativa: %+v", listadas.Reservas)
	}
}

// TestSemAssentoTrazTrechoNoDados confere o formato exato do erro da seção
// 5.9: além do código, o "dados" diz qual carona e qual trecho esgotaram.
//
// É o detalhe que permite ao CLI explicar a recusa em vez de só repeti-la, e
// ele existe porque o domínio sabe qual trecho falhou — a tradução carrega esse
// dado até a borda sem que o domínio conheça o protocolo (PROJETO.md, 5.3).
func TestSemAssentoTrazTrechoNoDados(t *testing.T) {
	endereco := subirServidor(t)

	// car-7 tem um assento só.
	primeiro := conectar(t, endereco)
	primeiro.entrar("maria", "abcd")
	primeiro.exigirOK(protocolo.TipoReservar, reserva(trecho("car-7", 0, 1)), nil)

	segundo := conectar(t, endereco)
	segundo.entrar("pedro", "abcd")

	resp := segundo.enviar(protocolo.TipoReservar, reserva(trecho("car-7", 0, 1)))
	if resp.Codigo != protocolo.CodigoSemAssento {
		t.Fatalf("codigo = %q, want %s (mensagem: %q)", resp.Codigo, protocolo.CodigoSemAssento, resp.Mensagem)
	}

	var dados protocolo.SemAssentoDados
	if err := json.Unmarshal(resp.Dados, &dados); err != nil {
		t.Fatalf("decodificar dados %s: %v", resp.Dados, err)
	}
	if dados.CaronaID != "car-7" || dados.IndiceTrecho != 0 {
		t.Errorf("dados = %+v, want car-7 trecho 0", dados)
	}
}

// TestConflitoHorarioTrazReservaNoDados confere o segundo erro detalhado da
// seção 5.9: o passageiro precisa saber *qual* das reservas dele bloqueia o
// período, ou não tem como decidir se cancela aquela para fazer esta.
func TestConflitoHorarioTrazReservaNoDados(t *testing.T) {
	c := conectar(t, subirServidor(t))
	c.entrar("maria", "abcd")

	// car-1 ocupa maria das 06:00 às 11:00; car-7 vai das 08:30 às 11:30.
	var primeira protocolo.ReservarResposta
	c.exigirOK(protocolo.TipoReservar, reserva(trecho("car-1", 0, 2)), &primeira)

	resp := c.enviar(protocolo.TipoReservar, reserva(trecho("car-7", 0, 1)))
	if resp.Codigo != protocolo.CodigoConflitoHorario {
		t.Fatalf("codigo = %q, want %s (mensagem: %q)", resp.Codigo, protocolo.CodigoConflitoHorario, resp.Mensagem)
	}

	var dados protocolo.ConflitoHorarioDados
	if err := json.Unmarshal(resp.Dados, &dados); err != nil {
		t.Fatalf("decodificar dados %s: %v", resp.Dados, err)
	}
	if dados.ReservaID != primeira.ReservaID {
		t.Errorf("reserva conflitante = %q, want %q", dados.ReservaID, primeira.ReservaID)
	}
}

// TestPrazosDeCancelamentoSaoAssimetricos exercita ponta a ponta as três regras
// que se encontram no cancelamento em cascata (D13), com uma carona publicada a
// meia hora da partida.
//
// Nessa janela o passageiro já perdeu o prazo dele — uma hora — e o motorista
// ainda tem o dele, que vai até a partida. O que o teste fixa:
//
//   - CANCELAR_RESERVA responde PRAZO_CANCELAMENTO_EXPIRADO, com a partida no
//     "dados" (seção 5.11);
//   - CANCELAR_CARONA é aceito e derruba a reserva assim mesmo, porque a
//     cascata ignora o prazo do passageiro;
//   - o assento que a reserva ocupava na carona vizinha, que não foi cancelada,
//     volta a ficar livre.
//
// O relógio aqui é o de verdade: a borda lê time.Now(), então o teste posiciona
// a partida em relação ao presente, e não o contrário.
func TestPrazosDeCancelamentoSaoAssimetricos(t *testing.T) {
	endereco := subirServidor(t)

	// Meia hora até a partida da primeira perna; a segunda parte três horas
	// depois, folga suficiente para a baldeação.
	partidaA := time.Now().In(time.FixedZone("-03:00", -3*60*60)).Add(30 * time.Minute).Truncate(time.Second)
	partidaB := partidaA.Add(3 * time.Hour)

	dono := credencial{"joao", "1234"}
	primeira := publicarComo(t, endereco, dono, "Salvador", "Feira de Santana", partidaA, 2, []int{3000})
	segunda := publicarComo(t, endereco, credencial{"carlos", "1234"}, "Feira de Santana", "Jequié", partidaB, 2, []int{4500})

	passageiro := conectar(t, endereco)
	passageiro.entrar("maria", "abcd")

	var confirmada protocolo.ReservarResposta
	passageiro.exigirOK(protocolo.TipoReservar,
		reserva(trecho(primeira, 0, 1), trecho(segunda, 0, 1)), &confirmada)

	// O prazo do passageiro já passou.
	resp := passageiro.enviar(protocolo.TipoCancelarReserva,
		protocolo.CancelarReservaRequisicao{ReservaID: confirmada.ReservaID})
	if resp.Codigo != protocolo.CodigoPrazoCancelamentoExpirado {
		t.Fatalf("codigo = %q, want %s (mensagem: %q)", resp.Codigo, protocolo.CodigoPrazoCancelamentoExpirado, resp.Mensagem)
	}
	var dados protocolo.PrazoCancelamentoExpiradoDados
	if err := json.Unmarshal(resp.Dados, &dados); err != nil {
		t.Fatalf("decodificar dados %s: %v", resp.Dados, err)
	}
	if !dados.Partida.Equal(partidaA) {
		t.Errorf("partida no dados = %v, want %v", dados.Partida, partidaA)
	}

	// O do motorista, não: ele vai até a partida.
	motorista := conectar(t, endereco)
	motorista.entrar(dono.usuario, dono.senha)

	var cascata protocolo.CancelarCaronaResposta
	motorista.exigirOK(protocolo.TipoCancelarCarona,
		protocolo.CancelarCaronaRequisicao{CaronaID: primeira}, &cascata)
	if cascata.ReservasCanceladas != 1 {
		t.Errorf("reservas_canceladas = %d, want 1 — a cascata respeitou o prazo do passageiro", cascata.ReservasCanceladas)
	}

	var listadas protocolo.ListarMinhasReservasResposta
	passageiro.exigirOK(protocolo.TipoListarMinhasReservas, protocolo.ListarMinhasReservasRequisicao{}, &listadas)
	if len(listadas.Reservas) != 0 {
		t.Errorf("a reserva sobreviveu ao cancelamento da carona: %+v", listadas.Reservas)
	}

	caronas := observarCaronas(t, endereco)
	if livres := caronas[segunda].livres[0]; livres != 2 {
		t.Errorf("carona vizinha: livres = %d, want 2 — a cascata deixou assento órfão", livres)
	}
	verificarInvariantes(t, endereco)
}

// TestRecusasDeReserva confere os códigos da seção 6 nas entradas malformadas e
// nas regras de itinerário, pela borda.
//
// O caso do "de" ausente é o que justifica os campos ponteiro do handler: 0 é
// um valor legítimo para "de", e sem a distinção entre ausente e zero um pedido
// incompleto seria silenciosamente lido como o primeiro trecho da rota.
func TestRecusasDeReserva(t *testing.T) {
	c := conectar(t, subirServidor(t))
	c.entrar("maria", "abcd")

	casos := []struct {
		nome   string
		dados  any
		codigo string
	}{
		{"sem trechos", vazio, protocolo.CodigoCampoInvalido},
		{"trechos vazio", reserva(), protocolo.CodigoCampoInvalido},
		{"trechos com tipo errado", map[string]any{"trechos": 7}, protocolo.CodigoCampoInvalido},
		{"de ausente", map[string]any{"trechos": []any{map[string]any{"carona_id": "car-1", "ate": 1}}}, protocolo.CodigoCampoInvalido},
		{"carona_id vazio", reserva(trecho("", 0, 1)), protocolo.CodigoCampoInvalido},
		{"de igual a ate", reserva(trecho("car-1", 1, 1)), protocolo.CodigoCampoInvalido},
		{"ate além da rota", reserva(trecho("car-1", 0, 3)), protocolo.CodigoCampoInvalido},
		{"carona inexistente", reserva(trecho("car-99", 0, 1)), protocolo.CodigoCaronaNaoEncontrada},
		{"mesma carona duas vezes", reserva(trecho("car-1", 0, 1), trecho("car-1", 1, 2)), protocolo.CodigoItinerarioInvalido},
		{"não encadeia no espaço", reserva(trecho("car-1", 0, 1), trecho("car-2", 0, 1)), protocolo.CodigoItinerarioInvalido},
		{"folga abaixo da margem", reserva(trecho("car-1", 0, 2), trecho("car-4", 0, 1)), protocolo.CodigoItinerarioInvalido},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			c.exigirErro(protocolo.TipoReservar, caso.dados, caso.codigo)
		})
	}
}

// TestRecusasDeCancelamento confere os códigos das seções 5.7 e 5.11 que não
// dependem de prazo, incluindo a distinção entre recurso inexistente e recurso
// de outro usuário.
func TestRecusasDeCancelamento(t *testing.T) {
	endereco := subirServidor(t)

	maria := conectar(t, endereco)
	maria.entrar("maria", "abcd")
	var confirmada protocolo.ReservarResposta
	maria.exigirOK(protocolo.TipoReservar, reserva(trecho("car-7", 0, 1)), &confirmada)

	maria.exigirErro(protocolo.TipoCancelarReserva,
		protocolo.CancelarReservaRequisicao{ReservaID: "res-inexistente"}, protocolo.CodigoReservaNaoEncontrada)
	maria.exigirErro(protocolo.TipoCancelarReserva, vazio, protocolo.CodigoCampoInvalido)

	pedro := conectar(t, endereco)
	pedro.entrar("pedro", "abcd")
	pedro.exigirErro(protocolo.TipoCancelarReserva,
		protocolo.CancelarReservaRequisicao{ReservaID: confirmada.ReservaID}, protocolo.CodigoNaoEDono)

	maria.exigirOK(protocolo.TipoCancelarReserva,
		protocolo.CancelarReservaRequisicao{ReservaID: confirmada.ReservaID}, nil)
	maria.exigirErro(protocolo.TipoCancelarReserva,
		protocolo.CancelarReservaRequisicao{ReservaID: confirmada.ReservaID}, protocolo.CodigoReservaJaCancelada)

	joao := conectar(t, endereco)
	joao.entrar("joao", "1234")
	joao.exigirErro(protocolo.TipoCancelarCarona,
		protocolo.CancelarCaronaRequisicao{CaronaID: "car-99"}, protocolo.CodigoCaronaNaoEncontrada)
	// car-2 é do carlos.
	joao.exigirErro(protocolo.TipoCancelarCarona,
		protocolo.CancelarCaronaRequisicao{CaronaID: "car-2"}, protocolo.CodigoNaoEDono)
	joao.exigirErro(protocolo.TipoCancelarCarona, vazio, protocolo.CodigoCampoInvalido)

	joao.exigirOK(protocolo.TipoCancelarCarona,
		protocolo.CancelarCaronaRequisicao{CaronaID: "car-7"}, nil)
	joao.exigirErro(protocolo.TipoCancelarCarona,
		protocolo.CancelarCaronaRequisicao{CaronaID: "car-7"}, protocolo.CodigoCaronaCancelada)

	// Reservar em carona cancelada reprova, mesmo com o assento devolvido.
	pedro.exigirErro(protocolo.TipoReservar, reserva(trecho("car-7", 0, 1)), protocolo.CodigoCaronaCancelada)
}

// TestPerfisDasNovasOperacoes confere a tabela da seção 5: as três operações de
// reserva são exclusivas do passageiro, e CANCELAR_CARONA do motorista.
func TestPerfisDasNovasOperacoes(t *testing.T) {
	endereco := subirServidor(t)

	motorista := conectar(t, endereco)
	motorista.entrar("joao", "1234")
	for _, tipo := range []string{protocolo.TipoReservar, protocolo.TipoListarMinhasReservas, protocolo.TipoCancelarReserva} {
		motorista.exigirErro(tipo, vazio, protocolo.CodigoPerfilIncorreto)
	}

	passageiro := conectar(t, endereco)
	passageiro.entrar("maria", "abcd")
	passageiro.exigirErro(protocolo.TipoCancelarCarona, vazio, protocolo.CodigoPerfilIncorreto)

	// Sem login, todas exigem autenticação antes de falar de perfil.
	anonimo := conectar(t, endereco)
	for _, tipo := range []string{protocolo.TipoReservar, protocolo.TipoListarMinhasReservas,
		protocolo.TipoCancelarReserva, protocolo.TipoCancelarCarona} {
		anonimo.exigirErro(tipo, vazio, protocolo.CodigoNaoAutenticado)
	}
}
