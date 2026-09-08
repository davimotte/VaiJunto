package servidor

import (
	"errors"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// Tabela única de tradução de erro de domínio para código do protocolo
// (PROJETO.md, seção 5.3).
//
// internal/dominio não importa internal/protocolo — o domínio não conhece a
// rede —, então os sentinelas não carregam código nem texto de protocolo. É
// aqui, e só aqui, que os dois vocabulários se encontram. Concentrar isso em
// uma tabela, em vez de espalhar por dentro dos handlers, é o que torna
// possível o teste que exige entrada para todo sentinela exportado.
//
// A mensagem é escrita para ser exibida direto no CLI, sem que o cliente
// precise conhecer o código.
type erroTraduzido struct {
	Codigo   string
	Mensagem string
}

var traducaoErros = map[error]erroTraduzido{
	dominio.ErrCidadeDesconhecida:   {protocolo.CodigoCidadeDesconhecida, "Cidade fora do corredor atendido."},
	dominio.ErrRotaInvalida:         {protocolo.CodigoRotaInvalida, "Rota inválida: verifique origem, destino e a quantidade de preços informada."},
	dominio.ErrPartidaInvalida:      {protocolo.CodigoPartidaInvalida, "A partida precisa ser um instante futuro."},
	dominio.ErrAssentosInvalidos:    {protocolo.CodigoCampoInvalido, "A carona precisa ter pelo menos um assento."},
	dominio.ErrPrecoInvalido:        {protocolo.CodigoCampoInvalido, "O preço de um trecho não pode ser negativo."},
	dominio.ErrCredenciaisInvalidas: {protocolo.CodigoCredenciaisInvalidas, "Usuário ou senha incorretos."},
	dominio.ErrCaronaNaoEncontrada:  {protocolo.CodigoCaronaNaoEncontrada, "Carona não encontrada."},
	dominio.ErrNaoEDono:             {protocolo.CodigoNaoEDono, "Esta carona pertence a outro motorista."},
	dominio.ErrGeracaoDeID:          {protocolo.CodigoErroInterno, "Falha interna ao gerar o identificador da carona."},
}

// respostaDeErroDeDominio monta a resposta de erro correspondente ao erro
// devolvido pelo domínio.
//
// A comparação é por errors.Is, e não por igualdade: o domínio embrulha os
// sentinelas com %w para acrescentar contexto à mensagem interna (qual cidade,
// qual carona), e o embrulho precisa continuar sendo reconhecido.
//
// Sentinela sem entrada na tabela cai em ERRO_INTERNO: um código errado é
// pior que um código genérico, porque o cliente tomaria decisão em cima dele.
// O teste de cobertura de erros_test.go existe para que esse caminho nunca
// seja exercido por um erro previsto.
func respostaDeErroDeDominio(id string, err error) protocolo.Resposta {
	for sentinela, traducao := range traducaoErros {
		if errors.Is(err, sentinela) {
			return respostaErro(id, traducao.Codigo, traducao.Mensagem)
		}
	}
	return respostaErro(id, protocolo.CodigoErroInterno, "Falha inesperada ao processar a operação.")
}
