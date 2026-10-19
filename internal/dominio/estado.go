package dominio

import "time"

// Este arquivo é a camada 6 do PROJETO.md (seção 5.1) e o **único** lugar do
// programa que adquire e.mu (D04, D05).
//
// Cada método segue a mesma forma: trava, chama a regra correspondente de
// caronas.go, destrava com defer. Como as regras são não exportadas, esta é a
// única porta de entrada no estado, e a propriedade "toda leitura e toda
// escrita do domínio acontecem sob o mutex" vale por construção, sem depender
// de disciplina de quem escreve o código depois.
//
// Um segundo efeito importante: como nenhum método devolve ponteiro para
// dentro do estado, nada do que sai daqui pode ser lido fora da seção crítica
// enquanto outra goroutine escreve. Todos devolvem cópias.

// NovoEstado cria um Estado vazio. Serve aos testes; a execução real usa
// CarregarEstado, que popula usuários e caronas a partir de dados/ (D02).
func NovoEstado(empresa string) *Estado {
	return &Estado{
		empresa:  empresa,
		usuarios: make(map[string]*Usuario),
		caronas:  make(map[string]*Carona),
		reservas: make(map[string]*Reserva),
		log:      NovaLogMemoria(),
		lamport:  relogioParado,
	}
}

// relogioParado é o relógio dos testes e de qualquer Estado antes do boot
// injetar o de verdade: todo registro sai com lamport 0.
//
// Existe para que gravar nunca precise de um if contra nil. Um campo de função
// que pode ser nil obrigaria a checagem em todo ponto de gravação, e bastaria
// um esquecimento para o servidor morrer com panic dentro da seção crítica,
// com o lock tomado.
func relogioParado() uint64 { return 0 }

// UsarRelogio injeta a leitura do relógio de Lamport (D24) que preenche o
// campo lamport dos registros (seção 4.8).
//
// Recebe uma função em vez do relógio porque o relógio mora em
// internal/transacao e o domínio não importa pacote interno nenhum (regra 8).
// O servidor chama uma vez no boot, antes de qualquer gravação; o lock é só
// para a troca ser visível a qualquer goroutine (-race), como em UsarLog.
func (e *Estado) UsarRelogio(lamport func() uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lamport = lamport
}

// UsarLog troca o destino dos registros. O servidor chama uma vez no boot,
// depois do Reaplicar e antes de aceitar conexões; o lock é só para que a
// troca seja visível a qualquer goroutine (-race), não porque haja disputa.
func (e *Estado) UsarLog(l Log) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.log = l
}

// Reaplicar refaz o estado a partir dos registros do log, na ordem em que
// foram gravados (D37, seção 9.2). Não grava nada: o log já os contém.
//
// Devolve o maior carimbo de Lamport encontrado, com que o boot restaura o
// relógio (seção 4.8, último parágrafo). Vem no retorno, e não num acessor,
// porque quem conhece o conteúdo do log é o domínio e quem tem o relógio é a
// camada de transação: obrigar o chamador a receber o valor é o que impede um
// boot futuro de esquecer de restaurá-lo e deixar o relógio andando para trás.
func (e *Estado) Reaplicar(registros [][]byte) (uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return reaplicar(e, registros)
}

// Autenticar confere as credenciais de LOGIN e devolve o usuário
// correspondente (PROTOCOL.md, seção 5.2).
//
// Devolve uma cópia do Usuario justamente para que a goroutine da conexão
// possa guardá-la como identidade da sessão (D08) e consultá-la a cada
// requisição sem tocar no estado compartilhado de novo.
func (e *Estado) Autenticar(usuario, senha string) (Usuario, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return autenticar(e, usuario, senha)
}

// PublicarCarona registra uma nova carona do motorista, com as paradas e os
// horários que ele informou (PROTOCOL.md, seção 5.4; D09). Validação e escrita
// acontecem juntas, na mesma seção crítica.
func (e *Estado) PublicarCarona(motoristaID string, rota []string, horarios []time.Time, assentos int, precos []int, agora time.Time) (Carona, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return publicarCarona(e, motoristaID, rota, horarios, assentos, precos, agora)
}

// CaronasDoMotorista lista as caronas publicadas pelo motorista
// (PROTOCOL.md, seção 5.5).
func (e *Estado) CaronasDoMotorista(motoristaID string, incluirCanceladas bool) []Carona {
	e.mu.Lock()
	defer e.mu.Unlock()
	return caronasDoMotorista(e, motoristaID, incluirCanceladas)
}

// DetalharCarona devolve a carona e os passageiros confirmados por trecho
// (PROTOCOL.md, seção 5.6), desde que motoristaID seja o dono.
func (e *Estado) DetalharCarona(caronaID, motoristaID string) (Carona, [][]PassageiroConfirmado, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return detalharCarona(e, caronaID, motoristaID)
}

// BuscarItinerarios devolve os itinerários da origem ao destino cuja primeira
// perna parte na data pedida (PROTOCOL.md, seção 5.8).
//
// Trava como todas as demais operações, embora não escreva nada: Livres é
// lido para podar pernas sem assento, e ler contador que outra goroutine
// decrementa é corrida mesmo quando o valor lido não vai a lugar nenhum. O
// resultado é montado inteiro dentro da seção crítica e sai como valor, sem
// nenhum ponteiro para dentro do estado.
//
// data carrega o fuso em que a pergunta "que dia é 15/09?" deve ser
// respondida; quem chama é responsável por escolhê-lo (D11).
func (e *Estado) BuscarItinerarios(origem, destino string, data time.Time) ([]Itinerario, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return buscarItinerarios(e, origem, destino, data)
}

// CancelarCarona cancela a carona e propaga o cancelamento às reservas que a
// usam (PROTOCOL.md, seção 5.7). Devolve quantas reservas caíram na cascata.
//
// A carona e todas as reservas atingidas mudam sob o mesmo Lock. É isso que
// torna a cascata atômica do ponto de vista de qualquer outra conexão: uma
// reserva concorrente ou entra inteira antes do cancelamento, e é cancelada
// junto, ou encontra a carona já cancelada e é recusada. Não existe instante
// observável no meio.
func (e *Estado) CancelarCarona(caronaID, motoristaID string, agora time.Time) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return cancelarCarona(e, caronaID, motoristaID, agora)
}

// Reservar confirma um itinerário de forma atômica (PROTOCOL.md, seção 5.9).
//
// Este é o método que carrega o peso de RNF05 e RNF06. Toda a validação e toda
// a escrita do algoritmo da seção 7 acontecem entre este Lock e este Unlock —
// não existe uma segunda seção crítica, nem estado intermediário de "assento em
// espera" entre elas (D07). Cinquenta conexões disputando o mesmo assento se
// enfileiram aqui, e só a primeira encontra Livres maior que zero.
func (e *Estado) Reservar(passageiroID string, itens []ItemReserva, agora time.Time) (Reserva, Itinerario, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return reservar(e, passageiroID, itens, agora)
}

// ReservasDoPassageiro lista as reservas do passageiro com os itinerários já
// resolvidos (PROTOCOL.md, seção 5.10).
func (e *Estado) ReservasDoPassageiro(passageiroID string, incluirCanceladas bool) []ReservaDetalhada {
	e.mu.Lock()
	defer e.mu.Unlock()
	return reservasDoPassageiro(e, passageiroID, incluirCanceladas)
}

// CancelarReserva desfaz a reserva do passageiro e devolve os assentos
// (PROTOCOL.md, seção 5.11).
func (e *Estado) CancelarReserva(reservaID, passageiroID string, agora time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return cancelarReserva(e, reservaID, passageiroID, agora)
}
