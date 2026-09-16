package dominio

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func fusoBrasilia() *time.Location {
	return time.FixedZone("-03:00", -3*60*60)
}

// TestCarregarCaronas_Car1ChegaEmJequieAs1045 usa o cenário de demonstração
// (PROJETO.md, seção 9.2): car-1 parte de Salvador às 06:00 de 01/10/2026,
// passa por Feira de Santana e chega a Jequié às 10:45.
func TestCarregarCaronas_Car1ChegaEmJequieAs1045(t *testing.T) {
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

	chegadaJequie := as(10, 45)
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
// Elas importam aqui tanto quanto na publicação: os horários vêm escritos no
// arquivo, nada garante por construção que eles cresçam, e a busca, a reserva e a
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

// agoraDaPublicacao é o relógio dos testes de publicação: 01/09/2026, antes
// de todas as paradas usadas neles, que caem em 01/10/2026 (helper as).
func agoraDaPublicacao() time.Time {
	return time.Date(2026, 9, 1, 0, 0, 0, 0, fusoBrasilia())
}

// TestPublicarCarona_GuardaParadasEInicializaLivres confere a D09 na
// publicação: o motorista informa as paradas e o horário de cada uma, e o
// estado guarda exatamente o que ele informou, com Livres começando igual a
// Assentos em cada trecho.
//
// Rota e horários foram escolhidos para que nenhuma derivação pudesse
// produzi-los — Jequié → Salvador → Vitória da Conquista não é sequência em
// linha, e 50 minutos entre Jequié e Salvador não é duração de trajeto nenhuma.
// Uma publicação que ainda calculasse horários falharia aqui.
func TestPublicarCarona_GuardaParadasEInicializaLivres(t *testing.T) {
	e := estadoDeTeste()
	rota := []string{"Jequié", "Salvador", "Vitória da Conquista"}
	horarios := []time.Time{as(8, 0), as(8, 50), as(20, 10)}

	c, err := e.PublicarCarona("joao", rota, horarios, 3, []int{1000, 2000}, agoraDaPublicacao())
	if err != nil {
		t.Fatalf("PublicarCarona: %v", err)
	}
	if c.ID == "" {
		t.Fatalf("carona publicada sem identificador")
	}

	// Confere o que ficou no estado, e não só o valor devolvido: é o estado
	// que a busca e a reserva vão ler.
	guardada, _, err := e.DetalharCarona(c.ID, "joao")
	if err != nil {
		t.Fatalf("DetalharCarona: %v", err)
	}
	if fmt.Sprint(guardada.Rota) != fmt.Sprint(rota) {
		t.Errorf("rota = %v, want %v", guardada.Rota, rota)
	}
	if len(guardada.Horarios) != len(horarios) {
		t.Fatalf("horarios = %v, want %v", guardada.Horarios, horarios)
	}
	for i, quero := range horarios {
		if !guardada.Horarios[i].Equal(quero) {
			t.Errorf("horarios[%d] = %v, want %v", i, guardada.Horarios[i], quero)
		}
	}
	if fmt.Sprint(guardada.Livres) != "[3 3]" {
		t.Errorf("Livres = %v, want [3 3]", guardada.Livres)
	}
	if guardada.MotoristaID != "joao" || fmt.Sprint(guardada.PrecoTrecho) != "[1000 2000]" {
		t.Errorf("carona guardada = %+v", guardada)
	}
}

// TestPublicarCarona_Validacoes percorre as validações da seção 5.4, cada uma
// com o sentinela que a seção 6 espera. Preço negativo e assento zero são
// "fora de faixa", que a tabela de erros classifica como CAMPO_INVALIDO.
//
// A grafia divergente fica aqui, e não num teste da lista de cidades: a
// comparação por igualdade exata só importa porque é ela que decide o que
// entra no estado.
func TestPublicarCarona_Validacoes(t *testing.T) {
	agora := agoraDaPublicacao()

	type paradas struct {
		rota     []string
		horarios []time.Time
	}
	duas := func(a, b string) paradas {
		return paradas{[]string{a, b}, []time.Time{as(8, 0), as(10, 0)}}
	}

	casos := []struct {
		nome     string
		paradas  paradas
		assentos int
		precos   []int
		querido  error
	}{
		{"cidade desconhecida", duas("Ilhéus", "Salvador"), 3, []int{3000}, ErrCidadeDesconhecida},
		{"grafia em minúsculas", duas("jequié", "Salvador"), 3, []int{3000}, ErrCidadeDesconhecida},
		{"grafia sem acento", duas("Jequie", "Salvador"), 3, []int{3000}, ErrCidadeDesconhecida},
		{"espaço antes", duas(" Jequié", "Salvador"), 3, []int{3000}, ErrCidadeDesconhecida},
		{"espaço depois", duas("Jequié ", "Salvador"), 3, []int{3000}, ErrCidadeDesconhecida},

		{"nenhuma parada", paradas{nil, nil}, 3, []int{}, ErrRotaInvalida},
		{"uma parada só", paradas{[]string{"Salvador"}, []time.Time{as(8, 0)}}, 3, []int{}, ErrRotaInvalida},
		{"origem igual ao destino", duas("Jequié", "Jequié"), 3, []int{3000}, ErrRotaInvalida},
		{"cidade repetida no meio", paradas{
			[]string{"Salvador", "Feira de Santana", "Salvador"},
			[]time.Time{as(8, 0), as(10, 0), as(12, 0)},
		}, 3, []int{3000, 3000}, ErrRotaInvalida},
		{"horário igual ao anterior", paradas{
			[]string{"Salvador", "Feira de Santana"},
			[]time.Time{as(8, 0), as(8, 0)},
		}, 3, []int{3000}, ErrRotaInvalida},
		{"horário antes do anterior", paradas{
			[]string{"Salvador", "Feira de Santana", "Jequié"},
			[]time.Time{as(8, 0), as(10, 0), as(9, 0)},
		}, 3, []int{3000, 4500}, ErrRotaInvalida},
		{"horários a menos", paradas{
			[]string{"Salvador", "Feira de Santana", "Jequié"},
			[]time.Time{as(8, 0), as(10, 0)},
		}, 3, []int{3000, 4500}, ErrRotaInvalida},
		{"preços a menos", duas("Salvador", "Feira de Santana"), 3, []int{}, ErrRotaInvalida},
		{"preços a mais", duas("Salvador", "Feira de Santana"), 3, []int{3000, 5000}, ErrRotaInvalida},

		{"preço negativo", duas("Salvador", "Feira de Santana"), 3, []int{-1}, ErrPrecoInvalido},
		{"sem assentos", duas("Salvador", "Feira de Santana"), 0, []int{3000}, ErrAssentosInvalidos},

		{"primeira parada no passado", paradas{
			[]string{"Salvador", "Feira de Santana"},
			[]time.Time{agora.Add(-time.Hour), as(10, 0)},
		}, 3, []int{3000}, ErrPartidaInvalida},
		{"primeira parada igual a agora", paradas{
			[]string{"Salvador", "Feira de Santana"},
			[]time.Time{agora, as(10, 0)},
		}, 3, []int{3000}, ErrPartidaInvalida},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			e := estadoDeTeste()
			_, err := e.PublicarCarona("joao", caso.paradas.rota, caso.paradas.horarios, caso.assentos, caso.precos, agora)
			if !errors.Is(err, caso.querido) {
				t.Errorf("err = %v, want %v", err, caso.querido)
			}
			// D07 aplicado à publicação: validação antes de qualquer escrita,
			// então uma recusa não pode deixar carona pela metade no estado.
			if len(e.caronas) != 0 {
				t.Errorf("recusa deixou %d carona(s) no estado", len(e.caronas))
			}
		})
	}
}

// TestPublicarCarona_NaoCompartilhaMemoriaComOChamador confere que as fatias
// de rota, horários e preços são copiadas ao entrar no estado. Sem a cópia,
// quem publicou continuaria com um ponteiro para dentro da estrutura protegida
// pelo mutex e poderia trocar uma parada, um horário ou um preço de uma carona
// já publicada, sem lock nenhum — e com os horários fora de ordem, sem passar
// pela validação.
func TestPublicarCarona_NaoCompartilhaMemoriaComOChamador(t *testing.T) {
	e := estadoDeTeste()
	rota := []string{"Salvador", "Feira de Santana"}
	horarios := []time.Time{as(8, 0), as(10, 0)}
	precos := []int{3000}

	c, err := e.PublicarCarona("joao", rota, horarios, 2, precos, agoraDaPublicacao())
	if err != nil {
		t.Fatalf("PublicarCarona: %v", err)
	}

	rota[0] = "Ilhéus"
	horarios[1] = as(7, 0)
	precos[0] = 999999
	c.Livres[0] = 999999

	guardada, _, err := e.DetalharCarona(c.ID, "joao")
	if err != nil {
		t.Fatalf("DetalharCarona: %v", err)
	}
	if guardada.Rota[0] != "Salvador" {
		t.Errorf("rota no estado = %v: a fatia de rota do chamador não foi copiada", guardada.Rota)
	}
	if !guardada.Horarios[1].Equal(as(10, 0)) {
		t.Errorf("horário no estado = %v, want 10:00: a fatia de horários do chamador não foi copiada", guardada.Horarios[1])
	}
	if guardada.PrecoTrecho[0] != 3000 {
		t.Errorf("preço no estado = %d, want 3000: a fatia de preços do chamador não foi copiada", guardada.PrecoTrecho[0])
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
	publicar := func(motorista string, hora int) {
		t.Helper()
		if _, err := e.PublicarCarona(motorista, []string{"Salvador", "Feira de Santana"},
			[]time.Time{as(hora, 0), as(hora+2, 0)}, 2, []int{3000}, agoraDaPublicacao()); err != nil {
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

// TestCaronasDoMotorista_LimiteCortaDepoisDeOrdenar confere o limite das
// listagens (D16): no máximo MAXIMO_ITENS_LISTAGEM caronas, as de pé antes das
// canceladas, e o corte depois da ordenação.
//
// As canceladas são as de partida mais cedo, de propósito. Ordenadas só por
// partida, viriam primeiro, e o corte descartaria caronas que ainda estão de
// pé — as que o motorista mais precisa ver.
func TestCaronasDoMotorista_LimiteCortaDepoisDeOrdenar(t *testing.T) {
	e := estadoDeTeste()
	const total, canceladas = MAXIMO_ITENS_LISTAGEM + 5, 10

	ids := make([]string, total)
	for i := range ids {
		partida := as(6, 0).AddDate(0, 0, i)
		c, err := e.PublicarCarona("joao", []string{"Salvador", "Feira de Santana"},
			[]time.Time{partida, partida.Add(2 * time.Hour)}, 2, []int{3000}, agoraDaPublicacao())
		if err != nil {
			t.Fatalf("PublicarCarona %d: %v", i, err)
		}
		ids[i] = c.ID
	}
	for _, id := range ids[:canceladas] {
		if _, err := e.CancelarCarona(id, "joao", agoraDaPublicacao()); err != nil {
			t.Fatalf("CancelarCarona(%s): %v", id, err)
		}
	}

	lista := e.CaronasDoMotorista("joao", true)
	if len(lista) != MAXIMO_ITENS_LISTAGEM {
		t.Fatalf("len = %d, want %d", len(lista), MAXIMO_ITENS_LISTAGEM)
	}
	dePe := total - canceladas
	for i, c := range lista {
		if querCancelada := i >= dePe; c.Cancelada != querCancelada {
			t.Fatalf("posição %d: cancelada = %v, want %v — as caronas de pé vêm antes das canceladas",
				i, c.Cancelada, querCancelada)
		}
	}

	if n := len(e.CaronasDoMotorista("joao", false)); n != dePe {
		t.Fatalf("sem as canceladas: len = %d, want %d", n, dePe)
	}
}

// TestDetalharCarona_DonoEInexistente confere a seção 5.6: carona de outro
// motorista e carona inexistente têm erros distintos.
func TestDetalharCarona_DonoEInexistente(t *testing.T) {
	e := estadoDeTeste()
	c, err := e.PublicarCarona("joao", []string{"Salvador", "Feira de Santana", "Jequié"},
		[]time.Time{as(8, 0), as(10, 0), as(13, 0)}, 2, []int{3000, 4500}, agoraDaPublicacao())
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
	c, err := e.PublicarCarona("joao", []string{"Salvador", "Feira de Santana", "Jequié", "Vitória da Conquista"},
		[]time.Time{as(8, 0), as(10, 0), as(13, 0), as(15, 30)}, 3, []int{3000, 5000, 4000}, agoraDaPublicacao())
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
// PROJETO.md (seção 9.2).
//
// A consulta Salvador → Vitória da Conquista em 01/10/2026 exercita o
// algoritmo inteiro da seção 6 de uma vez: baldeação entre motoristas, embarque
// no meio de uma rota que um corredor fixo não permitiria, três pernas, e os
// três critérios de ordenação da D16, cada um decidindo um par. Os controles
// negativos do cenário atacam, cada um, uma única regra.
//
// O teste fixa a lista **completa e ordenada**, e não apenas a presença dos
// itinerários válidos. Verificar só presença deixaria passar um itinerário a
// mais, que é exatamente a forma que um bug de validação toma aqui: car-6
// aparecendo por margem de baldeação mal aplicada não remove nenhum resultado
// correto, só acrescenta um errado.
func TestBuscarItinerarios_CenarioSecao92(t *testing.T) {
	e := estadoDoCenario(t)

	itinerarios, err := e.BuscarItinerarios("Salvador", "Vitória da Conquista", as(0, 0))
	if err != nil {
		t.Fatalf("BuscarItinerarios: %v", err)
	}

	// Ordem esperada (D16; PROTOCOL.md, seção 5.8), calculada à mão na seção
	// 9.2 do PROJETO.md:
	//   - #1 é a mais cara e vem primeiro: não tem baldeação;
	//   - #2 vem antes de #3 chegando mais tarde: é mais barata, e preço
	//     decide antes de chegada dentro do mesmo número de trocas;
	//   - #4 vem por último sendo mais barata que #1 e #3: tem duas trocas.
	esperados := []struct {
		assinatura string
		preco      int
		partida    time.Time
		chegada    time.Time
		baldeacoes int
	}{
		{"car-5:1-2", 11000, as(10, 30), as(17, 30), 0},
		{"car-1:0-1|car-3:0-1", 8000, as(6, 0), as(16, 45), 1},
		{"car-1:0-2|car-2:0-1", 10000, as(6, 0), as(14, 30), 1},
		{"car-1:0-1|car-4:0-1|car-2:0-1", 9500, as(6, 0), as(14, 30), 2},
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
	// redundante em relação à lista completa acima, mas nomeia a regra que
	// quebrou: sem ela, a falha diria apenas "5 itinerários, want 4".
	proibidas := map[string]string{
		"car-6": "sai 15 min após car-1 chegar a Jequié, abaixo de MARGEM_BALDEACAO",
		"car-7": "é de 02/10: como primeira perna, fora da data; como conexão, 24 h de espera, acima de ESPERA_MAXIMA_BALDEACAO",
		"car-8": "volta a Salvador depois de car-1: cidade repetida no itinerário",
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

// --- Busca com paradas livres (D09; PROJETO.md, seção 6) ---
//
// Os testes abaixo montam as caronas em código, e não em dados/, ao contrário
// do teste de regressão acima: cada um isola uma regra da busca com o menor
// número de caronas capaz de exercitá-la. Todas as caronas saem em 01/10/2026
// (helper as), e a busca é sempre dessa data.

// publicarNoDia publica uma carona de joao com as paradas dadas, 2 assentos e
// R$ 10,00 por trecho, e devolve o identificador gerado.
func publicarNoDia(t *testing.T, e *Estado, rota []string, horarios ...time.Time) string {
	t.Helper()
	return publicarComPreco(t, e, 1000, rota, horarios...)
}

// publicarComPreco é publicarNoDia com o preço de cada trecho escolhido, para
// os testes de ordenação.
func publicarComPreco(t *testing.T, e *Estado, precoPorTrecho int, rota []string, horarios ...time.Time) string {
	t.Helper()
	precos := make([]int, len(rota)-1)
	for i := range precos {
		precos[i] = precoPorTrecho
	}
	c, err := e.PublicarCarona("joao", rota, horarios, 2, precos, agoraDaPublicacao())
	if err != nil {
		t.Fatalf("PublicarCarona(%v): %v", rota, err)
	}
	return c.ID
}

// buscarNoDia busca de origem a destino no dia do helper as e devolve as
// assinaturas dos itinerários, na ordem da resposta.
func buscarNoDia(t *testing.T, e *Estado, origem, destino string) ([]Itinerario, []string) {
	t.Helper()
	itinerarios, err := e.BuscarItinerarios(origem, destino, as(0, 0))
	if err != nil {
		t.Fatalf("BuscarItinerarios(%s, %s): %v", origem, destino, err)
	}
	assinaturas := make([]string, len(itinerarios))
	for i, it := range itinerarios {
		assinaturas[i] = assinatura(it)
	}
	return itinerarios, assinaturas
}

// TestBuscarItinerarios_RotaForaDeOrdemServeABusca confere que a busca não
// descarta carona pela ordem das suas paradas.
//
// A carona vai de Feira de Santana a Salvador e depois a Vitória da Conquista.
// As cidades não têm ordem nem sentido de viagem (D09), e a perna Salvador →
// Vitória da Conquista serve à busca como qualquer outra. Uma poda que ainda
// classificasse a carona pelas duas primeiras paradas a descartaria inteira —
// falso negativo, e não excesso de cautela.
func TestBuscarItinerarios_RotaForaDeOrdemServeABusca(t *testing.T) {
	e := estadoDeTeste()
	id := publicarNoDia(t, e, []string{"Feira de Santana", "Salvador", "Vitória da Conquista"},
		as(6, 0), as(8, 0), as(18, 0))

	_, obtidas := buscarNoDia(t, e, "Salvador", "Vitória da Conquista")

	quero := []string{id + ":1-2"}
	if fmt.Sprint(obtidas) != fmt.Sprint(quero) {
		t.Fatalf("itinerários = %v, want %v", obtidas, quero)
	}
}

// TestBuscarItinerarios_NaoVoltaACidadeVisitada é o contraexemplo da seção 6
// do PROJETO.md: o encadeamento no espaço e no tempo, sozinho, não impede um
// itinerário de voltar à origem.
//
// A (Salvador → Feira), B (Feira → Salvador) e C (Salvador → Conquista)
// encadeiam com folgas dentro da janela de baldeação e são três caronas
// distintas, então A|B|C passaria em todas as outras regras da busca. Só o
// conjunto de cidades visitadas o recusa. C sozinha é o controle positivo: sem
// ela, uma busca que não devolvesse nada também passaria.
func TestBuscarItinerarios_NaoVoltaACidadeVisitada(t *testing.T) {
	e := estadoDeTeste()
	a := publicarNoDia(t, e, []string{"Salvador", "Feira de Santana"}, as(6, 0), as(8, 0))
	b := publicarNoDia(t, e, []string{"Feira de Santana", "Salvador"}, as(8, 30), as(10, 30))
	c := publicarNoDia(t, e, []string{"Salvador", "Vitória da Conquista"}, as(11, 0), as(18, 30))

	_, obtidas := buscarNoDia(t, e, "Salvador", "Vitória da Conquista")

	quero := []string{c + ":0-1"}
	if fmt.Sprint(obtidas) != fmt.Sprint(quero) {
		t.Fatalf("itinerários = %v, want %v\n(A=%s, B=%s, C=%s; A|B|C volta a Salvador)", obtidas, quero, a, b, c)
	}
}

// TestBuscarItinerarios_PernaNaoAtravessaCidadeVisitada confere D-e: as
// cidades por onde o passageiro passa dentro do veículo também contam como
// visitadas, e não só as de embarque e desembarque.
//
// A leva de Salvador a Jequié. B vai de Jequié a Feira de Santana passando por
// Salvador, e C segue de Feira a Vitória da Conquista. A perna Jequié → Feira
// de B embarca e desembarca em cidades novas, mas atravessa Salvador, onde o
// passageiro já esteve: A|B|C voltaria à origem sem descer do carro.
//
// Dois controles positivos. D, direta, garante que a busca não parou de
// encontrar. B|C, embarcando em B já em Salvador (posição 1 da rota de B),
// garante que a regra recusa a perna que atravessa a cidade visitada, e não a
// carona B inteira: a mesma carona, num segmento que não revisita nada, serve.
func TestBuscarItinerarios_PernaNaoAtravessaCidadeVisitada(t *testing.T) {
	e := estadoDeTeste()
	publicarNoDia(t, e, []string{"Salvador", "Jequié"}, as(6, 0), as(8, 0))
	b := publicarNoDia(t, e, []string{"Jequié", "Salvador", "Feira de Santana"}, as(8, 30), as(9, 30), as(10, 30))
	c := publicarNoDia(t, e, []string{"Feira de Santana", "Vitória da Conquista"}, as(11, 0), as(14, 0))
	d := publicarNoDia(t, e, []string{"Salvador", "Vitória da Conquista"}, as(7, 0), as(12, 0))

	_, obtidas := buscarNoDia(t, e, "Salvador", "Vitória da Conquista")

	// D é direta e B|C tem uma troca de veículo: D vem primeiro (D16).
	quero := []string{d + ":0-1", b + ":1-2|" + c + ":0-1"}
	if fmt.Sprint(obtidas) != fmt.Sprint(quero) {
		t.Fatalf("itinerários = %v, want %v", obtidas, quero)
	}
}

// TestBuscarItinerarios_TetoDePernas confere o teto que decorre da regra de
// não revisitar cidade: cada perna acrescenta ao menos uma cidade nova, então
// nenhum itinerário tem mais de |cidades| − 1 pernas.
//
// As caronas formam, de propósito, uma cadeia que encadeia no tempo por seis
// pernas indo e voltando entre as cidades (A, B, C, D, E, F). Sem o conjunto de
// visitadas ela seria devolvida; com ele, o mais longo possível é A|B|G, que
// passa pelas quatro cidades uma vez cada. A presença de A|B|G é o controle
// positivo: garante que o teto veio da regra, e não de uma busca que parou de
// encadear.
func TestBuscarItinerarios_TetoDePernas(t *testing.T) {
	e := estadoDeTeste()
	a := publicarNoDia(t, e, []string{"Salvador", "Feira de Santana"}, as(6, 0), as(7, 0))
	b := publicarNoDia(t, e, []string{"Feira de Santana", "Jequié"}, as(8, 0), as(9, 0))
	g := publicarNoDia(t, e, []string{"Jequié", "Vitória da Conquista"}, as(10, 0), as(11, 0))
	publicarNoDia(t, e, []string{"Jequié", "Feira de Santana"}, as(10, 0), as(11, 0))     // C
	publicarNoDia(t, e, []string{"Feira de Santana", "Salvador"}, as(12, 0), as(13, 0))   // D
	publicarNoDia(t, e, []string{"Salvador", "Jequié"}, as(14, 0), as(15, 0))             // E
	publicarNoDia(t, e, []string{"Jequié", "Vitória da Conquista"}, as(16, 0), as(17, 0)) // F

	itinerarios, obtidas := buscarNoDia(t, e, "Salvador", "Vitória da Conquista")

	teto := len(CidadesAtendidas()) - 1
	for _, it := range itinerarios {
		if len(it.Pernas) > teto {
			t.Errorf("itinerário %s tem %d pernas, acima do teto de %d", assinatura(it), len(it.Pernas), teto)
		}
	}
	maisLongo := a + ":0-1|" + b + ":0-1|" + g + ":0-1"
	encontrado := false
	for _, s := range obtidas {
		if s == maisLongo {
			encontrado = true
		}
	}
	if !encontrado {
		t.Errorf("A|B|G (%s) ausente; itinerários: %v", maisLongo, obtidas)
	}
}

// TestBuscarItinerarios_OrdenaPorTrocasDepoisPrecoDepoisChegada confere os
// três critérios da D16, cada um decidindo um par.
//
//   - Trocas antes de preço: a direta D1, a R$ 90,00, vem antes de A|B, com
//     uma troca, a R$ 20,00 — e A|B ainda chega mais cedo. É o caso em que a
//     ordem antiga (preço primeiro) daria o resultado oposto.
//   - Preço antes de chegada: entre as diretas, D2, a R$ 90,00 e chegando às
//     18:00, vem antes de D3, a R$ 95,00 e chegando às 13:00.
//   - Chegada como último critério de negócio: D1 e D2 custam o mesmo, e D1
//     chega antes.
func TestBuscarItinerarios_OrdenaPorTrocasDepoisPrecoDepoisChegada(t *testing.T) {
	e := estadoDeTeste()
	vdc := "Vitória da Conquista"
	d1 := publicarComPreco(t, e, 9000, []string{"Salvador", vdc}, as(8, 0), as(16, 0))
	d2 := publicarComPreco(t, e, 9000, []string{"Salvador", vdc}, as(9, 0), as(18, 0))
	d3 := publicarComPreco(t, e, 9500, []string{"Salvador", vdc}, as(7, 0), as(13, 0))
	a := publicarComPreco(t, e, 1000, []string{"Salvador", "Jequié"}, as(6, 0), as(9, 0))
	b := publicarComPreco(t, e, 1000, []string{"Jequié", vdc}, as(10, 0), as(12, 0))

	_, obtidas := buscarNoDia(t, e, "Salvador", vdc)

	quero := []string{d1 + ":0-1", d2 + ":0-1", d3 + ":0-1", a + ":0-1|" + b + ":0-1"}
	if fmt.Sprint(obtidas) != fmt.Sprint(quero) {
		t.Fatalf("ordem = %v\nwant    %v\n(D1=%s, D2=%s, D3=%s, A=%s, B=%s)", obtidas, quero, d1, d2, d3, a, b)
	}
}

// TestBuscarItinerarios_LimiteDeDezCortaDepoisDeOrdenar confere o limite da
// D16 e, principalmente, que ele é aplicado **depois** da ordenação.
//
// São 24 itinerários possíveis: 4 diretos, caros, e 20 com uma troca, baratos
// (4 caronas até Feira de Santana combinadas com 5 que seguem de lá). A busca
// devolve 10, e duas coisas precisam valer sobre eles:
//
//   - os 4 diretos estão todos lá: como nenhum itinerário possível tem menos
//     trocas que um direto, cortar qualquer um deles mantendo um com troca
//     seria cortar antes de ordenar, ou ordenar pelo critério errado;
//   - as 6 vagas restantes ficam com os 6 itinerários de uma troca **mais
//     baratos**. Os preços do grupo são todos diferentes, e a chegada foi
//     montada na ordem contrária à do preço: a segunda perna mais barata é a
//     que chega mais tarde. Um corte que desempatasse por chegada antes de
//     preço ficaria com outro conjunto.
func TestBuscarItinerarios_LimiteDeDezCortaDepoisDeOrdenar(t *testing.T) {
	e := estadoDeTeste()
	vdc := "Vitória da Conquista"
	const diretos, primeiras, segundas = 4, 4, 5

	// Preços em centavos: a primeira perna i custa 1000 + 100·i e a segunda
	// perna j custa 1000 + 10·j, então os 20 totais 2000 + 100·i + 10·j são
	// todos distintos.
	precoPrimeira := func(i int) int { return 1000 + 100*i }
	precoSegunda := func(j int) int { return 1000 + 10*j }

	for i := 0; i < diretos; i++ {
		publicarComPreco(t, e, 9000, []string{"Salvador", vdc}, as(9, 5*i), as(13, 5*i))
	}
	for i := 0; i < primeiras; i++ {
		publicarComPreco(t, e, precoPrimeira(i), []string{"Salvador", "Feira de Santana"}, as(6, 5*i), as(7, 5*i))
	}
	for j := 0; j < segundas; j++ {
		// Partidas entre 08:00 e 08:20: todas a mais de 30 minutos da última
		// chegada a Feira (07:15), então as 4 × 5 combinações encadeiam.
		// Chegadas entre 12:20 (j = 0, a mais barata) e 12:00 (j = 4, a mais
		// cara): chegada e preço discordam de propósito.
		publicarComPreco(t, e, precoSegunda(j), []string{"Feira de Santana", vdc}, as(8, 5*j), as(12, 20-5*j))
	}

	itinerarios, obtidas := buscarNoDia(t, e, "Salvador", vdc)

	// Errorf, e não Fatalf: com o tamanho errado, a verificação de quais
	// itinerários sobreviveram ao corte continua sendo informativa.
	if len(itinerarios) != MAXIMO_ITINERARIOS || MAXIMO_ITINERARIOS != 10 {
		t.Errorf("busca devolveu %d itinerários (MAXIMO_ITINERARIOS = %d), want 10 de %d possíveis",
			len(itinerarios), MAXIMO_ITINERARIOS, diretos+primeiras*segundas)
	}
	diretosDevolvidos := 0
	for i, it := range itinerarios {
		if len(it.Pernas) == 1 {
			diretosDevolvidos++
		}
		if i > 0 && len(it.Pernas) < len(itinerarios[i-1].Pernas) {
			t.Errorf("posição %d tem menos trocas que a anterior: %v", i, obtidas)
		}
	}
	if diretosDevolvidos != diretos {
		t.Errorf("%d diretos devolvidos, want os %d: o corte descartou itinerário com menos trocas\n%v",
			diretosDevolvidos, diretos, obtidas)
	}

	// As vagas que sobram depois dos diretos são dos itinerários de uma troca
	// mais baratos, na ordem de preço.
	var possiveis []int
	for i := 0; i < primeiras; i++ {
		for j := 0; j < segundas; j++ {
			possiveis = append(possiveis, precoPrimeira(i)+precoSegunda(j))
		}
	}
	sort.Ints(possiveis)
	var devolvidos []int
	for _, it := range itinerarios {
		if len(it.Pernas) == 2 {
			devolvidos = append(devolvidos, it.PrecoTotalCentavos)
		}
	}
	quero := possiveis[:MAXIMO_ITINERARIOS-diretos]
	if fmt.Sprint(devolvidos) != fmt.Sprint(quero) {
		t.Errorf("preços dos itinerários com uma troca devolvidos = %v, want os %d mais baratos, %v:\n"+
			"o corte não aplicou preço antes de chegada\n%v", devolvidos, len(quero), quero, obtidas)
	}
}

// TestFusoDasCidades confere que o fuso compartilhado por servidor e cliente
// fica no deslocamento das cidades atendidas, todas na Bahia.
//
// Vale com ou sem tzdata na máquina: se LoadLocation falhar, o deslocamento
// fixo responde o mesmo -03:00. O que não pode acontecer é cair em UTC, que é
// o fuso local dentro do contêiner Alpine.
func TestFusoDasCidades(t *testing.T) {
	fuso := FusoDasCidades()

	instante := time.Date(2026, 9, 15, 8, 0, 0, 0, fuso)
	if _, deslocamento := instante.Zone(); deslocamento != -3*60*60 {
		t.Errorf("deslocamento em 15/09/2026 = %d s, want %d s", deslocamento, -3*60*60)
	}
	if got := instante.Format(time.RFC3339); got != "2026-09-15T08:00:00-03:00" {
		t.Errorf("instante = %s, want 2026-09-15T08:00:00-03:00", got)
	}

	// Em janeiro também: a Bahia não observa horário de verão, e um fuso que
	// mudasse de deslocamento faria a carona publicada em dezembro sair uma
	// hora fora.
	verao := time.Date(2027, 1, 15, 8, 0, 0, 0, fuso)
	if got := verao.Format(time.RFC3339); got != "2027-01-15T08:00:00-03:00" {
		t.Errorf("instante = %s, want 2027-01-15T08:00:00-03:00", got)
	}
}
