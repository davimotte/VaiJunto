package dominio

import (
	"errors"
	"testing"
	"time"
)

func fusoBrasilia() *time.Location {
	return time.FixedZone("-03:00", -3*60*60)
}

// TestDerivarRotaEHorarios_SalvadorVitoriaDaConquista confere o exemplo de
// PROTOCOL.md (seção 5.4): publicar Salvador → Vitória da Conquista às 08:00
// deve gerar os quatro horários 08:00, 10:00, 13:00 e 15:30, na ordem do
// corredor (D09).
func TestDerivarRotaEHorarios_SalvadorVitoriaDaConquista(t *testing.T) {
	fuso := fusoBrasilia()
	partida := time.Date(2026, 9, 15, 8, 0, 0, 0, fuso)

	rota, horarios, err := DerivarRotaEHorarios("Salvador", "Vitória da Conquista", partida)
	if err != nil {
		t.Fatalf("DerivarRotaEHorarios: %v", err)
	}

	rotaEsperada := []string{"Salvador", "Feira de Santana", "Jequié", "Vitória da Conquista"}
	if len(rota) != len(rotaEsperada) {
		t.Fatalf("rota com tamanho errado: got %v, want %v", rota, rotaEsperada)
	}
	for i, cidade := range rotaEsperada {
		if rota[i] != cidade {
			t.Fatalf("rota[%d] = %q, want %q (rota completa: %v)", i, rota[i], cidade, rota)
		}
	}

	horariosEsperados := []time.Time{
		time.Date(2026, 9, 15, 8, 0, 0, 0, fuso),
		time.Date(2026, 9, 15, 10, 0, 0, 0, fuso),
		time.Date(2026, 9, 15, 13, 0, 0, 0, fuso),
		time.Date(2026, 9, 15, 15, 30, 0, 0, fuso),
	}
	if len(horarios) != len(horariosEsperados) {
		t.Fatalf("horarios com tamanho errado: got %v, want %v", horarios, horariosEsperados)
	}
	for i, h := range horariosEsperados {
		if !horarios[i].Equal(h) {
			t.Fatalf("horarios[%d] = %v, want %v", i, horarios[i], h)
		}
	}
}

// TestDerivarRotaEHorarios_SentidoInverso confere que o corredor deriva
// horários corretos também no sentido oposto (D09: "nos dois sentidos, com
// durações simétricas"), caso de car-5 em dados/caronas.json.
func TestDerivarRotaEHorarios_SentidoInverso(t *testing.T) {
	fuso := fusoBrasilia()
	partida := time.Date(2026, 9, 15, 8, 0, 0, 0, fuso)

	rota, horarios, err := DerivarRotaEHorarios("Vitória da Conquista", "Salvador", partida)
	if err != nil {
		t.Fatalf("DerivarRotaEHorarios: %v", err)
	}

	rotaEsperada := []string{"Vitória da Conquista", "Jequié", "Feira de Santana", "Salvador"}
	for i, cidade := range rotaEsperada {
		if rota[i] != cidade {
			t.Fatalf("rota[%d] = %q, want %q (rota completa: %v)", i, rota[i], cidade, rota)
		}
	}

	chegadaSalvador := time.Date(2026, 9, 15, 15, 30, 0, 0, fuso)
	if !horarios[3].Equal(chegadaSalvador) {
		t.Fatalf("chegada em Salvador = %v, want %v", horarios[3], chegadaSalvador)
	}
}

// TestDerivarRotaEHorarios_CidadeDesconhecida confere que cidade fora do
// corredor devolve o sentinela ErrCidadeDesconhecida. A comparação é por
// errors.Is porque o domínio embrulha o sentinela com %w para dizer qual
// cidade falhou (PROJETO.md, seção 5.3).
func TestDerivarRotaEHorarios_CidadeDesconhecida(t *testing.T) {
	_, _, err := DerivarRotaEHorarios("Ilhéus", "Salvador", time.Now())
	if !errors.Is(err, ErrCidadeDesconhecida) {
		t.Fatalf("err = %v, want ErrCidadeDesconhecida", err)
	}
}

// TestDerivarRotaEHorarios_OrigemIgualDestino confere ErrRotaInvalida quando
// origem e destino coincidem.
func TestDerivarRotaEHorarios_OrigemIgualDestino(t *testing.T) {
	_, _, err := DerivarRotaEHorarios("Jequié", "Jequié", time.Now())
	if !errors.Is(err, ErrRotaInvalida) {
		t.Fatalf("err = %v, want ErrRotaInvalida", err)
	}
}

// TestDerivarRotaEHorarios_GrafiaDivergente confere que a comparação é por
// igualdade exata (PROTOCOL.md seção 3): grafia em minúsculas, sem acento ou
// com espaço extra não é aceita como a cidade canônica — os clientes
// oficiais escolhem a cidade em um menu, nunca digitam o nome.
func TestDerivarRotaEHorarios_GrafiaDivergente(t *testing.T) {
	casos := []string{"jequié", "Jequie", " Jequié", "Jequié "}
	for _, origem := range casos {
		_, _, err := DerivarRotaEHorarios(origem, "Salvador", time.Now())
		if !errors.Is(err, ErrCidadeDesconhecida) {
			t.Fatalf("origem %q: err = %v, want ErrCidadeDesconhecida", origem, err)
		}
	}
}

// TestCarregarCaronas_Car1ChegaEmJequieAs1100 usa o cenário de demonstração
// (PROJETO.md, seção 9.2): car-1 parte de Salvador às 06:00 rumo a Jequié e
// deve chegar às 11:00.
func TestCarregarCaronas_Car1ChegaEmJequieAs1100(t *testing.T) {
	caronas, err := CarregarCaronas("../../dados/caronas.json")
	if err != nil {
		t.Fatalf("CarregarCaronas: %v", err)
	}

	car1, ok := caronas["car-1"]
	if !ok {
		t.Fatalf("car-1 não encontrada; caronas carregadas: %d", len(caronas))
	}

	rotaEsperada := []string{"Salvador", "Feira de Santana", "Jequié"}
	if len(car1.Rota) != len(rotaEsperada) {
		t.Fatalf("rota de car-1 com tamanho errado: got %v, want %v", car1.Rota, rotaEsperada)
	}
	for i, cidade := range rotaEsperada {
		if car1.Rota[i] != cidade {
			t.Fatalf("rota de car-1 [%d] = %q, want %q", i, car1.Rota[i], cidade)
		}
	}

	chegadaJequie := time.Date(2026, 9, 15, 11, 0, 0, 0, fusoBrasilia())
	if !car1.Horarios[2].Equal(chegadaJequie) {
		t.Fatalf("chegada de car-1 em Jequié = %v, want %v", car1.Horarios[2], chegadaJequie)
	}

	if len(car1.Livres) != 2 || car1.Livres[0] != car1.Assentos || car1.Livres[1] != car1.Assentos {
		t.Fatalf("Livres de car-1 deveria iniciar igual a Assentos (%d) em todo trecho: got %v", car1.Assentos, car1.Livres)
	}
	if car1.Cancelada {
		t.Fatalf("car-1 não deveria iniciar cancelada")
	}
}

// TestCarregarEstado confere que os dois arquivos de carga do cenário de
// demonstração são lidos sem erro e populam o Estado inicial, sem nenhuma
// reserva (D02, D07).
func TestCarregarEstado(t *testing.T) {
	estado, err := CarregarEstado("../../dados/usuarios.json", "../../dados/caronas.json")
	if err != nil {
		t.Fatalf("CarregarEstado: %v", err)
	}

	if len(estado.usuarios) == 0 {
		t.Fatalf("nenhum usuário carregado")
	}
	if len(estado.caronas) == 0 {
		t.Fatalf("nenhuma carona carregada")
	}
	if len(estado.reservas) != 0 {
		t.Fatalf("estado inicial não deveria ter reservas: %d", len(estado.reservas))
	}

	joao, ok := estado.usuarios["joao"]
	if !ok {
		t.Fatalf("usuário joao não encontrado")
	}
	if joao.Perfil != "MOTORISTA" {
		t.Fatalf("perfil de joao = %q, want MOTORISTA", joao.Perfil)
	}
}

// --- Regras de carona (camada 5) e camada de estado (camada 6) ---

// estadoDeTeste monta um Estado com um motorista, um passageiro e nenhuma
// carona, para exercitar as regras sem depender de dados/.
func estadoDeTeste() *Estado {
	e := NovoEstado()
	e.usuarios["joao"] = &Usuario{Usuario: "joao", Senha: "1234", Nome: "João Silva", Perfil: "MOTORISTA"}
	e.usuarios["maria"] = &Usuario{Usuario: "maria", Senha: "abcd", Nome: "Maria Souza", Perfil: "PASSAGEIRO"}
	return e
}

// TestAutenticar confere D12: credenciais conferidas contra a carga fixa, e a
// mesma resposta para usuário inexistente e para senha errada.
func TestAutenticar(t *testing.T) {
	e := estadoDeTeste()

	u, err := e.Autenticar("joao", "1234")
	if err != nil {
		t.Fatalf("Autenticar(joao): %v", err)
	}
	if u.Perfil != "MOTORISTA" || u.Nome != "João Silva" {
		t.Fatalf("usuário devolvido = %+v", u)
	}

	for _, caso := range []struct{ usuario, senha string }{
		{"joao", "errada"},
		{"ninguem", "1234"},
		{"", ""},
	} {
		if _, err := e.Autenticar(caso.usuario, caso.senha); !errors.Is(err, ErrCredenciaisInvalidas) {
			t.Errorf("Autenticar(%q,%q): err = %v, want ErrCredenciaisInvalidas", caso.usuario, caso.senha, err)
		}
	}
}

// TestPublicarCarona_DerivaRotaEInicializaLivres confere a seção 5.4 do
// PROTOCOL.md: o motorista informa origem, destino e partida, e o servidor
// deriva rota e horários, com Livres começando igual a Assentos em cada
// trecho.
func TestPublicarCarona_DerivaRotaEInicializaLivres(t *testing.T) {
	e := estadoDeTeste()
	fuso := fusoBrasilia()
	partida := time.Date(2026, 9, 15, 8, 0, 0, 0, fuso)
	agora := time.Date(2026, 9, 1, 0, 0, 0, 0, fuso)

	c, err := e.PublicarCarona("joao", "Salvador", "Vitória da Conquista", partida, 3, []int{3000, 5000, 4000}, agora)
	if err != nil {
		t.Fatalf("PublicarCarona: %v", err)
	}

	if len(c.Rota) != 4 || len(c.Horarios) != 4 {
		t.Fatalf("rota/horários com tamanho errado: %v / %v", c.Rota, c.Horarios)
	}
	chegada := time.Date(2026, 9, 15, 15, 30, 0, 0, fuso)
	if !c.Horarios[3].Equal(chegada) {
		t.Fatalf("chegada = %v, want %v", c.Horarios[3], chegada)
	}
	for i, livres := range c.Livres {
		if livres != 3 {
			t.Fatalf("Livres[%d] = %d, want 3 (todos: %v)", i, livres, c.Livres)
		}
	}
	if c.MotoristaID != "joao" {
		t.Fatalf("MotoristaID = %q, want joao", c.MotoristaID)
	}
	if c.ID == "" {
		t.Fatalf("carona publicada sem identificador")
	}
}

// TestPublicarCarona_Validacoes percorre as validações da seção 5.4, cada uma
// com o sentinela que a seção 6 espera. Preço negativo e assento zero são
// "fora de faixa", que a tabela de erros classifica como CAMPO_INVALIDO.
func TestPublicarCarona_Validacoes(t *testing.T) {
	fuso := fusoBrasilia()
	agora := time.Date(2026, 9, 1, 0, 0, 0, 0, fuso)
	futuro := time.Date(2026, 9, 15, 8, 0, 0, 0, fuso)

	casos := []struct {
		nome     string
		origem   string
		destino  string
		partida  time.Time
		assentos int
		precos   []int
		querido  error
	}{
		{"cidade fora do corredor", "Ilhéus", "Salvador", futuro, 3, []int{3000}, ErrCidadeDesconhecida},
		{"origem igual ao destino", "Jequié", "Jequié", futuro, 3, []int{3000}, ErrRotaInvalida},
		{"preços a menos", "Salvador", "Vitória da Conquista", futuro, 3, []int{3000, 5000}, ErrRotaInvalida},
		{"preços a mais", "Salvador", "Feira de Santana", futuro, 3, []int{3000, 5000}, ErrRotaInvalida},
		{"preço negativo", "Salvador", "Feira de Santana", futuro, 3, []int{-1}, ErrPrecoInvalido},
		{"sem assentos", "Salvador", "Feira de Santana", futuro, 0, []int{3000}, ErrAssentosInvalidos},
		{"partida no passado", "Salvador", "Feira de Santana", agora.Add(-time.Hour), 3, []int{3000}, ErrPartidaInvalida},
		{"partida igual a agora", "Salvador", "Feira de Santana", agora, 3, []int{3000}, ErrPartidaInvalida},
	}

	for _, caso := range casos {
		e := estadoDeTeste()
		_, err := e.PublicarCarona("joao", caso.origem, caso.destino, caso.partida, caso.assentos, caso.precos, agora)
		if !errors.Is(err, caso.querido) {
			t.Errorf("%s: err = %v, want %v", caso.nome, err, caso.querido)
		}
		// D07 aplicado à publicação: validação antes de qualquer escrita,
		// então uma recusa não pode deixar carona pela metade no estado.
		if len(e.caronas) != 0 {
			t.Errorf("%s: recusa deixou %d carona(s) no estado", caso.nome, len(e.caronas))
		}
	}
}

// TestPublicarCarona_NaoCompartilhaMemoriaComOChamador confere que a fatia de
// preços é copiada ao entrar no estado. Sem a cópia, quem publicou continuaria
// com um ponteiro para dentro da estrutura protegida pelo mutex e poderia
// alterar o preço de uma carona já publicada, sem lock nenhum.
func TestPublicarCarona_NaoCompartilhaMemoriaComOChamador(t *testing.T) {
	e := estadoDeTeste()
	fuso := fusoBrasilia()
	precos := []int{3000}

	c, err := e.PublicarCarona("joao", "Salvador", "Feira de Santana",
		time.Date(2026, 9, 15, 8, 0, 0, 0, fuso), 2, precos,
		time.Date(2026, 9, 1, 0, 0, 0, 0, fuso))
	if err != nil {
		t.Fatalf("PublicarCarona: %v", err)
	}

	precos[0] = 999999
	c.Livres[0] = 999999

	guardada, _, err := e.DetalharCarona(c.ID, "joao")
	if err != nil {
		t.Fatalf("DetalharCarona: %v", err)
	}
	if guardada.PrecoTrecho[0] != 3000 {
		t.Errorf("preço no estado = %d, want 3000: a fatia do chamador não foi copiada", guardada.PrecoTrecho[0])
	}
	if guardada.Livres[0] != 2 {
		t.Errorf("Livres no estado = %d, want 2: a carona devolvida não era uma cópia", guardada.Livres[0])
	}
}

// TestCaronasDoMotorista_FiltraPorDonoEOrdena confere a seção 5.5: cada
// motorista vê só as suas caronas, em ordem de partida.
func TestCaronasDoMotorista_FiltraPorDonoEOrdena(t *testing.T) {
	e := estadoDeTeste()
	e.usuarios["carlos"] = &Usuario{Usuario: "carlos", Senha: "1234", Nome: "Carlos Lima", Perfil: "MOTORISTA"}
	fuso := fusoBrasilia()
	agora := time.Date(2026, 9, 1, 0, 0, 0, 0, fuso)

	publicar := func(motorista string, hora int) {
		t.Helper()
		if _, err := e.PublicarCarona(motorista, "Salvador", "Feira de Santana",
			time.Date(2026, 9, 15, hora, 0, 0, 0, fuso), 2, []int{3000}, agora); err != nil {
			t.Fatalf("PublicarCarona: %v", err)
		}
	}
	publicar("joao", 14)
	publicar("joao", 8)
	publicar("carlos", 10)

	minhas := e.CaronasDoMotorista("joao", false)
	if len(minhas) != 2 {
		t.Fatalf("joao deveria ver 2 caronas, viu %d", len(minhas))
	}
	if minhas[0].Horarios[0].Hour() != 8 || minhas[1].Horarios[0].Hour() != 14 {
		t.Fatalf("caronas fora de ordem de partida: %v e %v", minhas[0].Horarios[0], minhas[1].Horarios[0])
	}
	for _, c := range minhas {
		if c.MotoristaID != "joao" {
			t.Fatalf("carona de outro motorista na lista: %+v", c)
		}
	}

	if n := len(e.CaronasDoMotorista("carlos", false)); n != 1 {
		t.Fatalf("carlos deveria ver 1 carona, viu %d", n)
	}
	if n := len(e.CaronasDoMotorista("maria", false)); n != 0 {
		t.Fatalf("maria não publicou nada, deveria ver 0 caronas, viu %d", n)
	}
}

// TestDetalharCarona_DonoEInexistente confere a seção 5.6: carona de outro
// motorista e carona inexistente têm erros distintos.
func TestDetalharCarona_DonoEInexistente(t *testing.T) {
	e := estadoDeTeste()
	fuso := fusoBrasilia()
	c, err := e.PublicarCarona("joao", "Salvador", "Jequié",
		time.Date(2026, 9, 15, 8, 0, 0, 0, fuso), 2, []int{3000, 4500},
		time.Date(2026, 9, 1, 0, 0, 0, 0, fuso))
	if err != nil {
		t.Fatalf("PublicarCarona: %v", err)
	}

	carona, passageiros, err := e.DetalharCarona(c.ID, "joao")
	if err != nil {
		t.Fatalf("DetalharCarona pelo dono: %v", err)
	}
	if len(passageiros) != len(carona.Rota)-1 {
		t.Fatalf("detalhe com %d trechos, want %d", len(passageiros), len(carona.Rota)-1)
	}
	for t2, lista := range passageiros {
		if len(lista) != 0 {
			t.Fatalf("trecho %d deveria estar sem passageiros, tem %d", t2, len(lista))
		}
	}

	if _, _, err := e.DetalharCarona(c.ID, "carlos"); !errors.Is(err, ErrNaoEDono) {
		t.Errorf("dono errado: err = %v, want ErrNaoEDono", err)
	}
	if _, _, err := e.DetalharCarona("car-inexistente", "joao"); !errors.Is(err, ErrCaronaNaoEncontrada) {
		t.Errorf("carona inexistente: err = %v, want ErrCaronaNaoEncontrada", err)
	}
}

// TestDetalharCarona_ListaPassageirosPorTrecho confere RF04 com uma reserva
// montada à mão no estado: o passageiro aparece só nos trechos que ele ocupa.
//
// A reserva é inserida direto porque RESERVAR ainda não existe; o que se
// testa aqui é a leitura que o motorista faz, não a escrita da reserva.
func TestDetalharCarona_ListaPassageirosPorTrecho(t *testing.T) {
	e := estadoDeTeste()
	fuso := fusoBrasilia()
	c, err := e.PublicarCarona("joao", "Salvador", "Vitória da Conquista",
		time.Date(2026, 9, 15, 8, 0, 0, 0, fuso), 3, []int{3000, 5000, 4000},
		time.Date(2026, 9, 1, 0, 0, 0, 0, fuso))
	if err != nil {
		t.Fatalf("PublicarCarona: %v", err)
	}

	// Maria viaja de Salvador (0) a Jequié (2): ocupa os trechos 0 e 1.
	e.reservas["res-1"] = &Reserva{
		ID: "res-1", PassageiroID: "maria", Ativa: true,
		Itens: []ItemReserva{{CaronaID: c.ID, De: 0, Ate: 2}},
	}
	// Reserva inativa não deve aparecer para ninguém.
	e.reservas["res-2"] = &Reserva{
		ID: "res-2", PassageiroID: "maria", Ativa: false,
		Itens: []ItemReserva{{CaronaID: c.ID, De: 2, Ate: 3}},
	}

	_, passageiros, err := e.DetalharCarona(c.ID, "joao")
	if err != nil {
		t.Fatalf("DetalharCarona: %v", err)
	}

	for _, trecho := range []int{0, 1} {
		if len(passageiros[trecho]) != 1 {
			t.Fatalf("trecho %d: %d passageiros, want 1", trecho, len(passageiros[trecho]))
		}
		p := passageiros[trecho][0]
		if p.ReservaID != "res-1" || p.Usuario != "maria" || p.Nome != "Maria Souza" {
			t.Fatalf("trecho %d: passageiro = %+v", trecho, p)
		}
	}
	if len(passageiros[2]) != 0 {
		t.Fatalf("trecho 2 deveria estar vazio (a reserva que o cobre está inativa): %+v", passageiros[2])
	}
}
