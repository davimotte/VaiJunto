package dominio

import (
	"sync"
	"time"
)

// nomeFusoDasCidades é o fuso das cidades atendidas, todas na Bahia.
const nomeFusoDasCidades = "America/Bahia"

// FusoDasCidades devolve o fuso em que um dia civil e uma hora digitada
// ganham sentido: o servidor lê nele a data de BUSCAR_ITINERARIOS, e o cliente
// monta nele os horários que o motorista digita.
//
// É uma única função para as duas pontas porque o fuso é propriedade das
// cidades (D09), e não da máquina: time.Local vem de TZ, que dentro do
// contêiner Alpine é UTC. Lida em UTC, a carona das 22:00 de Salvador cairia
// na busca do dia seguinte (PROJETO.md, seção 10.1).
//
// Depende do import de time/tzdata no main de cada binário. O deslocamento
// fixo é a rede de segurança para o caso de esse import sumir: perde um
// horário de verão hipotético, mas mantém o sistema em -03:00 em vez de
// cair em UTC.
func FusoDasCidades() *time.Location {
	return fusoDasCidades()
}

// fusoDasCidades resolve o fuso uma vez só. Resolver na primeira chamada, e
// não na inicialização do pacote, garante que o tzdata embutido pelo main já
// esteja registrado.
var fusoDasCidades = sync.OnceValue(func() *time.Location {
	if fuso, err := time.LoadLocation(nomeFusoDasCidades); err == nil {
		return fuso
	}
	return time.FixedZone("-03", -3*60*60)
})

// cidadesAtendidas é o conjunto fixo de cidades do sistema (D09; PROTOCOL.md,
// seção 3.1).
//
// A ordem da lista não tem significado de rota nem de distância: a sequência
// de paradas de cada carona e os horários são informados pelo motorista. A
// ordem só existe para que o menu enumerado dos clientes saia sempre igual.
var cidadesAtendidas = []string{
	"Salvador",
	"Feira de Santana",
	"Jequié",
	"Vitória da Conquista",
}

// CidadesAtendidas devolve as cidades do sistema, na ordem do menu.
//
// Existe para que os clientes oficiais montem o menu enumerado de cidades a
// partir da mesma lista que o servidor valida (PROTOCOL.md, seção 3): uma
// segunda lista escrita no cliente poderia divergir na grafia, e a comparação
// é por igualdade exata de string, então a divergência apareceria como
// CIDADE_DESCONHECIDA em tempo de execução.
//
// Devolve uma cópia porque o conjunto é constante do sistema (D09): quem
// consome não pode reordenar nem renomear a lista de dentro.
func CidadesAtendidas() []string {
	copia := make([]string, len(cidadesAtendidas))
	copy(copia, cidadesAtendidas)
	return copia
}

// CidadeConhecida responde se nome é uma das cidades atendidas.
//
// A comparação é por igualdade exata de string: os clientes oficiais escolhem
// a cidade em um menu enumerado (não digitam o nome), então a grafia que chega
// ao servidor é sempre a canônica, e normalizar aqui só esconderia um cliente
// com defeito.
func CidadeConhecida(nome string) bool {
	for _, cidade := range cidadesAtendidas {
		if cidade == nome {
			return true
		}
	}
	return false
}
