package servidor

import (
	"encoding/json"
	"fmt"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// Handlers das operações sobre caronas (PROTOCOL.md, seções 5.4 a 5.8).
//
// Todos seguem a mesma divisão de trabalho: aqui só se decodifica o payload,
// se chama um método do estado e se traduz o resultado para o envelope. A
// regra de negócio fica em internal/dominio, e a checagem de perfil já foi
// feita por sessao.autorizar antes de qualquer um deles ser chamado.

// publicarCaronaPedido decodifica o "dados" de PUBLICAR_CARONA com rigor
// suficiente para distinguir os códigos de erro da seção 5.4.
//
// Duas diferenças em relação a protocolo.PublicarCaronaRequisicao, que é a
// struct do contrato usada pelos clientes ao *enviar*:
//
//   - os campos são ponteiros, para separar "campo ausente" de "campo com
//     valor zero". Sem isso, {"assentos":0} e um pedido sem "assentos" seriam
//     indistinguíveis, e o cliente receberia a mensagem errada. Pelo mesmo
//     motivo, "paradas": [] (lista vazia, que é ROTA_INVALIDA) não se confunde
//     com um pedido sem "paradas" (CAMPO_INVALIDO);
//   - o horário de cada parada entra como string, e não como time.Time, para
//     que uma data malformada vire PARTIDA_INVALIDA em vez de derrubar a
//     decodificação inteira em CAMPO_INVALIDO.
//
// A regra que sai daí: campo ausente ou tipo JSON errado é CAMPO_INVALIDO;
// string que não é RFC 3339, em qualquer parada, é PARTIDA_INVALIDA.
type publicarCaronaPedido struct {
	Paradas        *[]paradaPedido `json:"paradas"`
	Assentos       *int            `json:"assentos"`
	PrecosCentavos *[]int          `json:"precos_centavos"`
}

// paradaPedido é um elemento de "paradas", com ponteiros pelo motivo acima.
// Uma parada null na lista decodifica com os dois campos nil e cai na mesma
// recusa de campo ausente.
type paradaPedido struct {
	Cidade  *string `json:"cidade"`
	Horario *string `json:"horario"`
}

// tratarPublicarCarona publica uma carona do motorista autenticado
// (PROTOCOL.md, seção 5.4).
func tratarPublicarCarona(req protocolo.Requisicao, estado *dominio.Estado, s *sessao) protocolo.Resposta {
	var pedido publicarCaronaPedido
	if err := json.Unmarshal(req.Dados, &pedido); err != nil {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "Algum campo veio com o tipo errado.")
	}
	if pedido.Paradas == nil || pedido.Assentos == nil || pedido.PrecosCentavos == nil {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "Informe paradas, assentos e precos_centavos.")
	}

	// Primeiro a forma de todas as paradas, depois o conteúdo dos horários:
	// assim um pedido com um campo faltando é sempre CAMPO_INVALIDO, qualquer
	// que seja a posição da parada incompleta.
	for _, p := range *pedido.Paradas {
		if p.Cidade == nil || p.Horario == nil {
			return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "Cada parada precisa de cidade e horario.")
		}
	}

	// A borda só separa as listas e interpreta o texto dos horários. Se as
	// paradas formam uma carona — quantas são, se repetem cidade, se os
	// horários crescem — é regra de domínio, e fica em validarCarona.
	rota := make([]string, len(*pedido.Paradas))
	horarios := make([]time.Time, len(*pedido.Paradas))
	for i, p := range *pedido.Paradas {
		horario, err := time.Parse(time.RFC3339, *p.Horario)
		if err != nil {
			return respostaErro(req.ID, protocolo.CodigoPartidaInvalida,
				fmt.Sprintf("O horário da parada %d precisa estar no formato RFC 3339, com fuso (ex.: 2026-09-15T08:00:00-03:00).", i+1))
		}
		rota[i] = *p.Cidade
		horarios[i] = horario
	}

	// O relógio é lido aqui, na borda, e passado ao domínio: as regras de
	// domínio não consultam o relógio por conta própria, o que as torna
	// testáveis sem depender da data em que o teste roda.
	carona, err := estado.PublicarCarona(
		s.usuario.Usuario,
		rota, horarios,
		*pedido.Assentos,
		*pedido.PrecosCentavos,
		time.Now(),
	)
	if err != nil {
		return respostaDeErroDeDominio(req.ID, err)
	}

	return respostaOK(req.ID, protocolo.PublicarCaronaResposta{
		CaronaID: carona.ID,
		Rota:     carona.Rota,
		Horarios: carona.Horarios,
	})
}

// tratarListarMinhasCaronas lista as caronas do motorista autenticado
// (PROTOCOL.md, seção 5.5).
//
// "incluir_canceladas" é opcional: ausente vale false, que é o caso comum de
// quem só quer ver o que ainda está de pé.
func tratarListarMinhasCaronas(req protocolo.Requisicao, estado *dominio.Estado, s *sessao) protocolo.Resposta {
	var pedido protocolo.ListarMinhasCaronasRequisicao
	if err := json.Unmarshal(req.Dados, &pedido); err != nil {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "O campo incluir_canceladas precisa ser booleano.")
	}

	caronas := estado.CaronasDoMotorista(s.usuario.Usuario, pedido.IncluirCanceladas)

	resumos := make([]protocolo.CaronaResumo, 0, len(caronas))
	for _, c := range caronas {
		trechos := make([]protocolo.TrechoResumo, len(c.Rota)-1)
		for t := range trechos {
			trechos[t] = protocolo.TrechoResumo{
				Indice:        t,
				Origem:        c.Rota[t],
				Destino:       c.Rota[t+1],
				PrecoCentavos: c.PrecoTrecho[t],
				Livres:        c.Livres[t],
			}
		}
		resumos = append(resumos, protocolo.CaronaResumo{
			CaronaID:  c.ID,
			Rota:      c.Rota,
			Horarios:  c.Horarios,
			Assentos:  c.Assentos,
			Cancelada: c.Cancelada,
			Trechos:   trechos,
		})
	}

	return respostaOK(req.ID, protocolo.ListarMinhasCaronasResposta{Caronas: resumos})
}

// tratarDetalharCarona devolve os passageiros confirmados em cada trecho de
// uma carona do motorista autenticado (PROTOCOL.md, seção 5.6; RF04).
//
// Quem verifica que a carona é do motorista é o domínio, e ele devolve
// ErrNaoEDono — traduzido em NAO_E_DONO. Note que carona inexistente e carona
// de outro motorista têm códigos distintos, como manda a seção 5.6.
func tratarDetalharCarona(req protocolo.Requisicao, estado *dominio.Estado, s *sessao) protocolo.Resposta {
	var pedido protocolo.DetalharCaronaRequisicao
	if err := json.Unmarshal(req.Dados, &pedido); err != nil {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "O campo carona_id precisa ser string.")
	}
	if pedido.CaronaID == "" {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "Informe carona_id.")
	}

	carona, passageiros, err := estado.DetalharCarona(pedido.CaronaID, s.usuario.Usuario)
	if err != nil {
		return respostaDeErroDeDominio(req.ID, err)
	}

	trechos := make([]protocolo.TrechoDetalhado, len(carona.Rota)-1)
	for t := range trechos {
		lista := make([]protocolo.PassageiroTrecho, 0, len(passageiros[t]))
		for _, p := range passageiros[t] {
			lista = append(lista, protocolo.PassageiroTrecho{
				ReservaID: p.ReservaID,
				Usuario:   p.Usuario,
				Nome:      p.Nome,
			})
		}
		trechos[t] = protocolo.TrechoDetalhado{
			Indice:      t,
			Origem:      carona.Rota[t],
			Destino:     carona.Rota[t+1],
			Livres:      carona.Livres[t],
			Passageiros: lista,
		}
	}

	return respostaOK(req.ID, protocolo.DetalharCaronaResposta{
		CaronaID: carona.ID,
		Trechos:  trechos,
	})
}

// tratarCancelarCarona cancela a carona do motorista autenticado e propaga o
// cancelamento às reservas que dependem dela (PROTOCOL.md, seção 5.7; RF05).
//
// A resposta traz reservas_canceladas porque o motorista precisa saber quantas
// pessoas ele acabou de deixar sem viagem — o protocolo é estritamente
// requisição/resposta e o servidor nunca notifica o passageiro por conta
// própria (seção 1), então esse número é a única medida imediata do estrago.
//
// O prazo do motorista vai até o instante da partida, e não até uma hora antes:
// é um prazo diferente do prazo do passageiro (D13), e o domínio é quem o
// aplica. Aqui só se lê o relógio na borda e se passa adiante.
func tratarCancelarCarona(req protocolo.Requisicao, estado *dominio.Estado, s *sessao) protocolo.Resposta {
	var pedido protocolo.CancelarCaronaRequisicao
	if err := json.Unmarshal(req.Dados, &pedido); err != nil {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "O campo carona_id precisa ser string.")
	}
	if pedido.CaronaID == "" {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "Informe carona_id.")
	}

	canceladas, err := estado.CancelarCarona(pedido.CaronaID, s.usuario.Usuario, time.Now())
	if err != nil {
		return respostaDeErroDeDominio(req.ID, err)
	}

	return respostaOK(req.ID, protocolo.CancelarCaronaResposta{
		CaronaID:           pedido.CaronaID,
		ReservasCanceladas: canceladas,
	})
}

// buscarItinerariosPedido decodifica o "dados" de BUSCAR_ITINERARIOS com
// campos ponteiro pelo mesmo motivo de publicarCaronaPedido: distinguir campo
// ausente de string vazia, para que a mensagem de erro diga o que faltou.
type buscarItinerariosPedido struct {
	Origem  *string `json:"origem"`
	Destino *string `json:"destino"`
	Data    *string `json:"data"`
}

// tratarBuscarItinerarios responde a BUSCAR_ITINERARIOS (PROTOCOL.md, seção
// 5.8).
//
// Nada aqui reserva ou bloqueia assento: os valores refletem o instante da
// consulta e podem estar desatualizados quando o passageiro confirmar (D07). É
// o que garante que nenhum assento fique preso a uma reserva nunca concluída.
func tratarBuscarItinerarios(req protocolo.Requisicao, estado *dominio.Estado, s *sessao) protocolo.Resposta {
	var pedido buscarItinerariosPedido
	if err := json.Unmarshal(req.Dados, &pedido); err != nil {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "Algum campo veio com o tipo errado.")
	}
	if pedido.Origem == nil || pedido.Destino == nil || pedido.Data == nil {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "Informe origem, destino e data.")
	}

	// "2026-09-15" não designa um instante: designa um dia, e um dia só existe
	// dentro de um fuso. O fuso é resolvido aqui, na borda, e entregue pronto
	// ao domínio — que assim não precisa consultar relógio nem configuração
	// de ambiente. time.Local vem de TZ, e o binário embute tzdata para que
	// isso funcione também no contêiner Alpine (PROJETO.md, seção 10.1).
	data, err := time.ParseInLocation("2006-01-02", *pedido.Data, time.Local)
	if err != nil {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "A data precisa estar no formato AAAA-MM-DD (ex.: 2026-09-15).")
	}

	itinerarios, err := estado.BuscarItinerarios(*pedido.Origem, *pedido.Destino, data)
	if err != nil {
		return respostaDeErroDeDominio(req.ID, err)
	}

	resposta := protocolo.BuscarItinerariosResposta{
		Itinerarios: make([]protocolo.Itinerario, 0, len(itinerarios)),
	}
	for _, it := range itinerarios {
		trechos := make([]protocolo.TrechoItinerario, 0, len(it.Pernas))
		for _, p := range it.Pernas {
			trechos = append(trechos, protocolo.TrechoItinerario{
				CaronaID:      p.CaronaID,
				Motorista:     p.Motorista,
				De:            p.De,
				Ate:           p.Ate,
				Origem:        p.Origem,
				Destino:       p.Destino,
				Partida:       p.Partida,
				Chegada:       p.Chegada,
				PrecoCentavos: p.PrecoCentavos,
			})
		}
		resposta.Itinerarios = append(resposta.Itinerarios, protocolo.Itinerario{
			PrecoTotalCentavos: it.PrecoTotalCentavos,
			Partida:            it.Partida,
			Chegada:            it.Chegada,
			// Derivado na borda, e não guardado no domínio: uma baldeação é
			// uma troca de veículo, e trocas são uma a menos que as pernas.
			Baldeacoes: len(it.Pernas) - 1,
			Trechos:    trechos,
		})
	}
	return respostaOK(req.ID, resposta)
}
