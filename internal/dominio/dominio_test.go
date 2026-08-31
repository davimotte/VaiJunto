package dominio

import (
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

// TestDerivarRotaEHorarios_CidadeDesconhecida confere o código de erro do
// PROTOCOL.md (seção 6) para cidade fora do corredor.
func TestDerivarRotaEHorarios_CidadeDesconhecida(t *testing.T) {
	_, _, err := DerivarRotaEHorarios("Ilhéus", "Salvador", time.Now())
	erroDominio, ok := err.(*ErroDominio)
	if !ok {
		t.Fatalf("err = %v (%T), want *ErroDominio", err, err)
	}
	if erroDominio.Codigo != CodigoCidadeDesconhecida {
		t.Fatalf("Codigo = %q, want %q", erroDominio.Codigo, CodigoCidadeDesconhecida)
	}
}

// TestDerivarRotaEHorarios_OrigemIgualDestino confere ROTA_INVALIDA quando
// origem e destino coincidem (seção 6).
func TestDerivarRotaEHorarios_OrigemIgualDestino(t *testing.T) {
	_, _, err := DerivarRotaEHorarios("Jequié", "Jequié", time.Now())
	erroDominio, ok := err.(*ErroDominio)
	if !ok {
		t.Fatalf("err = %v (%T), want *ErroDominio", err, err)
	}
	if erroDominio.Codigo != CodigoRotaInvalida {
		t.Fatalf("Codigo = %q, want %q", erroDominio.Codigo, CodigoRotaInvalida)
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
		erroDominio, ok := err.(*ErroDominio)
		if !ok {
			t.Fatalf("origem %q: err = %v (%T), want *ErroDominio", origem, err, err)
		}
		if erroDominio.Codigo != CodigoCidadeDesconhecida {
			t.Fatalf("origem %q: Codigo = %q, want %q", origem, erroDominio.Codigo, CodigoCidadeDesconhecida)
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
