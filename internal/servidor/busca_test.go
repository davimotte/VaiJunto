package servidor

import (
	"context"
	"time"

	"vaijunto/internal/dominio"
	"vaijunto/internal/federacao"
	"vaijunto/internal/transacao"
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

// coordenadorDeTeste monta um coordenador sobre o estado informado.
//
// Os testes deste pacote exercitam enquadramento, sessão e roteamento: nenhum
// deles reserva de verdade, e por isso a rede fica nil — chamá-la seria pânico,
// e é bom que seja, porque significaria que um teste da borda TCP passou a
// depender de três servidores no ar. O 2PC de verdade é exercitado em ./testes.
//
// O votante local existe porque o roteamento de RESERVAR precisa de um
// coordenador não nil para despachar.
func coordenadorDeTeste(estado *dominio.Estado) *transacao.Coordenador {
	return transacao.NovoCoordenador(transacao.ConfigCoordenador{
		Empresa:      estado.Empresa(),
		Estado:       estado,
		Relogio:      transacao.NovoRelogio(),
		Participante: federacao.NovoVotanteLocal(transacao.NovoParticipante(estado)),
	})
}

// nucleoDeTeste é o núcleo dos testes que só precisam de um servidor que
// responda: estado vazio desta empresa, busca que não acha nada e coordenador
// sem pares.
func nucleoDeTeste() *nucleo {
	estado := dominio.NovoEstado("vaijunto")
	return &nucleo{estado: estado, busca: buscaVazia{}, coordenador: coordenadorDeTeste(estado)}
}
