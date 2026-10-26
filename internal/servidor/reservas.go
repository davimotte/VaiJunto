package servidor

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// Handlers das operações de passageiro (PROTOCOL.md, seções 5.9 a 5.11).
//
// Mesma divisão de trabalho dos handlers de carona: aqui só se decodifica o
// payload, se chama um método do estado e se traduz o resultado para o
// envelope. Nenhuma regra de reserva mora neste arquivo — a atomicidade é
// propriedade da seção crítica de internal/dominio, e não de nada que aconteça
// nesta camada.

// reservarPedido decodifica o "dados" de RESERVAR com campos ponteiro pelo
// mesmo motivo dos demais: separar "campo ausente" de "campo com valor zero".
//
// Aqui isso importa mais que nas outras operações, porque 0 é um valor
// perfeitamente legítimo para "de": {"de":0,"ate":1} é o primeiro trecho da
// rota, e um pedido que esqueceu "de" não pode ser tratado como se tivesse
// pedido esse trecho.
type reservarPedido struct {
	Trechos *[]itemReservaPedido `json:"trechos"`
}

type itemReservaPedido struct {
	CaronaID *string `json:"carona_id"`
	De       *int    `json:"de"`
	Ate      *int    `json:"ate"`
}

// tratarReservar confirma um itinerário de forma atômica (PROTOCOL.md, seção
// 5.9), delegando ao coordenador do 2PC (D21; PROJETO_P2.md, seção 7.1).
//
// O cliente devolve os mesmos campos carona_id, de e ate que recebeu da busca,
// sem que nada disso apareça na tela do passageiro (D15). Nada é conferido
// contra o resultado daquela busca, e é assim de propósito: a busca não
// reservou nem prometeu nada (D26), então o que conta é o que cada **dono**
// encontrar sob o seu lock, agora, ao avaliar o PREPARAR. Uma opção que existia
// na consulta e sumiu no meio do caminho vira SEM_ASSENTO, que é a resposta
// honesta.
//
// O que mudou do P1: a reserva local da D07 não existe mais. As caronas podem
// ser de três empresas, e nenhum mutex abrange as três — a atomicidade passa a
// vir do two-phase commit, e esta função só traduz pedido e resposta.
func tratarReservar(req protocolo.Requisicao, n *nucleo, s *sessao) protocolo.Resposta {
	var pedido reservarPedido
	if err := json.Unmarshal(req.Dados, &pedido); err != nil {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "Algum campo veio com o tipo errado.")
	}
	if pedido.Trechos == nil || len(*pedido.Trechos) == 0 {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "Informe ao menos um trecho em trechos.")
	}

	itens := make([]dominio.ItemReserva, 0, len(*pedido.Trechos))
	for i, bruto := range *pedido.Trechos {
		if bruto.CaronaID == nil || bruto.De == nil || bruto.Ate == nil {
			return respostaErro(req.ID, protocolo.CodigoCampoInvalido,
				"O trecho "+strconv.Itoa(i)+" precisa de carona_id, de e ate.")
		}
		if *bruto.CaronaID == "" {
			return respostaErro(req.ID, protocolo.CodigoCampoInvalido,
				"O carona_id do trecho "+strconv.Itoa(i)+" está vazio.")
		}
		itens = append(itens, dominio.ItemReserva{
			CaronaID: *bruto.CaronaID,
			De:       *bruto.De,
			Ate:      *bruto.Ate,
		})
	}

	// O passageiro atravessa a transação como referência opaca mais nome de
	// exibição, e nada além disso (D32, I-P2-4). A referência leva o prefixo da
	// empresa porque "maria" só é única **dentro** de uma empresa: vaijunto:maria
	// e rotacapixaba:maria são pessoas diferentes (seção 12.1).
	//
	// O login cru também segue, separado, porque é a chave das reservas e da
	// marca da D35 aqui dentro: para a própria empresa, o passageiro é o
	// usuário local, e não a referência global.
	passageiro := dominio.Passageiro{
		Ref:  n.estado.Empresa() + ":" + s.usuario.Usuario,
		Nome: s.usuario.Nome,
	}

	// O prazo de votação é do coordenador (D41), não da conexão: um ctx sem
	// prazo aqui deixa a camada 5 decidir, que é quem conhece os prazos.
	reserva, itinerario, err := n.coordenador.Reservar(context.Background(), passageiro, s.usuario.Usuario, itens)
	if err != nil {
		return respostaDeErroDeDominio(req.ID, err)
	}

	return respostaOK(req.ID, protocolo.ReservarResposta{
		ReservaID:          reserva.ID,
		PrecoTotalCentavos: itinerario.PrecoTotalCentavos,
	})
}

// tratarListarMinhasReservas devolve as reservas do passageiro autenticado
// (PROTOCOL.md, seção 5.10; RF09).
//
// "incluir_canceladas" é opcional e vale false quando ausente, como em
// LISTAR_MINHAS_CARONAS: o caso comum é querer ver só as viagens que ainda
// valem. Passá-lo como true é justamente como o passageiro descobre que uma
// reserva dele caiu na cascata de um cancelamento de carona — o servidor nunca
// envia mensagem não solicitada (PROTOCOL.md, seção 1).
func tratarListarMinhasReservas(req protocolo.Requisicao, estado *dominio.Estado, s *sessao) protocolo.Resposta {
	var pedido protocolo.ListarMinhasReservasRequisicao
	if err := json.Unmarshal(req.Dados, &pedido); err != nil {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "O campo incluir_canceladas precisa ser booleano.")
	}

	reservas := estado.ReservasDoPassageiro(s.usuario.Usuario, pedido.IncluirCanceladas)

	resumos := make([]protocolo.ReservaResumo, 0, len(reservas))
	for _, r := range reservas {
		trechos := make([]protocolo.TrechoReserva, 0, len(r.Itinerario.Pernas))
		for _, p := range r.Itinerario.Pernas {
			trechos = append(trechos, protocolo.TrechoReserva{
				CaronaID:      p.CaronaID,
				Empresa:       p.Empresa,
				Motorista:     p.Motorista,
				Origem:        p.Origem,
				Destino:       p.Destino,
				Partida:       p.Partida,
				Chegada:       p.Chegada,
				PrecoCentavos: p.PrecoCentavos,
			})
		}
		resumos = append(resumos, protocolo.ReservaResumo{
			ReservaID:          r.ID,
			Ativa:              r.Ativa,
			CriadaEm:           r.CriadaEm,
			PrecoTotalCentavos: r.Itinerario.PrecoTotalCentavos,
			Partida:            r.Itinerario.Partida,
			Chegada:            r.Itinerario.Chegada,
			Trechos:            trechos,
		})
	}

	return respostaOK(req.ID, protocolo.ListarMinhasReservasResposta{Reservas: resumos})
}

// tratarCancelarReserva desfaz a reserva do passageiro autenticado
// (PROTOCOL.md, seção 5.11; PROJETO_P2.md, seção 10.1; D39; RF10).
//
// Quem confere que a reserva é dele é o domínio, e não este handler: "esta
// reserva é da maria" é propriedade do estado. O que a camada de sessão decide
// é apenas que perfil pode chamar a operação.
//
// Os avisos recém-criados (D39) são entregues pelo outbox depois da resposta
// ao passageiro, com o lock do estado já solto (regra 2): ele não espera os
// participantes remotos confirmarem para saber que a reserva caiu.
func tratarCancelarReserva(req protocolo.Requisicao, n *nucleo, s *sessao) protocolo.Resposta {
	var pedido protocolo.CancelarReservaRequisicao
	if err := json.Unmarshal(req.Dados, &pedido); err != nil {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "O campo reserva_id precisa ser string.")
	}
	if pedido.ReservaID == "" {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "Informe reserva_id.")
	}

	avisos, err := n.estado.CancelarReserva(pedido.ReservaID, s.usuario.Usuario, time.Now().In(dominio.FusoDasCidades()))
	if err != nil {
		return respostaDeErroDeDominio(req.ID, err)
	}
	if n.entregador != nil {
		n.entregador.Enfileirar(avisos)
	}

	return respostaOK(req.ID, protocolo.CancelarReservaResposta{ReservaID: pedido.ReservaID})
}
