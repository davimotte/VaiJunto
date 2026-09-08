package servidor

import (
	"encoding/json"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// Handlers das operações de motorista (PROTOCOL.md, seções 5.4 a 5.6).
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
//     indistinguíveis, e o cliente receberia a mensagem errada;
//   - "partida" entra como string, e não como time.Time, para que uma data
//     malformada vire PARTIDA_INVALIDA em vez de derrubar a decodificação
//     inteira em CAMPO_INVALIDO.
//
// A regra que sai daí: tipo JSON errado é CAMPO_INVALIDO; string que não é
// RFC 3339 é PARTIDA_INVALIDA.
type publicarCaronaPedido struct {
	Origem         *string `json:"origem"`
	Destino        *string `json:"destino"`
	Partida        *string `json:"partida"`
	Assentos       *int    `json:"assentos"`
	PrecosCentavos *[]int  `json:"precos_centavos"`
}

// tratarPublicarCarona publica uma carona do motorista autenticado
// (PROTOCOL.md, seção 5.4).
func tratarPublicarCarona(req protocolo.Requisicao, estado *dominio.Estado, s *sessao) protocolo.Resposta {
	var pedido publicarCaronaPedido
	if err := json.Unmarshal(req.Dados, &pedido); err != nil {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "Algum campo veio com o tipo errado.")
	}
	if pedido.Origem == nil || pedido.Destino == nil || pedido.Partida == nil ||
		pedido.Assentos == nil || pedido.PrecosCentavos == nil {
		return respostaErro(req.ID, protocolo.CodigoCampoInvalido, "Informe origem, destino, partida, assentos e precos_centavos.")
	}

	partida, err := time.Parse(time.RFC3339, *pedido.Partida)
	if err != nil {
		return respostaErro(req.ID, protocolo.CodigoPartidaInvalida, "A partida precisa estar no formato RFC 3339, com fuso (ex.: 2026-09-15T08:00:00-03:00).")
	}

	// O relógio é lido aqui, na borda, e passado ao domínio: as regras de
	// domínio não consultam o relógio por conta própria, o que as torna
	// testáveis sem depender da data em que o teste roda.
	carona, err := estado.PublicarCarona(
		s.usuario.Usuario,
		*pedido.Origem, *pedido.Destino,
		partida,
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
