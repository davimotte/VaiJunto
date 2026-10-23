package dominio

import (
	"sort"
	"time"
)

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
		empresa:       empresa,
		usuarios:      make(map[string]*Usuario),
		caronas:       make(map[string]*Carona),
		reservas:      make(map[string]*Reserva),
		participacoes: make(map[string]*Participacao),
		emAndamento:   make(map[string]bool),
		log:           NovaLogMemoria(),
		lamport:       relogioParado,
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

// CaronasNaJanela devolve as caronas desta empresa com alguma parada em
// [desde, ate), na forma que a busca consome (D40).
//
// É o passo 3 da busca federada (PROJETO_P2.md, seção 6) e, ao mesmo tempo, o
// corpo de GET /federacao/caronas: as duas pontas precisam exatamente do mesmo
// recorte, e escrevê-lo duas vezes seria convidar a divergência entre o que a
// empresa responde ao par e o que ela usa de si mesma — a equivalência federada
// quebraria só na metade que ninguém testa.
//
// Este é o **único** ponto da busca que trava, e ele não calcula nada: copia e
// devolve. Livres é lido aqui dentro porque ler contador que outra goroutine
// decrementa é corrida mesmo quando o valor lido é só indicativo (-race
// acusaria). A partir do retorno, tudo o que a busca faz é sobre cópias, e por
// isso as chamadas aos pares acontecem com o lock já solto (regra 2).
//
// A lista sai ordenada por ID. Nenhuma regra depende disso — a busca tem as
// suas duas ordenações —, mas é o que torna a resposta de /federacao/caronas
// byte a byte igual entre duas chamadas, e portanto comparável num teste e
// legível numa demonstração.
func (e *Estado) CaronasNaJanela(desde, ate time.Time) []CaronaBusca {
	e.mu.Lock()
	defer e.mu.Unlock()
	return caronasNaJanela(e, desde, ate)
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

// Empresa devolve o identificador desta empresa (D27).
//
// Não precisa do lock: o campo é atribuído na criação e nunca mais escrito.
// Existe porque o coordenador precisa dele para montar a referência opaca do
// passageiro ("<empresa>:<usuario>", D32) e o carimbo da transação (D24).
func (e *Estado) Empresa() string {
	return e.empresa
}

// MarcarReservaEmAndamento é o passo 2 do coordenador: no máximo uma reserva em
// andamento por passageiro (D35). Devolve ErrReservaEmAndamento se já houver.
//
// A seção crítica é curtíssima de propósito: ela **não** cobre a votação, que
// faz E/S de rede e não pode acontecer com o lock preso (regra 2). É a marca,
// e não o lock, que serializa as reservas do mesmo passageiro.
func (e *Estado) MarcarReservaEmAndamento(passageiroID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return marcarReservaEmAndamento(e, passageiroID)
}

// LiberarReservaEmAndamento retira a marca da D35. O coordenador a chama num
// defer, para que ela saia em qualquer saída do algoritmo.
func (e *Estado) LiberarReservaEmAndamento(passageiroID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	liberarReservaEmAndamento(e, passageiroID)
}

// ConfirmarReserva é o ponto de commit do coordenador (seção 7.1, passo 8):
// confere a sobreposição, grava DECISAO_CONFIRMAR com fsync e cria a reserva
// com a cópia das pernas (D42).
//
// Substitui o Reservar do P1, que fazia a reserva inteira — validação e escrita
// — numa seção crítica só (D07, revogada). No P2 isso não é possível: os
// assentos estão em outras empresas, e garantir atomicidade passou a ser
// trabalho do 2PC, não do mutex. O que **continua** valendo aqui é a regra 5:
// entre este Lock e este Unlock, valida-se tudo antes de escrever qualquer
// coisa, e nada parcial pode sobrar.
//
// O OK ao passageiro só sai depois que este método retorna, porque é o fsync
// que ele faz que torna a decisão durável (regra 3).
func (e *Estado) ConfirmarReserva(txID, passageiroID string, participantes []string, itens []ItemReserva, pernas []Perna, agora time.Time) (Reserva, Itinerario, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return confirmarReserva(e, txID, passageiroID, participantes, itens, pernas, agora)
}

// ConcluirReserva grava CONCLUIDA: a fase 2 terminou e todos os participantes
// confirmaram o recebimento (seção 7.1, passo 9).
func (e *Estado) ConcluirReserva(txID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return concluirReserva(e, txID)
}

// Reserva devolve uma cópia de uma reserva, se ela existe. Serve à auditoria e
// à retomada da fase 2; nenhuma decisão do protocolo a usa.
func (e *Estado) Reserva(txID string) (Reserva, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, existe := e.reservas[txID]
	if !existe {
		return Reserva{}, false
	}
	return r.copia(), true
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

// Preparar é a fase 1 do 2PC pelo lado do dono dos assentos (D21;
// PROJETO_P2.md, seção 7.2). Devolve os dados autoritativos das pernas, que
// acompanham o voto SIM (D34).
//
// Este método é o coração de RD02, e por um motivo só: validação, consulta aos
// contadores e desconto dos assentos acontecem entre este Lock e este Unlock.
// Cinquenta PREPARAR pelo último assento de um trecho, vindos de quantos
// coordenadores forem, enfileiram-se aqui, e só o primeiro encontra
// Livres >= 1. A garantia não depende da rede nem de relógio: depende de cada
// carona ter um único dono (seção 7.5).
//
// agora vem da borda, como em todas as operações do domínio, e arma o prazo de
// dúvida da participação (seção 4.4).
func (e *Estado) Preparar(p PedidoPreparacao, agora time.Time) ([]Perna, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return preparar(e, p, agora)
}

// Confirmar é a fase 2 com decisão de confirmar (seção 7.2; seção 4.4 da API).
// Devolve a situação em que a participação ficou.
func (e *Estado) Confirmar(txID string) (Situacao, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return confirmar(e, txID)
}

// Abortar é a fase 2 com decisão de abortar (seção 7.2; seção 4.5 da API).
// Libera os assentos em espera e, para transação desconhecida, grava o veto da
// D38.
func (e *Estado) Abortar(txID string) (Situacao, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return abortar(e, txID)
}

// Participacao devolve uma cópia da participação de uma transação, se ela
// existe. Serve à auditoria e ao teste; nenhuma decisão do protocolo a usa, e é
// por isso que ela pode ler e soltar o lock sem risco.
func (e *Estado) Participacao(txID string) (Participacao, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	pt, existe := e.participacoes[txID]
	if !existe {
		return Participacao{}, false
	}
	return pt.copia(), true
}

// Participacoes devolve cópias de todas as participações, ordenadas por txID.
//
// A ordem é explícita porque a iteração de mapa em Go é aleatória, e sem ela a
// mesma consulta devolveria a lista em ordem diferente a cada chamada — o que
// tornaria instável tanto o teste quanto a futura resposta de /auditoria.
func (e *Estado) Participacoes() []Participacao {
	e.mu.Lock()
	defer e.mu.Unlock()
	lista := make([]Participacao, 0, len(e.participacoes))
	for _, pt := range e.participacoes {
		lista = append(lista, pt.copia())
	}
	sort.Slice(lista, func(i, j int) bool { return lista[i].TxID < lista[j].TxID })
	return lista
}

// Ocupacao devolve a conta de I-P2-1 por carona e trecho (seção 11.1).
//
// Uma chamada só, sob um único Lock, porque a invariante é uma relação **entre**
// quatro números: lê-los em chamadas separadas deixaria uma transação
// concorrente entrar no meio e o teste acusaria uma violação que nunca
// existiu. É também a semente do GET /auditoria da fase 5 (seção 11.2).
func (e *Estado) Ocupacao() []OcupacaoTrecho {
	e.mu.Lock()
	defer e.mu.Unlock()
	return ocupacao(e)
}

// Auditoria devolve o estado completo da empresa para o verificador de
// invariantes (seção 11.2).
//
// Uma chamada só, sob um único Lock, pelo mesmo motivo de Ocupacao: as
// invariantes do P2 são relações entre caronas, participações e reservas, e
// lê-las em chamadas separadas deixaria uma transação concorrente entrar no
// meio e o verificador acusaria violação que nunca existiu.
func (e *Estado) Auditoria() Auditoria {
	e.mu.Lock()
	defer e.mu.Unlock()
	return auditoria(e)
}
