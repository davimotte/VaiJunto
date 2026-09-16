// Package testes reúne os testes de integração e de carga do VAIJUNTO.
//
// Diferente dos testes de internal/, estes exercitam o sistema pela mesma
// porta que os clientes reais usam: abrem socket TCP contra um servidor de
// verdade, escrevem linhas JSON e leem as respostas. Nenhuma função interna é
// chamada diretamente — se o enquadramento, o envelope ou o roteamento
// quebrarem, é aqui que aparece.
//
// É também a exceção deliberada de D15: um teste não usa o menu interativo,
// ele fala o protocolo direto, porque precisa controlar temporização e
// disparar requisições simultâneas.
package testes

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
	"vaijunto/internal/servidor"
)

const (
	caminhoUsuarios = "../dados/usuarios.json"
	caminhoCaronas  = "../dados/caronas.json"

	// caronasDeJoaoNaCarga é quantas caronas joao tem no cenário da seção 9.2
	// do PROJETO.md: car-1 e car-5. Os testes que publicam como joao somam as
	// publicadas a este número.
	caronasDeJoaoNaCarga = 2

	// Toda leitura de socket tem prazo: um teste que trava esperando resposta
	// esconde o defeito atrás de um timeout de suíte, em vez de apontá-lo.
	prazoLeitura = 5 * time.Second
)

// TestMain roda a suíte inteira com o fuso local em UTC, que é o que o
// servidor encontra dentro do contêiner Alpine (PROJETO.md, seção 10.1).
//
// Sem isto, a máquina de desenvolvimento em -03:00 esconde qualquer código
// que dependa de time.Local: o teste passa aqui e o sistema erra na
// apresentação. A atribuição acontece antes de m.Run, com uma única goroutine
// viva, então não há corrida com os servidores que os testes sobem.
func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}

// subirServidor sobe um servidor com o estado da carga de demonstração em uma
// porta efêmera de loopback e devolve o endereço.
//
// Cada teste chama esta função e ganha um Estado próprio: publicar carona
// altera o estado, e testes que compartilhassem o mesmo servidor passariam a
// depender da ordem em que rodam.
func subirServidor(t *testing.T) string {
	t.Helper()

	estado, err := dominio.CarregarEstado(caminhoUsuarios, caminhoCaronas)
	if err != nil {
		t.Fatalf("carga inicial: %v", err)
	}
	s, err := servidor.Escutar("127.0.0.1:0", estado)
	if err != nil {
		t.Fatalf("escutar: %v", err)
	}

	// Aceitar só retorna quando o listener fecha; o erro daí é o fechamento
	// em si, esperado no fim do teste.
	go func() { _ = s.Aceitar() }()
	t.Cleanup(func() { _ = s.Fechar() })

	return s.Endereco()
}

// cliente é uma conexão TCP falando o protocolo, do jeito que um cliente
// oficial faria: uma conexão por sessão, reaproveitada em todas as operações
// (D15).
type cliente struct {
	t      *testing.T
	conn   net.Conn
	leitor *protocolo.LeitorMensagens
	seq    int
}

func conectar(t *testing.T, endereco string) *cliente {
	t.Helper()

	conn, err := net.DialTimeout("tcp", endereco, prazoLeitura)
	if err != nil {
		t.Fatalf("conectar em %s: %v", endereco, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return &cliente{t: t, conn: conn, leitor: protocolo.NovoLeitorMensagens(conn)}
}

// enviarBruto escreve a linha exatamente como recebida e lê uma resposta.
// Serve aos casos em que o teste precisa de um JSON que as structs de
// protocolo não conseguem produzir, como um campo com o tipo errado.
func (c *cliente) enviarBruto(linha string) protocolo.Resposta {
	c.t.Helper()

	if err := c.conn.SetDeadline(time.Now().Add(prazoLeitura)); err != nil {
		c.t.Fatalf("prazo da conexão: %v", err)
	}
	if _, err := c.conn.Write([]byte(linha + "\n")); err != nil {
		c.t.Fatalf("escrever %q: %v", linha, err)
	}

	bruta, err := c.leitor.LerLinha()
	if err != nil {
		c.t.Fatalf("ler resposta de %q: %v", linha, err)
	}
	resp, err := protocolo.DecodificarResposta(bruta)
	if err != nil {
		c.t.Fatalf("decodificar resposta %q: %v", bruta, err)
	}
	return resp
}

// enviar monta o envelope da seção 2.1 com um id sequencial e devolve a
// resposta, conferindo de passagem que o id foi ecoado — o pareamento
// requisição/resposta é o que sustenta o teste de carga da fase 9.
func (c *cliente) enviar(tipo string, dados any) protocolo.Resposta {
	c.t.Helper()

	c.seq++
	id := fmt.Sprintf("req-%d", c.seq)

	corpo, err := json.Marshal(dados)
	if err != nil {
		c.t.Fatalf("montar dados de %s: %v", tipo, err)
	}
	linha, err := json.Marshal(protocolo.Requisicao{ID: id, Tipo: tipo, Dados: corpo})
	if err != nil {
		c.t.Fatalf("montar envelope de %s: %v", tipo, err)
	}

	resp := c.enviarBruto(string(linha))
	if resp.ID != id {
		c.t.Fatalf("%s: id ecoado = %q, want %q", tipo, resp.ID, id)
	}
	return resp
}

// exigirOK falha o teste se a operação não tiver sido bem-sucedida, e
// decodifica o "dados" da resposta em destino.
func (c *cliente) exigirOK(tipo string, dados any, destino any) {
	c.t.Helper()

	resp := c.enviar(tipo, dados)
	if resp.Status != protocolo.StatusOK {
		c.t.Fatalf("%s: status = %q, codigo = %q, mensagem = %q", tipo, resp.Status, resp.Codigo, resp.Mensagem)
	}
	if destino != nil {
		if err := json.Unmarshal(resp.Dados, destino); err != nil {
			c.t.Fatalf("%s: decodificar dados %s: %v", tipo, resp.Dados, err)
		}
	}
}

// exigirErro falha o teste se a operação não tiver sido recusada com o código
// esperado da seção 6.
func (c *cliente) exigirErro(tipo string, dados any, codigo string) {
	c.t.Helper()

	resp := c.enviar(tipo, dados)
	if resp.Status != protocolo.StatusErro || resp.Codigo != codigo {
		c.t.Fatalf("%s: got status=%q codigo=%q, want ERRO/%s (mensagem: %q)", tipo, resp.Status, resp.Codigo, codigo, resp.Mensagem)
	}
	if resp.Mensagem == "" {
		c.t.Errorf("%s: resposta de erro sem mensagem legível", tipo)
	}
}

// entrar faz LOGIN e devolve os dados da resposta.
func (c *cliente) entrar(usuario, senha string) protocolo.LoginResposta {
	c.t.Helper()

	var resposta protocolo.LoginResposta
	c.exigirOK(protocolo.TipoLogin, protocolo.LoginRequisicao{Usuario: usuario, Senha: senha}, &resposta)
	return resposta
}

// vazio é o payload das operações sem campos (seção 2.1: objeto vazio).
var vazio = struct{}{}

// futuro devolve um instante futuro no fuso de Brasília, com o offset
// explícito que a seção 3 exige.
func futuro(horas int) time.Time {
	return time.Now().In(time.FixedZone("-03:00", -3*60*60)).Add(time.Duration(horas) * time.Hour).Truncate(time.Second)
}

// --- Sessão: LOGIN, LOGOUT e identidade ligada à conexão (D08) ---

// TestLoginDevolvePerfil confere a seção 5.2: LOGIN responde usuário, nome e
// perfil do usuário carregado de dados/usuarios.json.
func TestLoginDevolvePerfil(t *testing.T) {
	c := conectar(t, subirServidor(t))

	entrada := c.entrar("joao", "1234")
	if entrada.Usuario != "joao" || entrada.Nome != "João Silva" || entrada.Perfil != protocolo.PerfilMotorista {
		t.Fatalf("resposta de LOGIN = %+v", entrada)
	}
}

// TestLoginRecusado confere que credencial errada não autentica a conexão.
//
// A última afirmação é a que importa: depois da recusa, a operação de
// motorista continua respondendo NAO_AUTENTICADO. Uma implementação que
// gravasse a identidade antes de validar a senha passaria nas duas primeiras
// e falharia aqui.
func TestLoginRecusado(t *testing.T) {
	c := conectar(t, subirServidor(t))

	c.exigirErro(protocolo.TipoLogin, protocolo.LoginRequisicao{Usuario: "joao", Senha: "errada"}, protocolo.CodigoCredenciaisInvalidas)
	c.exigirErro(protocolo.TipoLogin, protocolo.LoginRequisicao{Usuario: "ninguem", Senha: "1234"}, protocolo.CodigoCredenciaisInvalidas)
	c.exigirErro(protocolo.TipoLogin, protocolo.LoginRequisicao{Usuario: "joao"}, protocolo.CodigoCampoInvalido)
	c.exigirErro(protocolo.TipoLogin, vazio, protocolo.CodigoCampoInvalido)
	c.exigirErro(protocolo.TipoLogin, map[string]any{"usuario": 5, "senha": "1234"}, protocolo.CodigoCampoInvalido)

	c.exigirErro(protocolo.TipoListarMinhasCaronas, vazio, protocolo.CodigoNaoAutenticado)
}

// TestLoginEmConexaoJaAutenticada confere a seção 4: JA_AUTENTICADO, e a
// sessão anterior permanece intacta.
func TestLoginEmConexaoJaAutenticada(t *testing.T) {
	c := conectar(t, subirServidor(t))
	c.entrar("joao", "1234")

	c.exigirErro(protocolo.TipoLogin, protocolo.LoginRequisicao{Usuario: "carlos", Senha: "1234"}, protocolo.CodigoJaAutenticado)

	// A tentativa recusada não pode ter trocado a identidade da conexão: as
	// caronas listadas ainda têm que ser as do joão.
	var lista protocolo.ListarMinhasCaronasResposta
	c.exigirOK(protocolo.TipoListarMinhasCaronas, protocolo.ListarMinhasCaronasRequisicao{}, &lista)
	for _, carona := range lista.Caronas {
		if carona.CaronaID == "car-2" {
			t.Fatalf("a conexão assumiu a identidade de carlos após um LOGIN recusado")
		}
	}
}

// TestLogoutDesautenticaSemFechar confere a seção 5.3: LOGOUT tira a
// autenticação e mantém a conexão aberta, pronta para um novo LOGIN.
//
// O segundo login, com outro perfil e na mesma conexão, é a prova de que a
// identidade está mesmo ligada à conexão e não a um token: nada foi devolvido
// ao cliente para ele reapresentar.
func TestLogoutDesautenticaSemFechar(t *testing.T) {
	c := conectar(t, subirServidor(t))
	c.entrar("joao", "1234")

	c.exigirOK(protocolo.TipoLogout, vazio, nil)
	c.exigirErro(protocolo.TipoListarMinhasCaronas, vazio, protocolo.CodigoNaoAutenticado)

	// A conexão continua viva: PING responde e um novo LOGIN é aceito.
	c.exigirOK(protocolo.TipoPing, vazio, nil)
	entrada := c.entrar("maria", "abcd")
	if entrada.Perfil != protocolo.PerfilPassageiro {
		t.Fatalf("perfil após o segundo login = %q, want PASSAGEIRO", entrada.Perfil)
	}
}

// TestSemLoginSoLoginEPing confere a regra da seção 4: antes de autenticar,
// apenas LOGIN e PING são aceitos.
//
// LOGOUT entra na lista porque a seção 5 o marca como "autenticado": sair sem
// ter entrado é a mesma violação que qualquer outra operação.
func TestSemLoginSoLoginEPing(t *testing.T) {
	c := conectar(t, subirServidor(t))

	c.exigirOK(protocolo.TipoPing, vazio, nil)

	for _, tipo := range []string{
		protocolo.TipoLogout,
		protocolo.TipoPublicarCarona,
		protocolo.TipoListarMinhasCaronas,
		protocolo.TipoDetalharCarona,
	} {
		c.exigirErro(tipo, vazio, protocolo.CodigoNaoAutenticado)
	}
}

// TestPerfilIncorreto confere a tabela de perfis da seção 5: um passageiro
// autenticado não executa operação de motorista.
//
// O código precisa ser PERFIL_INCORRETO, e não NAO_AUTENTICADO: a conexão
// está autenticada, o que não serve é o perfil.
func TestPerfilIncorreto(t *testing.T) {
	c := conectar(t, subirServidor(t))
	c.entrar("maria", "abcd")

	c.exigirErro(protocolo.TipoPublicarCarona, vazio, protocolo.CodigoPerfilIncorreto)
	c.exigirErro(protocolo.TipoListarMinhasCaronas, vazio, protocolo.CodigoPerfilIncorreto)
	c.exigirErro(protocolo.TipoDetalharCarona, protocolo.DetalharCaronaRequisicao{CaronaID: "car-1"}, protocolo.CodigoPerfilIncorreto)

	// O perfil é verificado antes do payload: nem o carona_id válido de car-1
	// (que existe e é do joão) muda o resultado.
	c.exigirOK(protocolo.TipoPing, vazio, nil)
}

// TestIdentidadeEhPorConexao é o teste central de D08: duas conexões
// simultâneas com usuários diferentes não se confundem.
//
// Se a identidade estivesse em qualquer lugar compartilhado — uma variável de
// pacote, um mapa global sem chave por conexão —, uma das listagens traria as
// caronas do outro motorista.
func TestIdentidadeEhPorConexao(t *testing.T) {
	endereco := subirServidor(t)

	joao := conectar(t, endereco)
	carlos := conectar(t, endereco)
	anonimo := conectar(t, endereco)

	joao.entrar("joao", "1234")
	carlos.entrar("carlos", "1234")

	listar := func(c *cliente) map[string]bool {
		var lista protocolo.ListarMinhasCaronasResposta
		c.exigirOK(protocolo.TipoListarMinhasCaronas, protocolo.ListarMinhasCaronasRequisicao{}, &lista)
		ids := map[string]bool{}
		for _, carona := range lista.Caronas {
			ids[carona.CaronaID] = true
		}
		return ids
	}

	// Alternar as chamadas entre as conexões é proposital: o servidor tem que
	// devolver a identidade certa a cada requisição, e não a da última que
	// autenticou.
	deJoao := listar(joao)
	deCarlos := listar(carlos)
	deJoaoDeNovo := listar(joao)

	if !deJoao["car-1"] || !deJoao["car-5"] || len(deJoao) != caronasDeJoaoNaCarga {
		t.Fatalf("caronas de joao = %v, want car-1 e car-5", deJoao)
	}
	if !deCarlos["car-2"] || !deCarlos["car-6"] || !deCarlos["car-8"] || len(deCarlos) != 3 {
		t.Fatalf("caronas de carlos = %v, want car-2, car-6 e car-8", deCarlos)
	}
	if len(deJoaoDeNovo) != caronasDeJoaoNaCarga || !deJoaoDeNovo["car-1"] {
		t.Fatalf("a segunda listagem de joao mudou: %v", deJoaoDeNovo)
	}

	// A terceira conexão nunca autenticou e não é afetada pelas outras duas.
	anonimo.exigirErro(protocolo.TipoListarMinhasCaronas, vazio, protocolo.CodigoNaoAutenticado)
}

// TestSessaoMorreComAConexao confere a outra metade de D08: encerrada a
// conexão, a sessão desaparece sozinha. Uma nova conexão do mesmo endereço
// começa sem autenticação, sem que nada precise ser expirado ou coletado.
func TestSessaoMorreComAConexao(t *testing.T) {
	endereco := subirServidor(t)

	primeira := conectar(t, endereco)
	primeira.entrar("joao", "1234")
	if err := primeira.conn.Close(); err != nil {
		t.Fatalf("fechar conexão: %v", err)
	}

	segunda := conectar(t, endereco)
	segunda.exigirErro(protocolo.TipoListarMinhasCaronas, vazio, protocolo.CodigoNaoAutenticado)
}

// --- PUBLICAR_CARONA (seção 5.4) ---

// parada monta um elemento de "paradas" de PUBLICAR_CARONA (seção 5.4). O
// horário vai como string RFC 3339, do jeito que trafega na linha.
func parada(cidade string, horario time.Time) map[string]any {
	return map[string]any{"cidade": cidade, "horario": horario.Format(time.RFC3339)}
}

// publicacao é o payload de PUBLICAR_CARONA montado como mapa, e não com a
// struct do protocolo, para que os testes de validação possam omitir campos e
// trocar tipos à vontade.
func publicacao(assentos int, precos []int, paradas ...map[string]any) map[string]any {
	// Sem paradas, o variádico chega nil e serializaria como null — que é
	// campo ausente, e não lista vazia. Os dois casos têm códigos diferentes.
	if paradas == nil {
		paradas = []map[string]any{}
	}
	return map[string]any{
		"paradas":         paradas,
		"assentos":        assentos,
		"precos_centavos": precos,
	}
}

// TestPublicarCaronaDevolveParadasInformadas confere a seção 5.4 e a D09: o
// servidor não calcula rota nem horário, e a resposta devolve exatamente as
// paradas que o motorista informou.
//
// Rota e horários foram escolhidos para que nenhuma derivação pudesse
// produzi-los: Jequié → Salvador → Vitória da Conquista não é sequência em
// linha, e 50 minutos entre Jequié e Salvador não é duração de trajeto nenhuma.
func TestPublicarCaronaDevolveParadasInformadas(t *testing.T) {
	c := conectar(t, subirServidor(t))
	c.entrar("joao", "1234")

	partida := futuro(48)
	horarios := []time.Time{partida, partida.Add(50 * time.Minute), partida.Add(12*time.Hour + 10*time.Minute)}
	rota := []string{"Jequié", "Salvador", "Vitória da Conquista"}

	var publicada protocolo.PublicarCaronaResposta
	c.exigirOK(protocolo.TipoPublicarCarona,
		publicacao(3, []int{1000, 2000},
			parada(rota[0], horarios[0]), parada(rota[1], horarios[1]), parada(rota[2], horarios[2])),
		&publicada)

	if publicada.CaronaID == "" {
		t.Fatalf("carona publicada sem carona_id")
	}
	if strings.Join(publicada.Rota, "|") != strings.Join(rota, "|") {
		t.Fatalf("rota = %v, want %v", publicada.Rota, rota)
	}
	if len(publicada.Horarios) != len(horarios) {
		t.Fatalf("horarios = %v, want %v", publicada.Horarios, horarios)
	}
	for i, querido := range horarios {
		if !publicada.Horarios[i].Equal(querido) {
			t.Fatalf("horarios[%d] = %v, want %v", i, publicada.Horarios[i], querido)
		}
	}
}

// TestPublicarCaronaPreservaFuso confere a seção 3 e D11: o instante volta em
// RFC 3339 com fuso explícito, e não normalizado para UTC.
//
// O teste olha o JSON cru de propósito: decodificar em time.Time esconderia a
// diferença, porque o instante é o mesmo — o que muda é o que o cliente
// mostra na tela do motorista.
func TestPublicarCaronaPreservaFuso(t *testing.T) {
	c := conectar(t, subirServidor(t))
	c.entrar("joao", "1234")

	partida := futuro(72)
	resp := c.enviar(protocolo.TipoPublicarCarona,
		publicacao(2, []int{3000},
			parada("Salvador", partida), parada("Feira de Santana", partida.Add(2*time.Hour))))
	if resp.Status != protocolo.StatusOK {
		t.Fatalf("PUBLICAR_CARONA: %q %q", resp.Codigo, resp.Mensagem)
	}

	var bruto struct {
		Horarios []string `json:"horarios"`
	}
	if err := json.Unmarshal(resp.Dados, &bruto); err != nil {
		t.Fatalf("decodificar horarios: %v", err)
	}
	for i, instante := range bruto.Horarios {
		if _, err := time.Parse(time.RFC3339, instante); err != nil {
			t.Fatalf("horarios[%d] = %q não é RFC 3339: %v", i, instante, err)
		}
		if len(instante) < 6 || instante[len(instante)-6:] != "-03:00" {
			t.Fatalf("horarios[%d] = %q perdeu o fuso -03:00", i, instante)
		}
	}
}

// TestBuscarDataNoFusoDasCidades confere que a data da busca é o dia civil
// das cidades atendidas, e não o dia no fuso da máquina do servidor.
//
// Uma carona que sai às 22:00 em Salvador sai à 01:00 do dia seguinte em UTC.
// Se o servidor ler a data em time.Local (UTC no contêiner, e na suíte por
// causa de TestMain), a carona some da busca do seu dia e aparece na do dia
// seguinte.
func TestBuscarDataNoFusoDasCidades(t *testing.T) {
	endereco := subirServidor(t)

	motorista := conectar(t, endereco)
	motorista.entrar("joao", "1234")

	dia := futuro(72)
	partida := time.Date(dia.Year(), dia.Month(), dia.Day(), 22, 0, 0, 0, dia.Location())
	motorista.exigirOK(protocolo.TipoPublicarCarona,
		publicacao(2, []int{2000},
			parada("Salvador", partida), parada("Feira de Santana", partida.Add(90*time.Minute))), nil)

	passageiro := conectar(t, endereco)
	passageiro.entrar("pedro", "abcd")

	// encontrada responde se a busca da data traz algum itinerário partindo
	// no instante publicado. Compara com Equal porque o mesmo instante pode
	// voltar com outra representação de fuso.
	encontrada := func(data time.Time) bool {
		var busca protocolo.BuscarItinerariosResposta
		passageiro.exigirOK(protocolo.TipoBuscarItinerarios, protocolo.BuscarItinerariosRequisicao{
			Origem: "Salvador", Destino: "Feira de Santana", Data: data.Format("2006-01-02"),
		}, &busca)
		for _, it := range busca.Itinerarios {
			if it.Partida.Equal(partida) {
				return true
			}
		}
		return false
	}

	if !encontrada(partida) {
		t.Errorf("carona das 22:00 de %s ausente na busca do próprio dia", partida.Format("2006-01-02"))
	}
	if seguinte := partida.AddDate(0, 0, 1); encontrada(seguinte) {
		t.Errorf("carona das 22:00 de %s apareceu na busca de %s", partida.Format("2006-01-02"), seguinte.Format("2006-01-02"))
	}
}

// TestPublicarCaronaValidacoes percorre as recusas da seção 5.4, cada uma com
// o código que a seção 6 manda.
//
// A distinção entre CAMPO_INVALIDO e PARTIDA_INVALIDA é a regra que o
// servidor adota: tipo JSON errado ou campo ausente é campo inválido; string
// que existe mas não é RFC 3339 é partida inválida, em qualquer parada.
func TestPublicarCaronaValidacoes(t *testing.T) {
	c := conectar(t, subirServidor(t))
	c.entrar("joao", "1234")

	partida := futuro(48)
	em := func(horas int) time.Time { return partida.Add(time.Duration(horas) * time.Hour) }
	salvador, feira, jequie := parada("Salvador", em(0)), parada("Feira de Santana", em(2)), parada("Jequié", em(5))

	// comHorario troca o horário de uma parada por um valor cru qualquer, para
	// os casos que precisam de algo que não seja um time.Time bem formado.
	comHorario := func(cidade string, horario any) map[string]any {
		return map[string]any{"cidade": cidade, "horario": horario}
	}

	casos := []struct {
		nome   string
		dados  any
		codigo string
	}{
		// Cidades (seção 3.1).
		{"cidade desconhecida", publicacao(2, []int{3000}, parada("Ilhéus", em(0)), feira), protocolo.CodigoCidadeDesconhecida},
		{"grafia divergente", publicacao(2, []int{3000}, parada("salvador", em(0)), feira), protocolo.CodigoCidadeDesconhecida},

		// Estrutura da rota.
		{"lista de paradas vazia", publicacao(2, []int{}), protocolo.CodigoRotaInvalida},
		{"uma parada só", publicacao(2, []int{}, salvador), protocolo.CodigoRotaInvalida},
		{"origem igual ao destino", publicacao(2, []int{3000}, parada("Jequié", em(0)), parada("Jequié", em(2))), protocolo.CodigoRotaInvalida},
		{"cidade repetida no meio", publicacao(2, []int{3000, 3000}, salvador, feira, parada("Salvador", em(4))), protocolo.CodigoRotaInvalida},
		{"horário igual ao anterior", publicacao(2, []int{3000}, salvador, parada("Feira de Santana", em(0))), protocolo.CodigoRotaInvalida},
		{"horário antes do anterior", publicacao(2, []int{3000, 4500}, salvador, jequie, parada("Feira de Santana", em(3))), protocolo.CodigoRotaInvalida},
		{"preços a menos", publicacao(2, []int{3000}, salvador, feira, jequie), protocolo.CodigoRotaInvalida},
		{"preços a mais", publicacao(2, []int{3000, 4500}, salvador, feira), protocolo.CodigoRotaInvalida},

		// Faixa de valores.
		{"preço negativo", publicacao(2, []int{-1}, salvador, feira), protocolo.CodigoCampoInvalido},
		{"sem assentos", publicacao(0, []int{3000}, salvador, feira), protocolo.CodigoCampoInvalido},
		{"assentos negativos", publicacao(-3, []int{3000}, salvador, feira), protocolo.CodigoCampoInvalido},

		// Horários.
		{"primeira parada no passado", publicacao(2, []int{3000}, parada("Salvador", futuro(-2)), feira), protocolo.CodigoPartidaInvalida},
		{"horário sem fuso", publicacao(2, []int{3000}, comHorario("Salvador", "2027-09-15T08:00:00"), feira), protocolo.CodigoPartidaInvalida},
		{"horário com texto qualquer", publicacao(2, []int{3000}, comHorario("Salvador", "amanhã cedo"), feira), protocolo.CodigoPartidaInvalida},
		{"horário malformado numa parada intermediária", publicacao(2, []int{3000, 4500}, salvador, comHorario("Feira de Santana", "10h"), jequie), protocolo.CodigoPartidaInvalida},

		// Forma do payload.
		{"payload vazio", vazio, protocolo.CodigoCampoInvalido},
		{"formato antigo, com origem, destino e partida", map[string]any{
			"origem": "Salvador", "destino": "Feira de Santana",
			"partida": partida.Format(time.RFC3339), "assentos": 2, "precos_centavos": []int{3000},
		}, protocolo.CodigoCampoInvalido},
		{"paradas não é lista", map[string]any{
			"paradas": "Salvador, Feira de Santana", "assentos": 2, "precos_centavos": []int{3000},
		}, protocolo.CodigoCampoInvalido},
		{"parada sem cidade", publicacao(2, []int{3000}, map[string]any{"horario": em(0).Format(time.RFC3339)}, feira), protocolo.CodigoCampoInvalido},
		{"parada sem horário", publicacao(2, []int{3000}, salvador, map[string]any{"cidade": "Feira de Santana"}), protocolo.CodigoCampoInvalido},
		{"parada nula", publicacao(2, []int{3000}, salvador, nil), protocolo.CodigoCampoInvalido},
		{"horário como número", publicacao(2, []int{3000}, comHorario("Salvador", 20260915), feira), protocolo.CodigoCampoInvalido},
		{"sem assentos no payload", map[string]any{
			"paradas": []any{salvador, feira}, "precos_centavos": []int{3000},
		}, protocolo.CodigoCampoInvalido},
		{"assentos como string", map[string]any{
			"paradas": []any{salvador, feira}, "assentos": "2", "precos_centavos": []int{3000},
		}, protocolo.CodigoCampoInvalido},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			// Checagem feita com o t do subteste, e não com c.exigirErro: aquele
			// helper falha pelo t do teste pai, e a primeira recusa errada
			// abortaria a tabela inteira, escondendo as demais.
			resp := c.enviar(protocolo.TipoPublicarCarona, caso.dados)
			if resp.Status != protocolo.StatusErro || resp.Codigo != caso.codigo {
				t.Errorf("got status=%q codigo=%q, want ERRO/%s (mensagem: %q)", resp.Status, resp.Codigo, caso.codigo, resp.Mensagem)
			}
		})
	}

	// Nenhuma das recusas pode ter deixado carona no estado: o joão continua
	// com exatamente as da carga inicial.
	var lista protocolo.ListarMinhasCaronasResposta
	c.exigirOK(protocolo.TipoListarMinhasCaronas, protocolo.ListarMinhasCaronasRequisicao{IncluirCanceladas: true}, &lista)
	if len(lista.Caronas) != caronasDeJoaoNaCarga {
		t.Fatalf("após %d recusas, joao tem %d caronas, want %d", len(casos), len(lista.Caronas), caronasDeJoaoNaCarga)
	}
}

// TestPublicarCaronaGeraIdentificadoresDistintos confere a seção 3: o id é
// opaco e gerado pelo servidor, e duas publicações iguais não colidem.
func TestPublicarCaronaGeraIdentificadoresDistintos(t *testing.T) {
	c := conectar(t, subirServidor(t))
	c.entrar("joao", "1234")

	vistos := map[string]bool{}
	for i := 0; i < 20; i++ {
		var publicada protocolo.PublicarCaronaResposta
		partida := futuro(24)
		c.exigirOK(protocolo.TipoPublicarCarona,
			publicacao(2, []int{3000},
				parada("Salvador", partida), parada("Feira de Santana", partida.Add(2*time.Hour))), &publicada)
		if vistos[publicada.CaronaID] {
			t.Fatalf("carona_id repetido: %q", publicada.CaronaID)
		}
		vistos[publicada.CaronaID] = true
	}
}

// --- LISTAR_MINHAS_CARONAS (seção 5.5) ---

// TestListarMinhasCaronasDetalhaTrechos confere o formato da seção 5.5: cada
// carona traz rota, horários, assentos e um resumo por trecho com preço e
// assentos livres.
func TestListarMinhasCaronasDetalhaTrechos(t *testing.T) {
	c := conectar(t, subirServidor(t))
	c.entrar("joao", "1234")

	var lista protocolo.ListarMinhasCaronasResposta
	c.exigirOK(protocolo.TipoListarMinhasCaronas, protocolo.ListarMinhasCaronasRequisicao{}, &lista)

	// A carga inicial coloca car-5 às 05:30 e car-1 às 06:00: a lista sai
	// ordenada por partida, e não na ordem do arquivo nem na ordem aleatória
	// do mapa — car-1 vem antes de car-5 no arquivo e no identificador.
	ordemEsperada := []string{"car-5", "car-1"}
	if len(lista.Caronas) != len(ordemEsperada) {
		t.Fatalf("joao tem %d caronas, want %d", len(lista.Caronas), len(ordemEsperada))
	}
	for i, id := range ordemEsperada {
		if lista.Caronas[i].CaronaID != id {
			t.Fatalf("caronas[%d] = %q, want %q (lista fora de ordem de partida)", i, lista.Caronas[i].CaronaID, id)
		}
	}

	// car-1 vai de Salvador a Jequié com 3 assentos: dois trechos, ambos
	// livres por inteiro, com os preços da carga.
	car1 := lista.Caronas[1]
	if car1.Assentos != 3 || car1.Cancelada {
		t.Fatalf("car-1 = %+v", car1)
	}
	if len(car1.Trechos) != len(car1.Rota)-1 {
		t.Fatalf("car-1 tem %d trechos para uma rota de %d cidades", len(car1.Trechos), len(car1.Rota))
	}
	esperados := []protocolo.TrechoResumo{
		{Indice: 0, Origem: "Salvador", Destino: "Feira de Santana", PrecoCentavos: 2500, Livres: 3},
		{Indice: 1, Origem: "Feira de Santana", Destino: "Jequié", PrecoCentavos: 3500, Livres: 3},
	}
	for i, querido := range esperados {
		if car1.Trechos[i] != querido {
			t.Fatalf("car-1 trechos[%d] = %+v, want %+v", i, car1.Trechos[i], querido)
		}
	}
}

// TestListarMinhasCaronasIncluiAPublicada confere que a carona recém-criada
// aparece na listagem do próprio motorista e não vaza para outro.
func TestListarMinhasCaronasIncluiAPublicada(t *testing.T) {
	endereco := subirServidor(t)
	joao := conectar(t, endereco)
	carlos := conectar(t, endereco)
	joao.entrar("joao", "1234")
	carlos.entrar("carlos", "1234")

	var publicada protocolo.PublicarCaronaResposta
	partida := futuro(48)
	joao.exigirOK(protocolo.TipoPublicarCarona,
		publicacao(4, []int{3000},
			parada("Salvador", partida), parada("Feira de Santana", partida.Add(2*time.Hour))), &publicada)

	contem := func(c *cliente) bool {
		var lista protocolo.ListarMinhasCaronasResposta
		c.exigirOK(protocolo.TipoListarMinhasCaronas, protocolo.ListarMinhasCaronasRequisicao{}, &lista)
		for _, carona := range lista.Caronas {
			if carona.CaronaID == publicada.CaronaID {
				return true
			}
		}
		return false
	}

	if !contem(joao) {
		t.Fatalf("a carona publicada não apareceu na listagem de quem a publicou")
	}
	if contem(carlos) {
		t.Fatalf("a carona de joao apareceu na listagem de carlos")
	}
}

// TestListarMinhasCaronasCampoInvalido confere que o payload com o tipo errado
// é recusado em vez de tratado como ausente.
func TestListarMinhasCaronasCampoInvalido(t *testing.T) {
	c := conectar(t, subirServidor(t))
	c.entrar("joao", "1234")

	c.exigirErro(protocolo.TipoListarMinhasCaronas, map[string]any{"incluir_canceladas": "sim"}, protocolo.CodigoCampoInvalido)

	// Ausente vale false, que é o caso comum: o payload vazio é válido.
	c.exigirOK(protocolo.TipoListarMinhasCaronas, vazio, nil)
}

// --- DETALHAR_CARONA (seção 5.6) ---

// TestDetalharCaronaListaTrechosVazios confere o formato da seção 5.6 sobre a
// carga inicial, em que ninguém reservou ainda.
//
// A afirmação sobre o JSON cru não é preciosismo: "passageiros":null obrigaria
// o CLI do motorista a tratar nulo antes de iterar, enquanto a lista vazia
// imprime "nenhum passageiro" sem caso especial.
func TestDetalharCaronaListaTrechosVazios(t *testing.T) {
	c := conectar(t, subirServidor(t))
	c.entrar("joao", "1234")

	resp := c.enviar(protocolo.TipoDetalharCarona, protocolo.DetalharCaronaRequisicao{CaronaID: "car-1"})
	if resp.Status != protocolo.StatusOK {
		t.Fatalf("DETALHAR_CARONA: %q %q", resp.Codigo, resp.Mensagem)
	}

	var detalhe protocolo.DetalharCaronaResposta
	if err := json.Unmarshal(resp.Dados, &detalhe); err != nil {
		t.Fatalf("decodificar detalhe: %v", err)
	}
	if detalhe.CaronaID != "car-1" || len(detalhe.Trechos) != 2 {
		t.Fatalf("detalhe = %+v", detalhe)
	}
	esperados := []protocolo.TrechoDetalhado{
		{Indice: 0, Origem: "Salvador", Destino: "Feira de Santana", Livres: 3, Passageiros: []protocolo.PassageiroTrecho{}},
		{Indice: 1, Origem: "Feira de Santana", Destino: "Jequié", Livres: 3, Passageiros: []protocolo.PassageiroTrecho{}},
	}
	for i, querido := range esperados {
		obtido := detalhe.Trechos[i]
		if obtido.Indice != querido.Indice || obtido.Origem != querido.Origem ||
			obtido.Destino != querido.Destino || obtido.Livres != querido.Livres ||
			len(obtido.Passageiros) != 0 {
			t.Fatalf("trechos[%d] = %+v, want %+v", i, obtido, querido)
		}
	}

	if !strings.Contains(string(resp.Dados), `"passageiros":[]`) {
		t.Fatalf("trecho sem passageiros deveria serializar lista vazia, não nulo: %s", resp.Dados)
	}
}

// TestDetalharCaronaRecusas confere os três erros da seção 5.6 acessíveis
// nesta fase, e a distinção entre eles.
//
// Carona inexistente e carona de outro motorista precisam de códigos
// diferentes: só o segundo diz ao motorista que ele errou de carona, e não que
// ela sumiu.
func TestDetalharCaronaRecusas(t *testing.T) {
	c := conectar(t, subirServidor(t))
	c.entrar("joao", "1234")

	// car-2 é do carlos.
	c.exigirErro(protocolo.TipoDetalharCarona, protocolo.DetalharCaronaRequisicao{CaronaID: "car-2"}, protocolo.CodigoNaoEDono)
	c.exigirErro(protocolo.TipoDetalharCarona, protocolo.DetalharCaronaRequisicao{CaronaID: "car-inexistente"}, protocolo.CodigoCaronaNaoEncontrada)
	c.exigirErro(protocolo.TipoDetalharCarona, vazio, protocolo.CodigoCampoInvalido)
	c.exigirErro(protocolo.TipoDetalharCarona, map[string]any{"carona_id": 5}, protocolo.CodigoCampoInvalido)
}

// --- Comportamento da conexão sob uso real ---

// TestConexaoPersistenteComVariasOperacoes confere a seção 1: uma conexão, a
// sessão inteira, várias operações em sequência, cada resposta pareada com sua
// requisição pelo id.
func TestConexaoPersistenteComVariasOperacoes(t *testing.T) {
	c := conectar(t, subirServidor(t))

	c.exigirOK(protocolo.TipoPing, vazio, nil)
	c.entrar("joao", "1234")

	for i := 0; i < 30; i++ {
		var publicada protocolo.PublicarCaronaResposta
		partida := futuro(24 + i)
		c.exigirOK(protocolo.TipoPublicarCarona,
			publicacao(2, []int{4500},
				parada("Feira de Santana", partida), parada("Jequié", partida.Add(3*time.Hour))), &publicada)

		var detalhe protocolo.DetalharCaronaResposta
		c.exigirOK(protocolo.TipoDetalharCarona,
			protocolo.DetalharCaronaRequisicao{CaronaID: publicada.CaronaID}, &detalhe)
		if detalhe.CaronaID != publicada.CaronaID {
			t.Fatalf("detalhe de outra carona: %q != %q", detalhe.CaronaID, publicada.CaronaID)
		}
	}

	var lista protocolo.ListarMinhasCaronasResposta
	c.exigirOK(protocolo.TipoListarMinhasCaronas, protocolo.ListarMinhasCaronasRequisicao{}, &lista)
	if len(lista.Caronas) != caronasDeJoaoNaCarga+30 {
		t.Fatalf("joao tem %d caronas, want %d (%d da carga + 30 publicadas)",
			len(lista.Caronas), caronasDeJoaoNaCarga+30, caronasDeJoaoNaCarga)
	}

	c.exigirOK(protocolo.TipoLogout, vazio, nil)
}

// TestRajadaDeMensagens escreve muitas requisições de uma vez, sem esperar
// resposta entre elas.
//
// É o teste que pega a falha do ReadSlice sem cópia descrita no PROJETO.md
// (seção 5.1): com várias linhas no mesmo buffer, a fatia devolvida por uma
// leitura é sobrescrita pela seguinte, e as mensagens se corrompem umas às
// outras. O sintoma seria justamente este — ids embaralhados sob rajada.
func TestRajadaDeMensagens(t *testing.T) {
	c := conectar(t, subirServidor(t))
	c.entrar("joao", "1234")

	const total = 200

	if err := c.conn.SetDeadline(time.Now().Add(prazoLeitura)); err != nil {
		t.Fatalf("prazo da conexão: %v", err)
	}

	var rajada []byte
	for i := 0; i < total; i++ {
		linha, err := json.Marshal(protocolo.Requisicao{
			ID:    fmt.Sprintf("rajada-%d", i),
			Tipo:  protocolo.TipoDetalharCarona,
			Dados: json.RawMessage(`{"carona_id":"car-1"}`),
		})
		if err != nil {
			t.Fatalf("montar envelope: %v", err)
		}
		rajada = append(rajada, linha...)
		rajada = append(rajada, '\n')
	}
	if _, err := c.conn.Write(rajada); err != nil {
		t.Fatalf("escrever rajada: %v", err)
	}

	// As respostas voltam na ordem das requisições (modelo estritamente
	// requisição/resposta, seção 1), e cada uma tem que trazer o seu id.
	for i := 0; i < total; i++ {
		bruta, err := c.leitor.LerLinha()
		if err != nil {
			t.Fatalf("resposta %d: %v", i, err)
		}
		resp, err := protocolo.DecodificarResposta(bruta)
		if err != nil {
			t.Fatalf("resposta %d: decodificar %q: %v", i, bruta, err)
		}
		querido := fmt.Sprintf("rajada-%d", i)
		if resp.ID != querido {
			t.Fatalf("resposta %d: id = %q, want %q", i, resp.ID, querido)
		}
		if resp.Status != protocolo.StatusOK {
			t.Fatalf("resposta %d: %q %q", i, resp.Codigo, resp.Mensagem)
		}
	}
}

// TestClienteDerrubadoNaoAfetaOsDemais confere a seção 4 e o critério da fase
// 4 do roteiro: conexão encerrada no meio de uma linha JSON derruba só a si
// mesma.
func TestClienteDerrubadoNaoAfetaOsDemais(t *testing.T) {
	endereco := subirServidor(t)

	saudavel := conectar(t, endereco)
	saudavel.entrar("joao", "1234")

	// Um cliente escreve meia mensagem e some. Sem o '\n' não existe mensagem
	// a processar, então nem resposta ele recebe.
	quebrado, err := net.Dial("tcp", endereco)
	if err != nil {
		t.Fatalf("conectar: %v", err)
	}
	if _, err := quebrado.Write([]byte(`{"id":"1","tipo":"PING","da`)); err != nil {
		t.Fatalf("escrever fragmento: %v", err)
	}
	if err := quebrado.Close(); err != nil {
		t.Fatalf("fechar conexão quebrada: %v", err)
	}

	// Outro manda lixo e tipos que não existem, e continua atendido.
	barulhento := conectar(t, endereco)
	for _, linha := range []string{`isso não é json`, `[1,2,3]`, `{"id":"x","tipo":"INVENTADO","dados":{}}`} {
		if resp := barulhento.enviarBruto(linha); resp.Status != protocolo.StatusErro {
			t.Fatalf("linha %q deveria ter sido recusada: %+v", linha, resp)
		}
	}
	barulhento.exigirOK(protocolo.TipoPing, vazio, nil)

	// A conexão saudável segue intacta, com a sessão preservada.
	var lista protocolo.ListarMinhasCaronasResposta
	saudavel.exigirOK(protocolo.TipoListarMinhasCaronas, protocolo.ListarMinhasCaronasRequisicao{}, &lista)
	if len(lista.Caronas) != caronasDeJoaoNaCarga {
		t.Fatalf("a conexão saudável perdeu estado: %d caronas, want %d", len(lista.Caronas), caronasDeJoaoNaCarga)
	}
}

// TestPublicacoesSimultaneas exercita o mutex global (D04) pelo socket: 20
// conexões publicando ao mesmo tempo.
//
// Rodado com -race, é o que prova que a camada de estado serializa de fato as
// escritas. As duas afirmações no fim cobrem os dois modos de falha: id
// repetido significa geração fora da seção crítica, e carona que o servidor
// confirmou mas não encontra significa escrita perdida.
//
// A escrita perdida é conferida detalhando cada id, e não contando a listagem:
// as cem caronas passam do limite de LISTAR_MINHAS_CARONAS (D16).
func TestPublicacoesSimultaneas(t *testing.T) {
	endereco := subirServidor(t)

	const conexoes, porConexao = 20, 5

	var espera sync.WaitGroup
	ids := make(chan string, conexoes*porConexao)

	for i := 0; i < conexoes; i++ {
		espera.Add(1)
		go func(n int) {
			defer espera.Done()

			c := conectar(t, endereco)
			c.entrar("joao", "1234")
			for j := 0; j < porConexao; j++ {
				var publicada protocolo.PublicarCaronaResposta
				partida := futuro(24 + n)
				c.exigirOK(protocolo.TipoPublicarCarona,
					publicacao(2, []int{3000},
						parada("Salvador", partida), parada("Feira de Santana", partida.Add(2*time.Hour))), &publicada)
				ids <- publicada.CaronaID
			}
		}(i)
	}
	espera.Wait()
	close(ids)

	vistos := map[string]bool{}
	for id := range ids {
		if vistos[id] {
			t.Fatalf("carona_id repetido entre conexões simultâneas: %q", id)
		}
		vistos[id] = true
	}
	if len(vistos) != conexoes*porConexao {
		t.Fatalf("%d caronas publicadas, want %d", len(vistos), conexoes*porConexao)
	}

	conferente := conectar(t, endereco)
	conferente.entrar("joao", "1234")
	for id := range vistos {
		resp := conferente.enviar(protocolo.TipoDetalharCarona, protocolo.DetalharCaronaRequisicao{CaronaID: id})
		if resp.Status != protocolo.StatusOK {
			t.Fatalf("carona %s confirmada na publicação, mas DETALHAR_CARONA respondeu %s: houve escrita perdida",
				id, resp.Codigo)
		}
	}
}
