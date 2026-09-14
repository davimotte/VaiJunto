package cliente

import "vaijunto/internal/protocolo"

// Um método por operação do PROTOCOL.md, seção 5. Cada um monta o "dados" da
// requisição com a struct de internal/protocolo e devolve a struct de
// resposta correspondente — o mesmo pacote que o servidor usa para
// desserializar, de modo que os dois lados não têm como divergir.
//
// Nenhum método recebe o usuário autenticado: depois do LOGIN a identidade
// mora na goroutine da conexão do lado do servidor (D08), e do lado do
// cliente ela mora na própria Conexao.

// vazio é o payload das operações sem campos: a seção 2.1 exige objeto, e um
// struct sem campos serializa exatamente como {}.
var vazio = struct{}{}

// Ping é o diagnóstico da seção 5.1. Não exige autenticação.
func (c *Conexao) Ping() (protocolo.PingResposta, error) {
	var resposta protocolo.PingResposta
	err := c.executar(protocolo.TipoPing, vazio, &resposta)
	return resposta, err
}

// Login autentica a conexão (seção 5.2).
func (c *Conexao) Login(usuario, senha string) (protocolo.LoginResposta, error) {
	var resposta protocolo.LoginResposta
	err := c.executar(protocolo.TipoLogin, protocolo.LoginRequisicao{Usuario: usuario, Senha: senha}, &resposta)
	return resposta, err
}

// Logout desautentica a conexão sem fechá-la (seção 5.3). É o que permite
// trocar de usuário — ou corrigir um login feito com o perfil errado — sem
// abrir um segundo socket.
func (c *Conexao) Logout() error {
	return c.executar(protocolo.TipoLogout, vazio, nil)
}

// PublicarCarona publica uma carona (seção 5.4). O cliente informa as paradas,
// com o horário de cada uma, os assentos e o preço de cada trecho; o servidor
// valida e devolve a rota como a guardou, sem calcular nada (D09).
func (c *Conexao) PublicarCarona(requisicao protocolo.PublicarCaronaRequisicao) (protocolo.PublicarCaronaResposta, error) {
	var resposta protocolo.PublicarCaronaResposta
	err := c.executar(protocolo.TipoPublicarCarona, requisicao, &resposta)
	return resposta, err
}

// ListarMinhasCaronas devolve as caronas do motorista autenticado (seção 5.5).
func (c *Conexao) ListarMinhasCaronas(incluirCanceladas bool) (protocolo.ListarMinhasCaronasResposta, error) {
	var resposta protocolo.ListarMinhasCaronasResposta
	err := c.executar(protocolo.TipoListarMinhasCaronas,
		protocolo.ListarMinhasCaronasRequisicao{IncluirCanceladas: incluirCanceladas}, &resposta)
	return resposta, err
}

// DetalharCarona devolve os passageiros confirmados por trecho (seção 5.6).
func (c *Conexao) DetalharCarona(caronaID string) (protocolo.DetalharCaronaResposta, error) {
	var resposta protocolo.DetalharCaronaResposta
	err := c.executar(protocolo.TipoDetalharCarona,
		protocolo.DetalharCaronaRequisicao{CaronaID: caronaID}, &resposta)
	return resposta, err
}

// CancelarCarona cancela a carona e, em cascata, as reservas que a usam
// (seção 5.7).
func (c *Conexao) CancelarCarona(caronaID string) (protocolo.CancelarCaronaResposta, error) {
	var resposta protocolo.CancelarCaronaResposta
	err := c.executar(protocolo.TipoCancelarCarona,
		protocolo.CancelarCaronaRequisicao{CaronaID: caronaID}, &resposta)
	return resposta, err
}

// BuscarItinerarios consulta itinerários diretos e com baldeação (seção 5.8).
//
// A resposta é informativa e não reserva nada (D07): entre esta chamada e a
// Reservar seguinte, outro passageiro pode levar o último assento. É
// exatamente por isso que o cliente não guarda nenhum estado de "reserva em
// andamento" — ele apenas devolve, na confirmação, os trechos que recebeu.
func (c *Conexao) BuscarItinerarios(origem, destino, data string) (protocolo.BuscarItinerariosResposta, error) {
	var resposta protocolo.BuscarItinerariosResposta
	err := c.executar(protocolo.TipoBuscarItinerarios,
		protocolo.BuscarItinerariosRequisicao{Origem: origem, Destino: destino, Data: data}, &resposta)
	return resposta, err
}

// Reservar confirma o itinerário escolhido (seção 5.9).
//
// Os itens vêm da própria resposta da busca, sem passar pelo teclado do
// usuário: é o cliente devolvendo ao servidor os campos carona_id, de e ate
// que ele mesmo recebeu (D15).
func (c *Conexao) Reservar(trechos []protocolo.ItemReserva) (protocolo.ReservarResposta, error) {
	var resposta protocolo.ReservarResposta
	err := c.executar(protocolo.TipoReservar, protocolo.ReservarRequisicao{Trechos: trechos}, &resposta)
	return resposta, err
}

// ListarMinhasReservas devolve as reservas do passageiro autenticado
// (seção 5.10).
func (c *Conexao) ListarMinhasReservas(incluirCanceladas bool) (protocolo.ListarMinhasReservasResposta, error) {
	var resposta protocolo.ListarMinhasReservasResposta
	err := c.executar(protocolo.TipoListarMinhasReservas,
		protocolo.ListarMinhasReservasRequisicao{IncluirCanceladas: incluirCanceladas}, &resposta)
	return resposta, err
}

// CancelarReserva cancela a reserva e devolve os assentos (seção 5.11).
func (c *Conexao) CancelarReserva(reservaID string) (protocolo.CancelarReservaResposta, error) {
	var resposta protocolo.CancelarReservaResposta
	err := c.executar(protocolo.TipoCancelarReserva,
		protocolo.CancelarReservaRequisicao{ReservaID: reservaID}, &resposta)
	return resposta, err
}
