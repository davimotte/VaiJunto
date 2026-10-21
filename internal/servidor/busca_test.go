package servidor

import (
	"context"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/federacao"
)

// buscaVazia é a dublê de BuscaFederada dos testes deste pacote.
//
// Existe porque a interface é declarada aqui, no consumidor: os testes da borda
// TCP exercitam enquadramento, sessão e roteamento, e nenhum deles precisa de
// três servidores HTTP no ar para isso. A busca federada de verdade é
// exercitada em ./testes, contra o consórcio inteiro (TB1 e TB2).
//
// Devolve resultado vazio e não parcial: é o que uma busca sem par nenhum
// devolveria, e qualquer resposta serve aos testes que a usam — o que eles
// afirmam é que a operação foi reconhecida e atendida, não o seu conteúdo.
type buscaVazia struct{}

func (buscaVazia) Buscar(context.Context, string, string, time.Time) (federacao.Resultado, error) {
	return federacao.Resultado{
		Itinerarios:           []dominio.Itinerario{},
		EmpresasIndisponiveis: []string{},
	}, nil
}

// nucleoDeTeste é o núcleo dos testes que só precisam de um servidor que
// responda: estado vazio desta empresa e busca que não acha nada.
func nucleoDeTeste() *nucleo {
	return &nucleo{estado: dominio.NovoEstado("vaijunto"), busca: buscaVazia{}}
}
