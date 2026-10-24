package federacao

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/transacao"
)

// Recursos de transação da API: preparar, confirmar e abortar (seções 4.3 a 4.5
// do API_CONSORCIO.md). É a borda do participante do 2PC para o lado dos pares.
//
// Os handlers são finos de propósito. Eles fazem três coisas, e só elas:
//
//  1. conferem o **papel** de quem chama (seção 1.2), que depende do cabeçalho
//     e do caminho, e portanto não pode ser decidido no domínio;
//  2. traduzem JSON em tipos do domínio e de volta;
//  3. traduzem erro de domínio em voto NÃO ou em erro HTTP, pela tabela única
//     de erros.go (seção 5.5).
//
// Toda regra de significado — carona desta empresa, índices, assento,
// idempotência — vive no domínio, porque o coordenador local chama o
// participante direto, sem passar por aqui (seção 7.1, passo 5), e uma regra
// escrita só no handler não valeria para metade das chamadas.

// Tipos de fio. Existem separados dos do domínio porque as tags JSON são o
// contrato da API, e o domínio não conhece a rede (regra 8). São os mesmos
// motivos de CaronaFederada existir ao lado de dominio.CaronaBusca.
//
// Os campos obrigatórios são **ponteiros**, pelo mesmo motivo dos pedidos da
// borda TCP (internal/servidor/reservas.go): sem eles, "de" ausente e "de": 0
// chegariam indistinguíveis, e um pedido sem o campo viraria silenciosamente um
// pedido do trecho 0 em vez do 400 CORPO_INVALIDO que a seção 5.1 manda.
type pedidoPreparar struct {
	Coordenador   *string          `json:"coordenador"`
	Participantes *[]string        `json:"participantes"`
	Carimbo       *carimboDeFio    `json:"carimbo"`
	Passageiro    *passageiroDeFio `json:"passageiro"`
	Itens         *[]itemPreparar  `json:"itens"`
}

type carimboDeFio struct {
	Lamport *uint64 `json:"lamport"`
	Empresa *string `json:"empresa"`
}

type passageiroDeFio struct {
	Ref  *string `json:"ref"`
	Nome *string `json:"nome"`
}

type itemPreparar struct {
	CaronaID *string `json:"carona_id"`
	De       *int    `json:"de"`
	Ate      *int    `json:"ate"`
}

// pernaDeFio é a Perna da seção 3.3: os dados autoritativos que o dono afirma
// sobre a sua perna e o coordenador guarda na reserva (D34, D42).
type pernaDeFio struct {
	CaronaID      string    `json:"carona_id"`
	Empresa       string    `json:"empresa"`
	MotoristaNome string    `json:"motorista_nome"`
	De            int       `json:"de"`
	Ate           int       `json:"ate"`
	Cidades       []string  `json:"cidades"`
	Partida       time.Time `json:"partida"`
	Chegada       time.Time `json:"chegada"`
	PrecoCentavos int       `json:"preco_centavos"`
}

// respostaVoto é o corpo 200 do preparar, nas suas três formas (seção 4.3).
//
// Trecho é ponteiro porque o trecho 0 é um trecho legítimo, e omitempty o
// apagaria justamente no primeiro trecho de cada carona.
type respostaVoto struct {
	Voto     string       `json:"voto"`
	Pernas   []pernaDeFio `json:"pernas,omitempty"`
	Motivo   string       `json:"motivo,omitempty"`
	CaronaID string       `json:"carona_id,omitempty"`
	Trecho   *int         `json:"trecho,omitempty"`
}

// respostaSituacao é o corpo 200 de confirmar e abortar (seções 4.4 e 4.5).
type respostaSituacao struct {
	Situacao string `json:"situacao"`
}

// respostaConsulta é o corpo 200 do consultar, nas suas duas formas
// (seção 4.6): o coordenador manda papel e decisao, o participante manda papel
// e situacao.
//
// Os dois campos de conteúdo são omitempty porque cada resposta traz um só, e
// mandar `"decisao":""` num corpo de participante faria o outro lado precisar
// distinguir campo vazio de campo ausente para saber quem falou. O papel, que
// é quem diz qual dos dois ler, está sempre presente.
type respostaConsulta struct {
	Papel    string `json:"papel"`
	Decisao  string `json:"decisao,omitempty"`
	Situacao string `json:"situacao,omitempty"`
}

// tratarPreparar responde POST /transacoes/{tx}/preparar: a fase 1 do 2PC.
func tratarPreparar(w http.ResponseWriter, r *http.Request, p *transacao.Participante, injetor *transacao.Injetor) {
	txID, erro := txDoCoordenador(r)
	if erro != nil {
		responderErro(w, *erro)
		return
	}
	pedido, erro := pedidoDoCorpo(r, txID)
	if erro != nil {
		anotar(w, "tx=%s", txID)
		responderErro(w, *erro)
		return
	}

	voto, falha := votoDoParticipante(p.Preparar(pedido))
	carimbo := carimboDoRegistro(pedido.Carimbo)
	if falha != nil {
		anotar(w, "tx=%s %s erro=%s", txID, carimbo, falha.Codigo)
		responderErro(w, *falha)
		return
	}

	if voto.Motivo != "" {
		anotar(w, "tx=%s %s voto=%s motivo=%s", txID, carimbo, voto.Voto, voto.Motivo)
	} else {
		anotar(w, "tx=%s %s voto=%s", txID, carimbo, voto.Voto)
	}
	responderJSON(w, http.StatusOK, votoDeFio(voto))

	// D31: ponto part-apos-responder (TF6). É o único ponto que vive nesta
	// camada, e é por isso que ele está aqui e não em internal/transacao: a
	// tabela da seção 9.5 o define como "voto SIM enviado", e só a borda sabe
	// quando a resposta deixou de ser uma estrutura em memória. responderJSON
	// declara Content-Length e esvazia o buffer, então quando esta linha é
	// alcançada o voto está no fio.
	//
	// Só no SIM: um NÃO não compromete o participante com nada, e cair depois
	// dele seria o mesmo cenário do part-antes-de-gravar.
	if voto.Voto == transacao.VotoSim {
		injetor.Em(transacao.PontoPartAposResponder)
	}
}

// votoDoParticipante traduz o resultado do participante no voto que o
// coordenador consome, ou na falha HTTP correspondente.
//
// É o ponto onde a tabela de erros.go decide a natureza da resposta: status 200
// nela significa "isto é um voto NÃO", e não um erro — o participante processou
// o PREPARAR e respondeu com uma recusa (seção 2 da API). É o que impede o
// coordenador de confundir recusa legítima com falha de comunicação e repetir à
// toa.
//
// Está isolado do handler porque as **duas** implementações da rede o usam: o
// caminho HTTP, aqui, e a rede em memória dos testes. Se cada uma traduzisse por
// conta própria, a rede em memória deixaria de ser um substituto fiel
// exatamente na distinção que o coordenador mais precisa acertar.
func votoDoParticipante(pernas []dominio.Perna, err error) (transacao.RespostaPreparar, *erroHTTP) {
	if err == nil {
		return transacao.RespostaPreparar{Voto: transacao.VotoSim, Pernas: pernas}, nil
	}

	traducao := erroHTTPDeDominio(err)
	if traducao.Status != http.StatusOK {
		return transacao.RespostaPreparar{}, &traducao
	}

	// AGUARDE não é voto NÃO: é a terceira forma da seção 4.3, e vai sem
	// motivo. O wait-die o produz quando esta transação é mais velha que todos
	// os portadores (seção 8.2), e o participante não gravou nada — o
	// coordenador volta em 200 ms (D41).
	voto := transacao.RespostaPreparar{Voto: transacao.VotoNao, Motivo: traducao.Codigo}
	if errors.Is(err, dominio.ErrAguarde) {
		voto = transacao.RespostaPreparar{Voto: transacao.VotoAguarde}
	}

	// CaronaID e Trecho saem do erro tipado do domínio, com errors.As, e não de
	// uma segunda consulta ao estado: quem sabe qual trecho esgotou é quem olhou
	// o contador sob o lock, e reler depois daria outra resposta. A interface
	// cobre os três casos em que a seção 4.3 manda informá-los: SEM_ASSENTO,
	// PRECEDENCIA e AGUARDE.
	var disputado dominio.TrechoDisputado
	if errors.As(err, &disputado) {
		voto.CaronaID, voto.Trecho = disputado.TrechoEsgotado()
	}
	return voto, nil
}

// tratarConfirmar responde POST /transacoes/{tx}/confirmar.
func tratarConfirmar(w http.ResponseWriter, r *http.Request, p *transacao.Participante) {
	tratarDecisao(w, r, "CONFIRMAR", p.Confirmar)
}

// tratarAbortar responde POST /transacoes/{tx}/abortar.
func tratarAbortar(w http.ResponseWriter, r *http.Request, p *transacao.Participante) {
	tratarDecisao(w, r, "ABORTAR", p.Abortar)
}

// tratarDecisao atende os dois recursos da fase 2, que diferem apenas na
// operação que chamam: os dois recebem corpo vazio, conferem o mesmo papel e
// respondem a mesma situação. Escrevê-los duas vezes seria convidar a
// divergência num deles.
func tratarDecisao(w http.ResponseWriter, r *http.Request, operacao string, aplicar func(string) (dominio.Situacao, error)) {
	txID, erro := txDoCoordenador(r)
	if erro != nil {
		responderErro(w, *erro)
		return
	}

	situacao, err := aplicar(txID)
	if err != nil {
		traducao := erroHTTPDeDominio(err)
		anotar(w, "tx=%s %s erro=%s", txID, operacao, traducao.Codigo)
		responderErro(w, traducao)
		return
	}

	anotar(w, "tx=%s %s situacao=%s", txID, operacao, situacao)
	responderJSON(w, http.StatusOK, respostaSituacao{Situacao: string(situacao)})
}

// tratarConsultar responde POST /transacoes/{tx}/consultar: a base da
// terminação cooperativa (seção 4.6; D38).
//
// É POST, e não GET, porque consultar uma transação desconhecida **grava um
// veto**. Em REST, GET não pode ter efeito colateral, e um intermediário que
// repetisse um GET por conta própria estaria gravando vetos sem que ninguém
// pedisse.
//
// Diferente dos outros três recursos, não chama txDoCoordenador: consultar é
// aceito de qualquer empresa do consórcio (seção 1.2), porque quem é
// consultado sobre uma transação desconhecida não tem como saber quem participa
// dela. Quem barra uma empresa de fora continua sendo o middleware, com
// 403 EMPRESA_DESCONHECIDA — a abertura é para o consórcio, não para o mundo.
func tratarConsultar(w http.ResponseWriter, r *http.Request, p *transacao.Participante) {
	txID, erro := txDoCaminho(r)
	if erro != nil {
		responderErro(w, *erro)
		return
	}

	resposta, err := p.Consultar(txID)
	if err != nil {
		traducao := erroHTTPDeDominio(err)
		anotar(w, "tx=%s CONSULTAR erro=%s", txID, traducao.Codigo)
		responderErro(w, traducao)
		return
	}

	corpo := respostaConsulta{Papel: string(resposta.Papel)}
	if resposta.Papel == transacao.PapelCoordenador {
		corpo.Decisao = string(resposta.Decisao)
		anotar(w, "tx=%s CONSULTAR papel=%s decisao=%s", txID, corpo.Papel, corpo.Decisao)
	} else {
		corpo.Situacao = resposta.Situacao
		anotar(w, "tx=%s CONSULTAR papel=%s situacao=%s", txID, corpo.Papel, corpo.Situacao)
	}
	responderJSON(w, http.StatusOK, corpo)
}

// txDoCaminho extrai o txID do caminho e confere **só** a forma: o prefixo da
// empresa coordenadora (D27). Sem ele não há papel nenhum a resolver, e o
// caminho é malformado.
//
// É a metade de txDoCoordenador que vale para consultar, e existe separada
// justamente porque a outra metade — exigir que quem chama seja o coordenador
// — não vale lá (seção 1.2).
func txDoCaminho(r *http.Request) (string, *erroHTTP) {
	txID := r.PathValue("tx")
	if _, err := dominio.EmpresaDoID(txID); err != nil {
		return "", &erroHTTP{http.StatusBadRequest, codigoCorpoInvalido,
			"O identificador de transação precisa do prefixo da empresa coordenadora."}
	}
	return txID, nil
}

// txDoCoordenador extrai o txID do caminho e confere o papel de quem chama
// (seção 1.2 da API): preparar, confirmar e abortar só são aceitos do
// coordenador, que é a empresa do prefixo do txID (D27).
//
// A verificação é aqui, e não no domínio, porque depende do cabeçalho
// X-VaiJunto-Empresa e do caminho da URL — as duas coisas que o domínio não
// conhece. Ela não substitui autenticação (seção 15): impede que um bug ou um
// par mal configurado mexa em transações que não são dele.
//
// O prefixo é o que responde quem coordena, sem consultar ninguém. Sem
// prefixo não há papel a comparar, e o caminho é malformado.
func txDoCoordenador(r *http.Request) (string, *erroHTTP) {
	txID, erro := txDoCaminho(r)
	if erro != nil {
		return "", erro
	}
	coordenador, _ := dominio.EmpresaDoID(txID)
	if quemChama := r.Header.Get(cabecalhoEmpresa); quemChama != coordenador {
		return "", &erroHTTP{http.StatusForbidden, codigoPapelInvalido,
			"Só o coordenador da transação pode decidi-la."}
	}
	return txID, nil
}

// pedidoDoCorpo decodifica o corpo do preparar (seção 4.3).
//
// Confere **presença**, não significado: campo ausente é 400 CORPO_INVALIDO
// (seção 5.1), porque um campo que não veio não existe como conceito no
// domínio — o valor zero de Go o esconderia. Se o campo veio e não vale,
// quem recusa é o domínio, com o sentinela que a tabela traduz.
//
// A única exceção é o coordenador, conferido aqui contra o prefixo do caminho:
// é a coerência entre corpo e URL, e o domínio não vê a URL.
func pedidoDoCorpo(r *http.Request, txID string) (dominio.PedidoPreparacao, *erroHTTP) {
	invalido := func(detalhe string) *erroHTTP {
		return &erroHTTP{http.StatusBadRequest, codigoCorpoInvalido, detalhe}
	}

	// Campos desconhecidos são ignorados (seção 1 da API): um par com uma
	// versão mais nova pode mandar campo que ainda não conhecemos.
	var corpo pedidoPreparar
	if err := json.NewDecoder(r.Body).Decode(&corpo); err != nil {
		return dominio.PedidoPreparacao{}, invalido("Corpo não é um JSON válido de preparar.")
	}

	switch {
	case corpo.Coordenador == nil:
		return dominio.PedidoPreparacao{}, invalido("Informe o campo coordenador.")
	case corpo.Participantes == nil:
		return dominio.PedidoPreparacao{}, invalido("Informe o campo participantes.")
	case corpo.Carimbo == nil || corpo.Carimbo.Lamport == nil || corpo.Carimbo.Empresa == nil:
		return dominio.PedidoPreparacao{}, invalido("Informe o carimbo com lamport e empresa.")
	case corpo.Passageiro == nil || corpo.Passageiro.Ref == nil || corpo.Passageiro.Nome == nil:
		return dominio.PedidoPreparacao{}, invalido("Informe o passageiro com ref e nome.")
	case corpo.Itens == nil:
		return dominio.PedidoPreparacao{}, invalido("Informe o campo itens.")
	}

	// Seção 4.3: o coordenador do corpo é igual ao prefixo de {tx}. Divergir é
	// erro do coordenador, e nenhuma repetição o conserta.
	if empresaDoTx, _ := dominio.EmpresaDoID(txID); *corpo.Coordenador != empresaDoTx {
		return dominio.PedidoPreparacao{}, invalido(
			fmt.Sprintf("O coordenador %q não é o prefixo de %q.", *corpo.Coordenador, txID))
	}

	itens := make([]dominio.ItemReserva, 0, len(*corpo.Itens))
	for i, item := range *corpo.Itens {
		if item.CaronaID == nil || item.De == nil || item.Ate == nil {
			return dominio.PedidoPreparacao{}, invalido(
				fmt.Sprintf("O item %d precisa de carona_id, de e ate.", i))
		}
		itens = append(itens, dominio.ItemReserva{CaronaID: *item.CaronaID, De: *item.De, Ate: *item.Ate})
	}

	return dominio.PedidoPreparacao{
		TxID:          txID,
		Coordenador:   *corpo.Coordenador,
		Participantes: *corpo.Participantes,
		Carimbo:       dominio.Carimbo{Lamport: *corpo.Carimbo.Lamport, Empresa: *corpo.Carimbo.Empresa},
		Passageiro:    dominio.Passageiro{Ref: *corpo.Passageiro.Ref, Nome: *corpo.Passageiro.Nome},
		Itens:         itens,
	}, nil
}

// votoDeFio converte o voto para o corpo da resposta (seção 4.3).
//
// O trecho só vai quando há carona a indicar: carona_id e trecho acompanham
// juntos SEM_ASSENTO, PRECEDENCIA e AGUARDE, e nunca um sem o outro.
func votoDeFio(voto transacao.RespostaPreparar) respostaVoto {
	corpo := respostaVoto{
		Voto:     string(voto.Voto),
		Pernas:   pernasDeFio(voto.Pernas),
		Motivo:   voto.Motivo,
		CaronaID: voto.CaronaID,
	}
	if voto.CaronaID != "" {
		trecho := voto.Trecho
		corpo.Trecho = &trecho
	}
	return corpo
}

// pernasDeFio converte as pernas autoritativas para o formato da seção 3.3.
func pernasDeFio(pernas []dominio.Perna) []pernaDeFio {
	saida := make([]pernaDeFio, 0, len(pernas))
	for _, p := range pernas {
		saida = append(saida, pernaDeFio{
			CaronaID:      p.CaronaID,
			Empresa:       p.Empresa,
			MotoristaNome: p.MotoristaNome,
			De:            p.De,
			Ate:           p.Ate,
			Cidades:       p.Cidades,
			Partida:       p.Partida,
			Chegada:       p.Chegada,
			PrecoCentavos: p.PrecoCentavos,
		})
	}
	return saida
}

// pernasDoFio é o caminho inverso, usado por quem chama ao ler um voto SIM.
func pernasDoFio(pernas []pernaDeFio) []dominio.Perna {
	saida := make([]dominio.Perna, 0, len(pernas))
	for _, p := range pernas {
		saida = append(saida, dominio.Perna{
			CaronaID:      p.CaronaID,
			Empresa:       p.Empresa,
			MotoristaNome: p.MotoristaNome,
			De:            p.De,
			Ate:           p.Ate,
			Cidades:       p.Cidades,
			Partida:       p.Partida,
			Chegada:       p.Chegada,
			PrecoCentavos: p.PrecoCentavos,
		})
	}
	return saida
}

// pedidoDeFio converte um pedido do domínio para o corpo da requisição, no
// lado de quem chama.
func pedidoDeFio(p dominio.PedidoPreparacao) pedidoPreparar {
	itens := make([]itemPreparar, 0, len(p.Itens))
	for _, item := range p.Itens {
		carona, de, ate := item.CaronaID, item.De, item.Ate
		itens = append(itens, itemPreparar{CaronaID: &carona, De: &de, Ate: &ate})
	}
	coordenador, participantes := p.Coordenador, p.Participantes
	lamport, empresa := p.Carimbo.Lamport, p.Carimbo.Empresa
	ref, nome := p.Passageiro.Ref, p.Passageiro.Nome
	return pedidoPreparar{
		Coordenador:   &coordenador,
		Participantes: &participantes,
		Carimbo:       &carimboDeFio{Lamport: &lamport, Empresa: &empresa},
		Passageiro:    &passageiroDeFio{Ref: &ref, Nome: &nome},
		Itens:         &itens,
	}
}

// VotanteLocal liga o participante desta empresa ao coordenador dela, para o
// caso em que a empresa do passageiro também é dona de algum trecho (D21).
//
// A chamada é direta, sem rede: uma empresa não faz HTTP contra si mesma, e
// fazê-lo acrescentaria uma serialização, uma porta e um prazo de rede a uma
// chamada de função. O que ela **não** encurta é a tradução: o voto sai de
// votoDoParticipante, a mesma função que o handler HTTP e a rede em memória
// usam, para que os três caminhos tragam o mesmo motivo para o mesmo erro
// (seções 5.4 e 5.5).
//
// Mora aqui, e não em internal/transacao, porque é nesta camada que vive a
// tabela de tradução da borda. O coordenador a recebe pela interface
// transacao.Votante, que ele próprio declara.
type VotanteLocal struct {
	p *transacao.Participante
}

func NovoVotanteLocal(p *transacao.Participante) *VotanteLocal {
	return &VotanteLocal{p: p}
}

var _ transacao.Votante = (*VotanteLocal)(nil)

// Preparar avalia o PREPARAR no participante local e devolve o voto.
//
// Um erro aqui é o que seria um status diferente de 200 no caminho HTTP: falha
// de verdade, como o log não ter conseguido gravar. Recusa de negócio não é
// erro — volta como voto NÃO, com o motivo da seção 5.2.
func (v *VotanteLocal) Preparar(pedido dominio.PedidoPreparacao) (transacao.RespostaPreparar, error) {
	voto, falha := votoDoParticipante(v.p.Preparar(pedido))
	if falha != nil {
		return transacao.RespostaPreparar{}, erroDeBorda(*falha)
	}
	return voto, nil
}

func (v *VotanteLocal) Confirmar(txID string) (dominio.Situacao, error) {
	return v.p.Confirmar(txID)
}

func (v *VotanteLocal) Abortar(txID string) (dominio.Situacao, error) {
	return v.p.Abortar(txID)
}
