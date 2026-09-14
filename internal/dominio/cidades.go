package dominio

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
