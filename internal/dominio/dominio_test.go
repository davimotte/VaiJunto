package dominio

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// --- Carga de boot com paradas explícitas (D09) ---

// caronaDeArquivo monta uma entrada de dados/caronas.json como mapa, e não
// como a struct do carregamento, para que os casos de recusa possam remover
// campos e escrever o formato antigo à vontade.
func caronaDeArquivo(id string, rota []string, horarios []string, assentos int, precos []int) map[string]any {
	return map[string]any{
		"id":              id,
		"motorista":       "joao",
		"rota":            rota,
		"horarios":        horarios,
		"assentos":        assentos,
		"precos_centavos": precos,
	}
}

// escreverCaronas grava as entradas num arquivo temporário e devolve o
// caminho. O arquivo some sozinho ao fim do teste (t.TempDir).
func escreverCaronas(t *testing.T, entradas ...map[string]any) string {
	t.Helper()
	b, err := json.Marshal(entradas)
	if err != nil {
		t.Fatalf("serializar caronas: %v", err)
	}
	caminho := filepath.Join(t.TempDir(), "caronas.json")
	if err := os.WriteFile(caminho, b, 0o600); err != nil {
		t.Fatalf("gravar %s: %v", caminho, err)
	}
	return caminho
}

// TestCarregarCaronas_LeRotaEHorariosDoArquivo confere que a carga guarda as
// paradas exatamente como estão no arquivo, sem derivar nada (D09).
//
// A rota e os horários foram escolhidos para que nenhuma derivação pudesse
// produzi-los: Jequié → Salvador → Vitória da Conquista não é uma sequência em
// linha, e 40 minutos entre Jequié e Salvador não é duração de trajeto nenhuma.
// Uma carga que ainda calculasse os horários falharia aqui.
func TestCarregarCaronas_LeRotaEHorariosDoArquivo(t *testing.T) {
	caminho := escreverCaronas(t, caronaDeArquivo("car-x",
		[]string{"Jequié", "Salvador", "Vitória da Conquista"},
		[]string{"2026-09-15T07:00:00-03:00", "2026-09-15T07:40:00-03:00", "2026-09-15T19:00:00-03:00"},
		2, []int{1000, 2000}))

	caronas, err := CarregarCaronas(caminho)
	if err != nil {
		t.Fatalf("CarregarCaronas: %v", err)
	}
	c, ok := caronas["car-x"]
	if !ok {
		t.Fatalf("car-x não carregada; caronas: %v", caronas)
	}

	rotaEsperada := []string{"Jequié", "Salvador", "Vitória da Conquista"}
	if fmt.Sprint(c.Rota) != fmt.Sprint(rotaEsperada) {
		t.Errorf("rota = %v, want %v", c.Rota, rotaEsperada)
	}
	fuso := fusoBrasilia()
	horariosEsperados := []time.Time{
		time.Date(2026, 9, 15, 7, 0, 0, 0, fuso),
		time.Date(2026, 9, 15, 7, 40, 0, 0, fuso),
		time.Date(2026, 9, 15, 19, 0, 0, 0, fuso),
	}
	if len(c.Horarios) != len(horariosEsperados) {
		t.Fatalf("horarios = %v, want %v", c.Horarios, horariosEsperados)
	}
	for i, quero := range horariosEsperados {
		if !c.Horarios[i].Equal(quero) {
			t.Errorf("horarios[%d] = %v, want %v", i, c.Horarios[i], quero)
		}
	}
	if c.MotoristaID != "joao" || c.Assentos != 2 || fmt.Sprint(c.PrecoTrecho) != "[1000 2000]" {
		t.Errorf("carona carregada = %+v", c)
	}
	if fmt.Sprint(c.Livres) != "[2 2]" {
		t.Errorf("Livres = %v, want [2 2]: todo trecho começa com a capacidade inteira", c.Livres)
	}
}

// TestCarregarCaronas_NaoExigePartidaNoFuturo fixa a única validação da
// publicação que a carga não aplica (D09): os dados de demonstração têm data
// fixa, e exigir partida no futuro impediria o servidor de subir depois dela.
func TestCarregarCaronas_NaoExigePartidaNoFuturo(t *testing.T) {
	caminho := escreverCaronas(t, caronaDeArquivo("car-antiga",
		[]string{"Salvador", "Feira de Santana"},
		[]string{"2020-01-10T08:00:00-03:00", "2020-01-10T10:00:00-03:00"},
		3, []int{3000}))

	if _, err := CarregarCaronas(caminho); err != nil {
		t.Fatalf("CarregarCaronas recusou carona no passado: %v", err)
	}
}

// TestCarregarCaronas_RecusaCaronaInvalida percorre as validações de uma
// carona (D09, I6) na porta de entrada do boot.
//
// Elas importam aqui tanto quanto na publicação: sem o corredor, nada mais
// garante por construção que os horários cresçam, e a busca, a reserva e a
// invariante I3 dependem disso. Um arquivo de carga com horário fora de ordem
// não pode virar estado.
//
// Cada caso parte de uma carona válida e estraga uma coisa só, para que a
// recusa só possa ter vindo da regra que o caso nomeia. A mensagem precisa
// citar o id da carona: é o que diz a quem subiu o servidor qual linha do
// arquivo corrigir.
func TestCarregarCaronas_RecusaCaronaInvalida(t *testing.T) {
	valida := func() map[string]any {
		return caronaDeArquivo("car-ruim",
			[]string{"Salvador", "Feira de Santana", "Jequié"},
			[]string{"2026-09-15T06:00:00-03:00", "2026-09-15T08:00:00-03:00", "2026-09-15T11:00:00-03:00"},
			3, []int{3000, 4500})
	}

	casos := []struct {
		nome    string
		estraga func(c map[string]any)
		quero   error
	}{
		{"cidade desconhecida", func(c map[string]any) {
			c["rota"] = []string{"Salvador", "Ilhéus", "Jequié"}
		}, ErrCidadeDesconhecida},
		{"grafia divergente", func(c map[string]any) {
			c["rota"] = []string{"salvador", "Feira de Santana", "Jequié"}
		}, ErrCidadeDesconhecida},
		{"uma parada só", func(c map[string]any) {
			c["rota"] = []string{"Salvador"}
			c["horarios"] = []string{"2026-09-15T06:00:00-03:00"}
			c["precos_centavos"] = []int{}
		}, ErrRotaInvalida},
		{"cidade repetida", func(c map[string]any) {
			c["rota"] = []string{"Salvador", "Feira de Santana", "Salvador"}
		}, ErrRotaInvalida},
		{"horário igual ao anterior", func(c map[string]any) {
			c["horarios"] = []string{"2026-09-15T06:00:00-03:00", "2026-09-15T08:00:00-03:00", "2026-09-15T08:00:00-03:00"}
		}, ErrRotaInvalida},
		{"horário antes do anterior", func(c map[string]any) {
			c["horarios"] = []string{"2026-09-15T06:00:00-03:00", "2026-09-15T05:00:00-03:00", "2026-09-15T11:00:00-03:00"}
		}, ErrRotaInvalida},
		{"mais horários que cidades", func(c map[string]any) {
			c["horarios"] = []string{"2026-09-15T06:00:00-03:00", "2026-09-15T08:00:00-03:00", "2026-09-15T11:00:00-03:00", "2026-09-15T12:00:00-03:00"}
		}, ErrRotaInvalida},
		{"formato antigo, sem rota nem horários", func(c map[string]any) {
			delete(c, "rota")
			delete(c, "horarios")
			c["origem"] = "Salvador"
			c["destino"] = "Jequié"
			c["partida"] = "2026-09-15T06:00:00-03:00"
		}, ErrRotaInvalida},
		{"preços a menos", func(c map[string]any) {
			c["precos_centavos"] = []int{3000}
		}, ErrRotaInvalida},
		{"preço negativo", func(c map[string]any) {
			c["precos_centavos"] = []int{3000, -1}
		}, ErrPrecoInvalido},
		{"sem assentos", func(c map[string]any) {
			c["assentos"] = 0
		}, ErrAssentosInvalidos},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			entrada := valida()
			caso.estraga(entrada)

			_, err := CarregarCaronas(escreverCaronas(t, entrada))
			if !errors.Is(err, caso.quero) {
				t.Fatalf("err = %v, want %v", err, caso.quero)
			}
			if !strings.Contains(err.Error(), "car-ruim") {
				t.Errorf("mensagem não identifica a carona: %v", err)
			}
		})
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

// estadoDoCenario carrega o estado a partir de dados/, e não de caronas
// montadas no próprio teste.
//
// A escolha é deliberada: o cenário da seção 9.2 do PROJETO.md é um artefato
// versionado, usado também na demonstração e no relatório. Um teste que
// reconstruísse as sete caronas em código passaria a valer sobre dados que
// ninguém executa, e deixaria de acusar uma alteração indevida em
// dados/caronas.json — que é justamente um dos riscos que ele existe para
// cobrir.
func estadoDoCenario(t *testing.T) *Estado {
	t.Helper()
	e, err := CarregarEstado("../../dados/usuarios.json", "../../dados/caronas.json")
	if err != nil {
		t.Fatalf("CarregarEstado: %v", err)
	}
	return e
}

// assinatura reduz um itinerário à sequência de trechos que o identifica,
// no formato "car-1:0-1|car-3:0-2". É o que o teste compara: os horários e o
// preço são conferidos à parte, e comparar struct a struct só tornaria a
// mensagem de falha ilegível.
func assinatura(it Itinerario) string {
	s := ""
	for i, p := range it.Pernas {
		if i > 0 {
			s += "|"
		}
		s += fmt.Sprintf("%s:%d-%d", p.CaronaID, p.De, p.Ate)
	}
	return s
}

// TestBuscarItinerarios_CenarioSecao92 é o teste de regressão exigido pelo
// PROJETO.md (seção 9.2) e o critério de pronto da fase 6 (seção 11).
//
// A consulta Salvador → Vitória da Conquista em 15/09/2026 exercita o
// algoritmo inteiro da seção 6 de uma vez: não há carona direta na data, então
// toda resposta é uma baldeação, e os três controles negativos do cenário
// atacam um filtro diferente cada um.
//
// O teste fixa a lista **completa e ordenada**, e não apenas a presença dos
// itinerários válidos. Verificar só presença deixaria passar um itinerário a
// mais, que é exatamente a forma que um bug de validação toma aqui: car-4
// aparecendo por margem de baldeação mal aplicada não remove nenhum resultado
// correto, só acrescenta um errado.
func TestBuscarItinerarios_CenarioSecao92(t *testing.T) {
	e := estadoDoCenario(t)
	fuso := fusoBrasilia()
	data := time.Date(2026, 9, 15, 0, 0, 0, 0, fuso)

	itinerarios, err := e.BuscarItinerarios("Salvador", "Vitória da Conquista", data)
	if err != nil {
		t.Fatalf("BuscarItinerarios: %v", err)
	}

	// Ordem esperada: preço total crescente, empate por chegada mais cedo,
	// depois por menos baldeações (PROTOCOL.md, seção 5.8). Como as sete
	// caronas do cenário têm preços simétricos por construção, os seis
	// itinerários custam os mesmos 11500 centavos e o critério de preço empata
	// em todos: o que a lista abaixo fixa, na prática, são os dois critérios
	// de desempate.
	esperados := []struct {
		assinatura string
		preco      int
		partida    time.Time
		chegada    time.Time
		baldeacoes int
	}{
		{"car-1:0-1|car-3:0-2", 11500,
			time.Date(2026, 9, 15, 6, 0, 0, 0, fuso), time.Date(2026, 9, 15, 14, 30, 0, 0, fuso), 1},
		{"car-1:0-2|car-3:1-2", 11500,
			time.Date(2026, 9, 15, 6, 0, 0, 0, fuso), time.Date(2026, 9, 15, 14, 30, 0, 0, fuso), 1},
		{"car-1:0-1|car-7:0-1|car-3:1-2", 11500,
			time.Date(2026, 9, 15, 6, 0, 0, 0, fuso), time.Date(2026, 9, 15, 14, 30, 0, 0, fuso), 2},
		{"car-1:0-2|car-2:0-1", 11500,
			time.Date(2026, 9, 15, 6, 0, 0, 0, fuso), time.Date(2026, 9, 15, 15, 0, 0, 0, fuso), 1},
		{"car-1:0-1|car-3:0-1|car-2:0-1", 11500,
			time.Date(2026, 9, 15, 6, 0, 0, 0, fuso), time.Date(2026, 9, 15, 15, 0, 0, 0, fuso), 2},
		{"car-1:0-1|car-7:0-1|car-2:0-1", 11500,
			time.Date(2026, 9, 15, 6, 0, 0, 0, fuso), time.Date(2026, 9, 15, 15, 0, 0, 0, fuso), 2},
	}

	obtidas := make([]string, len(itinerarios))
	for i, it := range itinerarios {
		obtidas[i] = assinatura(it)
	}
	if len(itinerarios) != len(esperados) {
		t.Fatalf("quantidade de itinerários: got %d, want %d\nobtidos: %v",
			len(itinerarios), len(esperados), obtidas)
	}

	for i, quero := range esperados {
		it := itinerarios[i]
		if obtidas[i] != quero.assinatura {
			t.Errorf("posição %d: got %q, want %q\nlista obtida: %v", i, obtidas[i], quero.assinatura, obtidas)
			continue
		}
		if it.PrecoTotalCentavos != quero.preco {
			t.Errorf("%s: preço total = %d, want %d", quero.assinatura, it.PrecoTotalCentavos, quero.preco)
		}
		// Comparação por Equal, nunca por ==: o mesmo instante escrito em
		// fusos diferentes é o mesmo ponto no tempo (D11).
		if !it.Partida.Equal(quero.partida) {
			t.Errorf("%s: partida = %s, want %s", quero.assinatura, it.Partida, quero.partida)
		}
		if !it.Chegada.Equal(quero.chegada) {
			t.Errorf("%s: chegada = %s, want %s", quero.assinatura, it.Chegada, quero.chegada)
		}
		if baldeacoes := len(it.Pernas) - 1; baldeacoes != quero.baldeacoes {
			t.Errorf("%s: baldeações = %d, want %d", quero.assinatura, baldeacoes, quero.baldeacoes)
		}
		// A soma dos preços das pernas tem que fechar com o total: é o
		// campo em cima do qual o passageiro decide, e o único lugar onde
		// um erro de acumulação apareceria (D11).
		soma := 0
		for _, p := range it.Pernas {
			soma += p.PrecoCentavos
		}
		if soma != it.PrecoTotalCentavos {
			t.Errorf("%s: soma das pernas = %d, mas preço total = %d", quero.assinatura, soma, it.PrecoTotalCentavos)
		}
	}

	// Controles negativos do cenário (PROJETO.md, seção 9.2). A verificação é
	// redundante em relação à lista completa acima, mas nomeia o filtro que
	// quebrou: sem ela, a falha diria apenas "6 itinerários, want 5".
	proibidas := map[string]string{
		"car-4": "folga de 15 min após car-1, abaixo de MARGEM_BALDEACAO",
		"car-5": "sentido oposto ao da busca",
		"car-6": "direta, mas parte em 16/09",
	}
	for _, it := range itinerarios {
		for _, p := range it.Pernas {
			if motivo, proibida := proibidas[p.CaronaID]; proibida {
				t.Errorf("itinerário %s usa %s, que deveria ser rejeitada: %s",
					assinatura(it), p.CaronaID, motivo)
			}
		}
	}
}
