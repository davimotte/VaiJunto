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
// Livres começa igual a Assentos em cada posição e só é decrementado pela
// confirmação de reserva (D07), nunca pela busca.
type Carona struct {
	ID          string
	MotoristaID string
	Rota        []string    // ex.: ["Salvador", "Feira de Santana", "Jequié"]
	Horarios    []time.Time // len == len(Rota); informados pelo motorista, estritamente crescentes (D09, I6)
	Assentos    int         // capacidade total do veículo
	PrecoTrecho []int       // centavos; len == len(Rota)-1
	Livres      []int       // len == len(Rota)-1; inicia com Assentos
	Cancelada   bool
}

// ItemReserva referencia um trecho contíguo de uma Carona. De e Ate são
// índices de cidade na Rota da carona; a reserva consome os trechos
// [De, Ate).
type ItemReserva struct {
	CaronaID string
	De, Ate  int
}

// Reserva é um itinerário confirmado por um passageiro, possivelmente
// combinando trechos de caronas diferentes (RF07). É criada de uma só vez,
// dentro da seção crítica da confirmação (D07): não existe reserva parcial.
type Reserva struct {
	ID           string
	PassageiroID string
	Itens        []ItemReserva
	Ativa        bool
	CriadaEm     time.Time
}

// Estado é todo o estado de domínio do servidor, protegido por um único
// sync.Mutex (D04). Funções de domínio recebem o Estado já travado por quem
// chama e nunca adquirem mu por conta própria (D05): só a camada de estado
// adquire o lock, o que elimina por construção o risco de reentrância no
// mesmo mutex por caminhos diferentes.
type Estado struct {
	mu       sync.Mutex
	usuarios map[string]*Usuario
	caronas  map[string]*Carona
	reservas map[string]*Reserva
}

// PernaItinerario é um segmento contíguo de uma única carona dentro de um
// itinerário: dos índices De até Ate da rota daquela carona, consumindo os
// trechos De, De+1, …, Ate-1 (PROTOCOL.md, seção 5.8).
//
// Motorista é o nome de exibição, e não o identificador de login: quem lê o
// resultado da busca é o passageiro, que escolhe com quem viajar e nunca
// digita identificador (D15).
type PernaItinerario struct {
	CaronaID      string
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
