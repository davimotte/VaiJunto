package testes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/federacao"
	"vaijunto/internal/protocolo"
	"vaijunto/internal/servidor"
	"vaijunto/internal/transacao"
)

// Testes da busca federada (PROJETO_P2.md, seção 6): TB1, TB2, a semente da
// seção 12.2 e o descarte do passo 5.
//
// As três empresas sobem no mesmo processo, cada uma com as suas duas portas de
// verdade em loopback — a TCP dos clientes e a HTTP dos pares. Tudo é
// exercitado pela porta por onde o passageiro entra, falando o protocolo: é a
// única forma de provar que o resultado parcial chega até ele.

// donoNoTB1 distribui as oito caronas do fixture do P1 entre as três empresas,
// conforme a seção 12.4 do PROJETO_P2.md.
//
// A distribuição não é arbitrária: ela faz cada itinerário esperado atravessar
// empresas. O #3 (car-1 + car-2) usa duas; o #4 (car-1 + car-4 + car-2) usa as
// três; e os controles negativos também atravessam, de modo que a margem de
// baldeação (car-6) e o ciclo (car-8) sejam recusados sobre dados que vieram
// pela rede, e não sobre dados locais.
var donoNoTB1 = map[string]string{
	"car-1": "vaijunto",
	"car-5": "vaijunto",
	"car-2": "rotacapixaba",
	"car-6": "rotacapixaba",
	"car-3": "rotafluminense",
	"car-4": "rotafluminense",
	"car-7": "rotafluminense",
	"car-8": "rotafluminense",
}

// subirConsorcioDeBusca sobe o consórcio com as duas portas e a busca federada
// ligada, que é o que os testes de busca exercitam.
//
// É um nome próprio sobre subirConsorcioP2 (ajuda_test.go) porque os testes
// deste arquivo falam de busca, e ler "subirConsorcioDeBusca" diz o que a
// empresa está pronta para fazer.
func subirConsorcioDeBusca(t *testing.T, caronasPorEmpresa map[string][]byte, paresExtra map[string]string) map[string]*empresa {
	t.Helper()
	return subirConsorcioP2(t, opcoesConsorcio{
		caronas:      caronasPorEmpresa,
		tcp:          true,
		busca:        true,
		paresExtra:   paresExtra,
		prazoChamada: prazoBuscaNosTestes,
	})
}

// buscaDe monta uma busca federada com a mesma Config das outras pontas da
// pilha REST, do jeito que o boot faz. prazo zero vira o padrão dos testes.
//
// Existe para que subirServidor, em integracao_test.go, monte a empresa
// sozinha pelo mesmo caminho do servidor de verdade.
func buscaDe(t *testing.T, nome string, estado *dominio.Estado, pares map[string]string, registro *saidaSegura, prazo time.Duration) servidor.BuscaFederada {
	t.Helper()

	if pares == nil {
		pares = map[string]string{}
	}
	if prazo == 0 {
		prazo = prazoBuscaNosTestes
	}
	cfg := federacao.Config{
		Empresa:      nome,
		Pares:        pares,
		Relogio:      transacao.NovoRelogio(),
		Estado:       estado,
		PrazoChamada: prazo,
		PrazoBusca:   prazo,
	}
	if registro != nil {
		cfg.Registro = federacao.NovoRegistro(registro, nome)
	}
	return federacao.NovaBusca(cfg, federacao.NovoCliente(cfg))
}

// distribuirFixtureDoP1 lê testes/dados/p1/caronas.json uma única vez e
// devolve, por empresa, o arquivo de caronas que ela hospeda no TB1.
//
// A troca de prefixo acontece aqui, em código, e não num segundo conjunto de
// arquivos: o fixture continua sendo a fonte única das oito caronas, e assim
// nenhuma diferença de dado pode explicar uma diferença de resultado entre a
// regressão de uma empresa e a federada. Nada além do campo "id" é tocado.
func distribuirFixtureDoP1(t *testing.T) map[string][]byte {
	t.Helper()

	bruto, err := os.ReadFile(filepath.Join(diretorioSemente, "caronas.json"))
	if err != nil {
		t.Fatalf("ler caronas do fixture: %v", err)
	}

	// map[string]any preserva os campos que não nos interessam — inclusive o
	// _nota — sem precisar de uma struct espelho que ficaria desatualizada.
	var caronas []map[string]any
	if err := json.Unmarshal(bruto, &caronas); err != nil {
		t.Fatalf("decodificar caronas do fixture: %v", err)
	}

	porEmpresa := map[string][]map[string]any{
		"vaijunto": {}, "rotacapixaba": {}, "rotafluminense": {},
	}
	for _, c := range caronas {
		id, ok := c["id"].(string)
		if !ok {
			t.Fatalf("carona sem id no fixture: %v", c)
		}
		_, sufixo, encontrado := strings.Cut(id, ":")
		if !encontrado {
			t.Fatalf("carona %q do fixture sem prefixo de empresa", id)
		}
		dono, mapeada := donoNoTB1[sufixo]
		if !mapeada {
			t.Fatalf("carona %q não está na distribuição da seção 12.4", sufixo)
		}
		c["id"] = dono + ":" + sufixo
		porEmpresa[dono] = append(porEmpresa[dono], c)
	}

	arquivos := make(map[string][]byte, len(porEmpresa))
	for empresa, lista := range porEmpresa {
		if len(lista) == 0 {
			t.Fatalf("empresa %s ficou sem carona: a distribuição da seção 12.4 mudou?", empresa)
		}
		codificado, err := json.Marshal(lista)
		if err != nil {
			t.Fatalf("codificar caronas de %s: %v", empresa, err)
		}
		arquivos[empresa] = codificado
	}
	return arquivos
}

// sementesDasTresEmpresas devolve os arquivos de caronas de dados/<empresa>, a
// semente da demonstração (seção 12.2).
func sementesDasTresEmpresas(t *testing.T) map[string][]byte {
	t.Helper()

	arquivos := make(map[string][]byte, 3)
	for _, empresa := range []string{"vaijunto", "rotacapixaba", "rotafluminense"} {
		bruto, err := os.ReadFile(filepath.Join("..", "dados", empresa, "caronas.json"))
		if err != nil {
			t.Fatalf("ler semente de %s: %v", empresa, err)
		}
		arquivos[empresa] = bruto
	}
	return arquivos
}

// buscar faz o BUSCAR_ITINERARIOS pela porta TCP da empresa, autenticado como
// maria, que é passageira nas três (seção 12.1).
func buscar(t *testing.T, endereco, origem, destino, data string) protocolo.BuscarItinerariosResposta {
	t.Helper()

	c := conectar(t, endereco)
	c.entrar("maria", "abcd")

	var resposta protocolo.BuscarItinerariosResposta
	c.exigirOK(protocolo.TipoBuscarItinerarios, protocolo.BuscarItinerariosRequisicao{
		Origem: origem, Destino: destino, Data: data,
	}, &resposta)
	return resposta
}

// assinaturaSemPrefixo reduz o itinerário à sequência de trechos, **sem** o
// prefixo da empresa: "car-1:0-1|car-3:0-1".
//
// É assim que o TB1 compara (PROJETO_P2.md, seção 6): a afirmação é que a
// distribuição entre empresas não muda o itinerário, e o prefixo é justamente a
// parte que ela muda.
func assinaturaSemPrefixo(it protocolo.Itinerario) string {
	var b strings.Builder
	for i, trecho := range it.Trechos {
		if i > 0 {
			b.WriteByte('|')
		}
		_, sufixo, _ := strings.Cut(trecho.CaronaID, ":")
		fmt.Fprintf(&b, "%s:%d-%d", sufixo, trecho.De, trecho.Ate)
	}
	return b.String()
}

// assinaturasSemPrefixo mapeia a lista inteira, preservando a ordem.
func assinaturasSemPrefixo(itinerarios []protocolo.Itinerario) []string {
	assinaturas := make([]string, len(itinerarios))
	for i, it := range itinerarios {
		assinaturas[i] = assinaturaSemPrefixo(it)
	}
	return assinaturas
}

// TB1 — as oito caronas da regressão do P1, distribuídas entre as três
// empresas, produzem exatamente os mesmos quatro itinerários da seção 9.2 do
// PROJETO.md, na mesma ordem (PROJETO_P2.md, seção 6, "Equivalência federada").
//
// É o teste central da fase: ele afirma que o resultado da busca não depende de
// qual empresa hospeda cada carona. A comparação ignora o prefixo porque é
// exatamente ele que muda; tudo o mais — quais caronas, quais índices, qual
// preço, qual ordem — tem de ser idêntico.
func TestTB1EquivalenciaFederadaReproduzOsQuatroItinerariosDoP1(t *testing.T) {
	consorcio := subirConsorcioDeBusca(t, distribuirFixtureDoP1(t), nil)

	resposta := buscar(t, consorcio["vaijunto"].endereco, "Salvador", "Vitória da Conquista", "2026-10-01")

	if resposta.Parcial {
		t.Fatalf("resultado parcial com as três empresas no ar; indisponíveis: %v", resposta.EmpresasIndisponiveis)
	}
	if len(resposta.EmpresasIndisponiveis) != 0 {
		t.Errorf("empresas_indisponiveis = %v, want vazia", resposta.EmpresasIndisponiveis)
	}

	// A lista completa e ordenada, e não só a presença dos válidos: um bug de
	// validação costuma acrescentar um itinerário errado sem remover nenhum
	// correto (PROJETO.md, seção 9.2).
	querAssinaturas := []string{
		"car-5:1-2",                     // #1 direta, zero baldeações
		"car-1:0-1|car-3:0-1",           // #2 uma troca, mais barata
		"car-1:0-2|car-2:0-1",           // #3 uma troca, a baldeação do enunciado
		"car-1:0-1|car-4:0-1|car-2:0-1", // #4 duas trocas
	}
	querPrecos := []int{11000, 8000, 10000, 9500}

	got := assinaturasSemPrefixo(resposta.Itinerarios)
	if len(got) != len(querAssinaturas) {
		t.Fatalf("itinerários = %d (%v), want %d (%v)", len(got), got, len(querAssinaturas), querAssinaturas)
	}
	for i := range querAssinaturas {
		if got[i] != querAssinaturas[i] {
			t.Errorf("itinerário %d = %q, want %q (a ordem da D16 mudou)", i+1, got[i], querAssinaturas[i])
		}
		if p := resposta.Itinerarios[i].PrecoTotalCentavos; p != querPrecos[i] {
			t.Errorf("itinerário %d: preço = %d, want %d", i+1, p, querPrecos[i])
		}
	}

	// Cada perna informa a empresa, e ela é a dona do prefixo do ID (D27).
	for i, it := range resposta.Itinerarios {
		for j, trecho := range it.Trechos {
			dona, err := dominio.EmpresaDoID(trecho.CaronaID)
			if err != nil {
				t.Errorf("itinerário %d, trecho %d: carona_id %q sem prefixo", i+1, j, trecho.CaronaID)
				continue
			}
			if trecho.Empresa != dona {
				t.Errorf("itinerário %d, trecho %d: empresa = %q, want %q", i+1, j, trecho.Empresa, dona)
			}
		}
	}
}

// Nenhum dos três controles negativos pode aparecer, e agora eles atravessam
// empresas: car-6 (margem de baldeação) está na rotacapixaba, car-7 (data e
// teto de espera) e car-8 (ciclo) na rotafluminense.
//
// Se um deles aparecer, a regra que ele isola quebrou — e quebrou sobre dado
// que chegou pela rede, que é o caminho que a fase 3B acrescentou.
func TestTB1ControlesNegativosNaoAparecemVindoDeOutraEmpresa(t *testing.T) {
	consorcio := subirConsorcioDeBusca(t, distribuirFixtureDoP1(t), nil)

	resposta := buscar(t, consorcio["vaijunto"].endereco, "Salvador", "Vitória da Conquista", "2026-10-01")

	proibidas := map[string]string{
		"car-6": "margem de baldeação: sai 15 min após car-1 chegar a Jequié",
		"car-7": "é de 02/10: fora da data como primeira perna, e 24 h de espera como conexão",
		"car-8": "fecha um ciclo com car-1, voltando a Salvador",
	}
	for i, it := range resposta.Itinerarios {
		for _, trecho := range it.Trechos {
			_, sufixo, _ := strings.Cut(trecho.CaronaID, ":")
			if motivo, proibida := proibidas[sufixo]; proibida {
				t.Errorf("itinerário %d usa o controle negativo %s (%s) — %s",
					i+1, sufixo, trecho.CaronaID, motivo)
			}
		}
	}
}

// A semente da demonstração: Salvador → Petrópolis em 05/11/2026 devolve
// exatamente o itinerário da seção 12.2, com uma perna em cada empresa.
//
// É o cenário D-1 da seção 12.3, e o critério de pronto da fase.
func TestBuscaDaSementeSalvadorPetropolis(t *testing.T) {
	consorcio := subirConsorcioDeBusca(t, sementesDasTresEmpresas(t), nil)

	resposta := buscar(t, consorcio["vaijunto"].endereco, "Salvador", "Petrópolis", "2026-11-05")

	if resposta.Parcial {
		t.Fatalf("resultado parcial com as três empresas no ar; indisponíveis: %v", resposta.EmpresasIndisponiveis)
	}
	if len(resposta.Itinerarios) != 1 {
		t.Fatalf("itinerários = %d (%v), want exatamente 1", len(resposta.Itinerarios), assinaturasSemPrefixo(resposta.Itinerarios))
	}

	it := resposta.Itinerarios[0]
	if got := assinaturaSemPrefixo(it); got != "car-1:0-2|car-1:0-2|car-1:0-2" {
		t.Errorf("assinatura = %q, want três pernas 0-2", got)
	}
	if it.Baldeacoes != 2 {
		t.Errorf("baldeacoes = %d, want 2", it.Baldeacoes)
	}
	// R$ 430,00 = 9500 (joao) + 21500 (rafael) + 12000 (marcos).
	if it.PrecoTotalCentavos != 43000 {
		t.Errorf("preço total = %d centavos, want 43000 (R$ 430,00)", it.PrecoTotalCentavos)
	}

	fuso := dominio.FusoDasCidades()
	querPartida := time.Date(2026, 11, 5, 5, 0, 0, 0, fuso)
	querChegada := time.Date(2026, 11, 6, 14, 30, 0, 0, fuso)
	if !it.Partida.Equal(querPartida) {
		t.Errorf("partida = %s, want %s", it.Partida, querPartida)
	}
	if !it.Chegada.Equal(querChegada) {
		t.Errorf("chegada = %s, want %s", it.Chegada, querChegada)
	}

	// Uma perna em cada empresa, nesta ordem: é o que a demonstração mostra.
	querEmpresas := []string{"vaijunto", "rotacapixaba", "rotafluminense"}
	if len(it.Trechos) != len(querEmpresas) {
		t.Fatalf("trechos = %d, want 3", len(it.Trechos))
	}
	for i, quer := range querEmpresas {
		if it.Trechos[i].Empresa != quer {
			t.Errorf("trecho %d: empresa = %q, want %q", i, it.Trechos[i].Empresa, quer)
		}
	}
}

// TB2 — com um par fora do ar, a resposta é parcial, chega dentro do prazo e
// cita a empresa indisponível (D26, D30, D41).
//
// A empresa é derrubada mas continua na lista de pares, que é exatamente o que
// acontece quando o tutor dá docker kill numa máquina (cenário D-4).
func TestTB2BuscaComParForaDoArVoltaParcialDentroDoPrazo(t *testing.T) {
	consorcio := subirConsorcioDeBusca(t, sementesDasTresEmpresas(t), nil)

	if err := consorcio["rotacapixaba"].rest.Fechar(); err != nil {
		t.Fatalf("derrubar rotacapixaba: %v", err)
	}

	inicio := time.Now()
	// Salvador → Jequié não depende da rotacapixaba: é vaijunto:car-1 até Feira
	// mais vaijunto:car-2. A busca precisa continuar entregando o que não
	// dependia da empresa que caiu.
	resposta := buscar(t, consorcio["vaijunto"].endereco, "Salvador", "Jequié", "2026-11-05")
	decorrido := time.Since(inicio)

	if !resposta.Parcial {
		t.Errorf("parcial = false, want true: a rotacapixaba está fora do ar")
	}
	if len(resposta.EmpresasIndisponiveis) != 1 || resposta.EmpresasIndisponiveis[0] != "rotacapixaba" {
		t.Errorf("empresas_indisponiveis = %v, want [rotacapixaba]", resposta.EmpresasIndisponiveis)
	}

	// O prazo é único para o leque inteiro (D41). A margem cobre o resto da
	// requisição; o que o teste recusa é a busca ter esperado por par.
	if limite := 3 * prazoBuscaNosTestes; decorrido > limite {
		t.Errorf("a busca levou %s, acima do limite de %s: o prazo não é único para o leque", decorrido, limite)
	}

	// E o que não dependia da empresa caída continua lá.
	if len(resposta.Itinerarios) == 0 {
		t.Errorf("nenhum itinerário: a queda de um par não pode apagar o que as outras empresas oferecem")
	}
	for _, it := range resposta.Itinerarios {
		for _, trecho := range it.Trechos {
			if trecho.Empresa == "rotacapixaba" {
				t.Errorf("itinerário com perna da empresa fora do ar: %s", trecho.CaronaID)
			}
		}
	}
}

// Com as três no ar, a mesma busca não é parcial: a asserção do TB2 prova algo
// só se o caso normal for o contrário.
func TestBuscaNaoEhParcialComTodasNoAr(t *testing.T) {
	consorcio := subirConsorcioDeBusca(t, sementesDasTresEmpresas(t), nil)

	resposta := buscar(t, consorcio["vaijunto"].endereco, "Salvador", "Jequié", "2026-11-05")

	if resposta.Parcial || len(resposta.EmpresasIndisponiveis) != 0 {
		t.Errorf("parcial = %t, indisponíveis = %v, want false e vazia", resposta.Parcial, resposta.EmpresasIndisponiveis)
	}
}

// Uma empresa sozinha nunca responde parcial: não há par de quem ficar devendo
// resposta. É o caminho do P1, e o que mantém a regressão inteira válida.
func TestBuscaDeUmaEmpresaSoNuncaEhParcial(t *testing.T) {
	resposta := buscar(t, subirServidor(t), "Salvador", "Vitória da Conquista", "2026-10-01")

	if resposta.Parcial {
		t.Errorf("parcial = true sem par nenhum na lista")
	}
	if len(resposta.EmpresasIndisponiveis) != 0 {
		t.Errorf("empresas_indisponiveis = %v, want vazia", resposta.EmpresasIndisponiveis)
	}
	if len(resposta.Itinerarios) != 4 {
		t.Errorf("itinerários = %d, want os 4 da seção 9.2: %v", len(resposta.Itinerarios), assinaturasSemPrefixo(resposta.Itinerarios))
	}
}

// parFalso é um servidor que fala a API do consórcio e devolve as caronas que o
// teste escolher, inclusive malformadas.
//
// Não é o nosso handler: o ponto do passo 5 é justamente o par que **não** roda
// a nossa versão do binário, e um handler nosso nunca produziria o dado que
// este teste precisa injetar.
func parFalso(t *testing.T, empresa string, caronas []federacao.CaronaFederada) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Os cabeçalhos da seção 1.1 da API, para que a resposta seja aceita
		// como vinda de um par de verdade.
		w.Header().Set("X-VaiJunto-Empresa", empresa)
		w.Header().Set("X-VaiJunto-Lamport", "1")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(federacao.RespostaCaronas{Empresa: empresa, Caronas: caronas})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// Passo 5 — uma carona recebida com cidade fora do catálogo é descartada, com
// registro no log, e o resto do que aquele par mandou continua valendo.
//
// É a proteção contra um par rodando outra versão do binário (D33). O descarte
// não torna a empresa indisponível: uma carona que recusamos é diferente de uma
// empresa de quem não sabemos nada.
func TestPasso5DescartaCaronaComCidadeForaDoCatalogo(t *testing.T) {
	fuso := dominio.FusoDasCidades()
	invalida := federacao.CaronaFederada{
		ID:            "rotaparaense:car-9",
		Empresa:       "rotaparaense",
		MotoristaNome: "Alguém",
		// Belém não está no catálogo do consórcio (D33).
		Rota:           []string{"Salvador", "Belém"},
		Horarios:       []time.Time{time.Date(2026, 10, 1, 6, 0, 0, 0, fuso), time.Date(2026, 10, 1, 18, 0, 0, 0, fuso)},
		PrecosCentavos: []int{5000},
		Assentos:       4,
		Livres:         []int{4},
	}
	// Uma válida ao lado, para provar que o descarte é por carona e não pelo par.
	valida := federacao.CaronaFederada{
		ID:             "rotaparaense:car-10",
		Empresa:        "rotaparaense",
		MotoristaNome:  "Outro Alguém",
		Rota:           []string{"Salvador", "Vitória da Conquista"},
		Horarios:       []time.Time{time.Date(2026, 10, 1, 7, 0, 0, 0, fuso), time.Date(2026, 10, 1, 19, 0, 0, 0, fuso)},
		PrecosCentavos: []int{7000},
		Assentos:       4,
		Livres:         []int{4},
	}

	url := parFalso(t, "rotaparaense", []federacao.CaronaFederada{invalida, valida})
	consorcio := subirConsorcioDeBusca(t, distribuirFixtureDoP1(t), map[string]string{"rotaparaense": url})
	vaijunto := consorcio["vaijunto"]

	resposta := buscar(t, vaijunto.endereco, "Salvador", "Vitória da Conquista", "2026-10-01")

	// O par respondeu, então não está indisponível.
	for _, empresa := range resposta.EmpresasIndisponiveis {
		if empresa == "rotaparaense" {
			t.Errorf("rotaparaense entrou como indisponível: carona descartada não é empresa fora do ar")
		}
	}
	// A inválida não aparece em itinerário nenhum...
	for i, it := range resposta.Itinerarios {
		for _, trecho := range it.Trechos {
			if trecho.CaronaID == invalida.ID {
				t.Errorf("itinerário %d usa a carona descartada %s", i+1, invalida.ID)
			}
		}
	}
	// ...e a válida do mesmo par aparece, como direta Salvador → Conquista.
	if !usa(resposta.Itinerarios, valida.ID) {
		t.Errorf("a carona válida %s do mesmo par não entrou na busca: %v", valida.ID, assinaturasSemPrefixo(resposta.Itinerarios))
	}
	// E a linha do descarte está no registro (seção 6, passo 5).
	esperarLinha(t, vaijunto.registro, "carona \"rotaparaense:car-9\" descartada")
}

// Um par não fala pelas caronas de outro: a carona viria com a empresa errada no
// PREPARAR, e o 2PC a endereçaria a quem não é dono do trecho.
func TestPasso5DescartaCaronaDeOutraEmpresa(t *testing.T) {
	fuso := dominio.FusoDasCidades()
	alheia := federacao.CaronaFederada{
		ID:             "vaijunto:car-99",
		Empresa:        "vaijunto", // mas quem responde é a rotaparaense
		MotoristaNome:  "Impostor",
		Rota:           []string{"Salvador", "Vitória da Conquista"},
		Horarios:       []time.Time{time.Date(2026, 10, 1, 7, 0, 0, 0, fuso), time.Date(2026, 10, 1, 19, 0, 0, 0, fuso)},
		PrecosCentavos: []int{100},
		Assentos:       4,
		Livres:         []int{4},
	}

	url := parFalso(t, "rotaparaense", []federacao.CaronaFederada{alheia})
	consorcio := subirConsorcioDeBusca(t, distribuirFixtureDoP1(t), map[string]string{"rotaparaense": url})
	vaijunto := consorcio["vaijunto"]

	resposta := buscar(t, vaijunto.endereco, "Salvador", "Vitória da Conquista", "2026-10-01")

	if usa(resposta.Itinerarios, alheia.ID) {
		t.Errorf("a busca aceitou de rotaparaense uma carona declarada da vaijunto")
	}
	esperarLinha(t, vaijunto.registro, "não é quem respondeu")
}

// A resposta de /federacao/caronas de cada empresa traz só as caronas dela, com
// a janela da D40 aplicada: é a privacidade entre empresas (D32) do lado do
// dono, e a prova de que nenhuma empresa guarda dado alheio (D26).
func TestCadaEmpresaSoDevolveAsCaronasDela(t *testing.T) {
	consorcio := subirConsorcioDeBusca(t, distribuirFixtureDoP1(t), nil)

	// A busca federada já rodou? Não importa: nada do que ela descobre fica
	// guardado. Cada empresa continua conhecendo só as suas.
	buscar(t, consorcio["vaijunto"].endereco, "Salvador", "Vitória da Conquista", "2026-10-01")

	desde, ate := dominio.JanelaDaBusca(time.Date(2026, 10, 1, 0, 0, 0, 0, dominio.FusoDasCidades()))
	for nome, e := range consorcio {
		locais := e.estado.CaronasNaJanela(desde, ate)
		if len(locais) == 0 {
			t.Errorf("empresa %s não tem carona nenhuma na janela", nome)
		}
		for _, c := range locais {
			if c.Empresa != nome {
				t.Errorf("empresa %s guarda carona de %s (%s): não há cache nem réplica das caronas alheias (D26)", nome, c.Empresa, c.ID)
			}
		}
	}
}

// Busca inválida é recusada antes de qualquer chamada a par: o pedido errado
// não gasta uma rodada de rede.
func TestBuscaInvalidaNaoConsultaPares(t *testing.T) {
	consorcio := subirConsorcioDeBusca(t, sementesDasTresEmpresas(t), nil)
	endereco := consorcio["vaijunto"].endereco

	casos := []struct {
		nome            string
		origem, destino string
		codigo          string
	}{
		{"cidade desconhecida", "Macapá", "Petrópolis", protocolo.CodigoCidadeDesconhecida},
		{"origem igual ao destino", "Salvador", "Salvador", protocolo.CodigoRotaInvalida},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			c := conectar(t, endereco)
			c.entrar("maria", "abcd")
			c.exigirErro(protocolo.TipoBuscarItinerarios, protocolo.BuscarItinerariosRequisicao{
				Origem: caso.origem, Destino: caso.destino, Data: "2026-11-05",
			}, caso.codigo)
		})
	}

	// Nenhuma linha de chamada a par no registro da vaijunto: a validação
	// aconteceu antes do leque.
	if linhas := consorcio["vaijunto"].registro.String(); strings.Contains(linhas, "/federacao/caronas") {
		t.Errorf("a busca inválida consultou pares:\n%s", linhas)
	}
}

// usa responde se algum itinerário da lista contém a carona.
func usa(itinerarios []protocolo.Itinerario, caronaID string) bool {
	for _, it := range itinerarios {
		for _, trecho := range it.Trechos {
			if trecho.CaronaID == caronaID {
				return true
			}
		}
	}
	return false
}
