package dominio

import "errors"

// Erros sentinela do domínio (PROJETO.md, seção 5.3).
//
// São valores tipados **sem string de protocolo**: o domínio sabe qual erro
// aconteceu, mas não conhece a rede e por isso não pode conhecer os códigos
// do PROTOCOL.md. Quem traduz sentinela em código é a tabela única de
// internal/servidor. Comparar com errors.Is, nunca por texto da mensagem.
var (
	// ErrCidadeDesconhecida — cidade fora do corredor fixo (D09).
	ErrCidadeDesconhecida = errors.New("dominio: cidade fora do corredor")

	// ErrRotaInvalida — origem igual ao destino, ou quantidade de preços
	// diferente do número de trechos entre as duas cidades.
	ErrRotaInvalida = errors.New("dominio: rota inválida")

	// ErrPartidaInvalida — instante de partida que não está no futuro.
	ErrPartidaInvalida = errors.New("dominio: partida não está no futuro")

	// ErrAssentosInvalidos — capacidade menor que 1.
	ErrAssentosInvalidos = errors.New("dominio: quantidade de assentos inválida")

	// ErrPrecoInvalido — preço de trecho negativo.
	ErrPrecoInvalido = errors.New("dominio: preço de trecho inválido")

	// ErrCredenciaisInvalidas — usuário inexistente ou senha incorreta (D12).
	ErrCredenciaisInvalidas = errors.New("dominio: usuário ou senha incorretos")

	// ErrCaronaNaoEncontrada — identificador de carona inexistente.
	ErrCaronaNaoEncontrada = errors.New("dominio: carona não encontrada")

	// ErrNaoEDono — o recurso pertence a outro usuário.
	ErrNaoEDono = errors.New("dominio: recurso pertence a outro usuário")

	// ErrGeracaoDeID — a fonte de aleatoriedade falhou ao gerar um
	// identificador. Só existe para não engolir o erro de crypto/rand em
	// silêncio; na prática não acontece.
	ErrGeracaoDeID = errors.New("dominio: falha ao gerar identificador")
)

// TodosOsErros lista todos os sentinelas exportados do domínio.
//
// Existe para o teste de cobertura exigido pelo PROJETO.md (seção 5.3): a
// tabela de tradução de internal/servidor precisa ter entrada para cada um.
// Go não permite enumerar os símbolos de um pacote em tempo de execução, e
// sem esta lista um sentinela novo cairia calado em ERRO_INTERNO. Todo
// sentinela adicionado acima entra aqui.
func TodosOsErros() []error {
	return []error{
		ErrCidadeDesconhecida,
		ErrRotaInvalida,
		ErrPartidaInvalida,
		ErrAssentosInvalidos,
		ErrPrecoInvalido,
		ErrCredenciaisInvalidas,
		ErrCaronaNaoEncontrada,
		ErrNaoEDono,
		ErrGeracaoDeID,
	}
}
