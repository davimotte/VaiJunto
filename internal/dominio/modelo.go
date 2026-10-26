// Package dominio implementa o estado e as regras de negócio do VAIJUNTO:
// cidades atendidas, caronas, reservas e o mutex único que protege tudo
// (PROJETO.md, seções 3 e 4). Não importa internal/protocolo nem
// internal/servidor — o domínio não conhece a rede.
package dominio

import (
	"sync"
	"time"
)

// Usuario é um perfil carregado de dados/usuarios.json no boot (D12). Não há
// operação de cadastro nem alteração: os campos são imutáveis após o boot.
type Usuario struct {
	Usuario string
	Senha   string
	Nome    string
	Perfil  string // "MOTORISTA" ou "PASSAGEIRO"
}

// Carona é uma oferta de um motorista, com as paradas e os horários que ele
// informou (D09).
//
// Rota e Horarios têm o mesmo comprimento; PrecoTrecho e Livres têm
// len(Rota)-1, um valor por trecho entre cidades consecutivas da rota.
// Livres começa igual a Assentos em cada posição e nunca é tocado pela busca.
// A D07 foi revogada pela D21: desde o P2, Livres[t] desconta também os
// assentos **em espera**, descontados no PREPARAR e devolvidos só por abort ou
// cancelamento (D25, seção 4.3). A invariante mestra é I-P2-1:
//
//	Livres[t] + confirmados em t + em espera em t == Assentos
type Carona struct {
	ID          string
	MotoristaID string
	Rota        []string    // ex.: ["Salvador", "Feira de Santana", "Jequié"]
	Horarios    []time.Time // len == len(Rota); informados pelo motorista, estritamente crescentes (D09, I6)
	Assentos    int         // capacidade total do veículo
	PrecoTrecho []int       // centavos; len == len(Rota)-1
	Livres      []int       // len == len(Rota)-1; inicia com Assentos; desconta confirmados e em espera (D25)
	Cancelada   bool
}

// ItemReserva referencia um trecho contíguo de uma Carona. De e Ate são
// índices de cidade na Rota da carona; a reserva consome os trechos
// [De, Ate).
type ItemReserva struct {
	CaronaID string
	De, Ate  int
}

// Reserva é um itinerário confirmado por um passageiro, guardada pelo
// **coordenador**, isto é, pela empresa do passageiro (seção 4.5). É a reserva
// que o passageiro lista e cancela.
//
// O ID **é** o TxID da tentativa que venceu (D36): cada tentativa de reserva é
// uma transação com identificador próprio, e a que confirma dá o seu nome à
// reserva. Guardar os dois lado a lado seria guardar o mesmo dado duas vezes.
//
// Pernas é a cópia dos dados que os participantes devolveram no voto SIM (D42).
// No P1 a reserva guardava só os índices e os dados legíveis eram resolvidos
// contra as caronas a cada listagem; no P2 as caronas das pernas remotas estão
// em outros servidores, e listar as reservas não pode depender de todos eles
// estarem no ar (RD05). A cópia não diverge porque rota, horários e preços são
// imutáveis após a publicação (D09).
type Reserva struct {
	ID            string
	PassageiroID  string
	Itens         []ItemReserva
	Pernas        []Perna
	Participantes []string
	Ativa         bool
	CriadaEm      time.Time
	// Concluida é verdadeiro quando todos os participantes confirmaram o
	// recebimento do CONFIRMAR, isto é, quando a fase 2 terminou (seção 7.1,
	// passo 9).
	Concluida bool
}

// TipoAviso é o tipo de uma entrada do outbox (D39; seção 4.7 da API).
type TipoAviso string

const (
	// AvisoCancelarPernas vai do coordenador a um participante: cancele a
	// perna local desta transação e devolva o assento (seção 10.1).
	AvisoCancelarPernas TipoAviso = "CANCELAR_PERNAS"
	// AvisoCaronaCancelada vai do dono de uma carona ao coordenador de uma
	// reserva atingida: a carona caiu, cancele o itinerário inteiro
	// (seção 10.2, D29).
	AvisoCaronaCancelada TipoAviso = "CARONA_CANCELADA"
)

// Aviso é uma entrada do outbox: um fato que esta empresa já decidiu e que
// precisa chegar a outra, de forma confiável, sem que ninguém possa recusá-lo
// (D39; seção 4.7 da API).
//
// Diferente do PREPARAR, nenhuma resposta aqui é voto: a entrega repete até
// confirmar, e quem recebe só aplica (seção 10.3).
type Aviso struct {
	ID       string
	Destino  string
	Tipo     TipoAviso
	TxID     string
	CaronaID string // só usado quando Tipo == AvisoCaronaCancelada
}

// copia devolve uma cópia do aviso. Os campos são todos valor, mas a função
// existe pelo mesmo motivo das outras: nenhum ponteiro para dentro do estado
// escapa da seção crítica, e um Aviso sempre entra e sai do outbox por valor.
func (a *Aviso) copia() Aviso { return *a }

// Estado é todo o estado de domínio do servidor, protegido por um único
// sync.Mutex (D04). Funções de domínio recebem o Estado já travado por quem
// chama e nunca adquirem mu por conta própria (D05): só a camada de estado
// adquire o lock, o que elimina por construção o risco de reentrância no
// mesmo mutex por caminhos diferentes.
type Estado struct {
	// empresa é o prefixo dos IDs gerados aqui (D27). Fica fora do mutex de
	// propósito: é atribuída na criação e nunca mais escrita, então qualquer
	// goroutine pode lê-la sem corrida.
	empresa  string
	mu       sync.Mutex
	usuarios map[string]*Usuario
	caronas  map[string]*Carona
	reservas map[string]*Reserva
	// participacoes é a visão desta empresa das transações que usam assentos
	// dela, por txID (D21, seção 4.4). Guarda só o que o participante precisa
	// saber; de outra empresa, nada além da referência opaca do passageiro e do
	// nome de exibição (D32, I-P2-4).
	//
	// Quem está em espera em cada trecho **não** é guardado aqui em paralelo:
	// é derivado destas participações pela função portadores (seção 4.3).
	participacoes map[string]*Participacao
	// emAndamento marca os passageiros com uma reserva em andamento (D35). O
	// servidor de origem recusa um segundo RESERVAR do mesmo passageiro
	// enquanto o primeiro não termina: entre a votação e a decisão passa-se
	// tempo de rede, e duas reservas simultâneas do mesmo passageiro poderiam
	// ambas passar na checagem de sobreposição.
	//
	// **Nunca vai ao log**, de propósito: o coordenador não grava nada antes de
	// decidir (D23), então a marca não sobrevive a um reboot — é exatamente o
	// que a seção 9.3 prevê ("Passageiro marcado pela D35: não existe, estava
	// só na memória").
	//
	// Fica aqui, e não na camada 5, para que continue valendo que existe **um**
	// mutex por servidor protegendo todo o estado da empresa (D04, regra 1). Os
	// votos e os prazos de uma transação em andamento, que são contabilidade da
	// coordenação e não estado da empresa, ficam em variáveis locais da
	// goroutine que atende o RESERVAR (seção 4.6).
	emAndamento map[string]bool
	// transacoesEmVotacao são os txID que o coordenador desta empresa está
	// votando agora, entre o primeiro PREPARAR e a decisão (seção 4.6). É o
	// que a consulta da seção 4.6 da API responde como EM_ANDAMENTO.
	//
	// **Nunca vai ao log**, pelo mesmo motivo do emAndamento acima: o
	// coordenador não grava nada antes de decidir (D23). Após um reboot o mapa
	// volta vazio, e a seção 9.3 diz exatamente isso — "Transação em andamento:
	// não existe, estava só na memória. Abort presumido".
	//
	// Mora aqui, e não num mapa do próprio Coordenador, por uma razão concreta:
	// a consulta precisa olhar as reservas e as transações em votação no
	// **mesmo** retrato. Em dois mapas com dois locks, uma transação que acabou
	// de confirmar poderia ser lida depois de sair deste mapa e antes de
	// aparecer nas reservas, e a resposta seria ABORTADA para uma transação
	// confirmada — o pior erro possível no 2PC. Sob um lock só, esse instante
	// não existe.
	transacoesEmVotacao map[string]bool
	// outbox é o lado de dentro dos avisos pendentes da D39 (seção 4.7),
	// indexado pelo ID do próprio aviso. Ao contrário de emAndamento e
	// transacoesEmVotacao, **vai ao log** (AVISO_REGISTRADO/AVISO_ENTREGUE):
	// um aviso só sai daqui quando a entrega já foi confirmada, e até lá
	// precisa sobreviver a um reboot (seção 9.3: "aviso no outbox → retoma o
	// envio").
	outbox map[string]*Aviso
	// log recebe cada mudança ANTES de ela ser aplicada (D22, regra 3). Nunca
	// é nil: os construtores começam com LogMemoria e o servidor troca por
	// arquivo com UsarLog. seq numera os registros (seção 4.8).
	log Log
	seq uint64
	// lamport lê o relógio de Lamport da empresa para o campo lamport dos
	// registros (seção 4.8). É uma função, e não o relógio, porque quem o
	// implementa é internal/transacao e o domínio não importa pacote interno
	// nenhum (regra 8, seção 5.3). Nunca é nil: os construtores começam com
	// uma função que devolve 0 e o servidor injeta a de verdade com
	// UsarRelogio.
	lamport func() uint64
}

// CaronaBusca é uma carona como a busca federada a enxerga, venha ela do
// estado local ou de um par (PROJETO_P2.md, seção 6, passos 3 e 5).
//
// É o par de domínio da "Carona federada" da seção 3.4 do API_CONSORCIO.md, e
// não leva tags JSON: quem serializa é internal/federacao, porque o domínio
// não conhece a rede (regra 8). Difere de Carona em três pontos, e cada um tem
// motivo:
//
//   - MotoristaNome já vem resolvido, e não é um MotoristaID: quem sabe o nome
//     de um motorista é a empresa dele, e o mapa de usuários de outra empresa
//     não existe aqui (D32).
//   - Empresa é explícita, e não derivada do prefixo do ID. É o que a resposta
//     do par informa, e confrontá-la com o prefixo é uma das validações do
//     passo 5.
//   - Não tem Cancelada: carona cancelada não entra na busca, e quem a filtra é
//     o dono, antes de responder (D40).
//
// Livres[t] é assentos − confirmados − em espera no trecho t (D25, D40). É uma
// fotografia, nunca uma promessa: o valor que vale é o que o dono verifica no
// PREPARAR.
type CaronaBusca struct {
	ID            string
	Empresa       string
	MotoristaNome string
	Rota          []string    // ex.: ["Salvador", "Feira de Santana", "Jequié"]
	Horarios      []time.Time // len == len(Rota)
	Assentos      int
	PrecoTrecho   []int // centavos; len == len(Rota)-1
	Livres        []int // len == len(Rota)-1
}

// PernaItinerario é um segmento contíguo de uma única carona dentro de um
// itinerário: dos índices De até Ate da rota daquela carona, consumindo os
// trechos De, De+1, …, Ate-1 (PROTOCOL.md, seção 5.8).
//
// Motorista é o nome de exibição, e não o identificador de login: quem lê o
// resultado da busca é o passageiro, que escolhe com quem viajar e nunca
// digita identificador (D15).
//
// Empresa é a dona da carona (D27), e é campo em vez de valor derivado do
// prefixo do CaronaID na borda, como acontece com o número de baldeações: a
// perna nasce de uma CaronaBusca, que já informa a empresa, e EmpresaDoID
// devolve erro — erro sem tratamento razoável num caminho em que o ID já foi
// validado. Na busca federada é também o que o passageiro precisa ver para
// saber com quantas empresas vai viajar (PROJETO_P2.md, seção 5.6).
type PernaItinerario struct {
	CaronaID      string
	Empresa       string
	Motorista     string
	De, Ate       int
	Origem        string
	Destino       string
	Partida       time.Time
	Chegada       time.Time
	PrecoCentavos int
}

// Itinerario é um caminho completo da origem ao destino pedidos, montado com
// uma ou mais pernas (RF07).
//
// É um valor derivado, calculado a cada busca e nunca guardado no Estado: a
// busca é informativa e não reserva nada (D07). O número de baldeações é
// len(Pernas)-1 e por isso não vira campo — dado derivável guardado em
// paralelo é dado que pode divergir.
type Itinerario struct {
	PrecoTotalCentavos int
	Partida            time.Time
	Chegada            time.Time
	Pernas             []PernaItinerario
}

// ReservaDetalhada é uma Reserva com os dados das caronas já resolvidos, do
// jeito que LISTAR_MINHAS_RESERVAS precisa entregá-los (PROTOCOL.md, seção
// 5.10): cidades, horários, preços e o nome do motorista.
//
// A Reserva guardada no Estado só tem índices — {carona, De, Ate} —, e é assim
// que deve continuar: o preço de um trecho e o horário de uma cidade pertencem
// à carona, e copiá-los para dentro da reserva criaria duas cópias do mesmo
// dado, que podem divergir. Esta struct é o resultado de resolver os índices, e
// não é guardada em lugar nenhum.
//
// Reaproveita Itinerario porque uma reserva confirmada é literalmente o
// itinerário que o passageiro escolheu na busca.
type ReservaDetalhada struct {
	ID         string
	Ativa      bool
	CriadaEm   time.Time
	Itinerario Itinerario
}
