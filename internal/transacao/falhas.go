package transacao

import (
	"fmt"
	"strings"
)

// Injeção de falhas por pontos nomeados (D31; PROJETO_P2.md, seção 9.5).
//
// A variável de ambiente FALHAR_EM=<ponto> faz o servidor morrer exatamente num
// ponto do protocolo. Sem ela, nenhum ponto está ativo.
//
// Por que isto existe: os estados que a recuperação do 2PC precisa resolver são
// instantes de microssegundos — entre o fsync do PREPARADO e a resposta, entre a
// decisão gravada e o primeiro CONFIRMAR. Derrubar o servidor "no meio de uma
// reserva" com um `docker kill` cronometrado à mão acerta esses instantes por
// sorte, e um teste que depende de sorte não prova nada. Com os pontos, a queda
// acontece **sempre** no mesmo lugar, e o TF1 a TF8 passam a afirmar o
// comportamento da tabela da seção 9.5 em vez de uma amostra dele.
//
// Por que variável de ambiente e não flag (seção 13.1): o ponto de falha não é
// configuração do servidor, é sabotagem deliberada. Como flag, apareceria na
// linha de comando de toda execução normal — inclusive na do laboratório — e
// seria só uma questão de tempo até alguém demonstrar com um ponto armado.

// Ponto é um lugar nomeado do protocolo onde o servidor pode ser derrubado.
//
// É tipo próprio, e não string solta, para que o compilador separe um ponto de
// um motivo de voto ou de um identificador de empresa: todos são strings
// minúsculas e nenhum deles é intercambiável.
type Ponto string

// Os pontos da tabela da seção 9.5, um por teste de TF1 a TF8.
//
// Esta é a **única** lista dos nomes. O boot a usa para validar FALHAR_EM, a
// mensagem de erro para dizer o que é válido e os testes para armar os pontos —
// ninguém escreve o literal de novo. Duas listas do mesmo vocabulário acabam
// divergindo, e aqui a divergência seria silenciosa: o ponto simplesmente nunca
// dispararia.
const (
	PontoCoordAposPreparar     Ponto = "coord-apos-preparar"
	PontoCoordAposDecidir      Ponto = "coord-apos-decidir"
	PontoCoordDuranteConfirmar Ponto = "coord-durante-confirmar"
	PontoPartAntesDeGravar     Ponto = "part-antes-de-gravar"
	PontoPartAposGravar        Ponto = "part-apos-gravar"
	PontoPartAposResponder     Ponto = "part-apos-responder"
	PontoPartAposConfirmar     Ponto = "part-apos-confirmar"

	// PontoAvisoAposRegistrar é o ponto do outbox (TF8). O nome existe desde já,
	// para que a lista de FALHAR_EM seja a da seção 9.5 inteira, mas ninguém o
	// aciona: o outbox nasce com os cancelamentos da D39, na fase 8. Armá-lo
	// hoje é um servidor que nunca cai.
	PontoAvisoAposRegistrar Ponto = "aviso-apos-registrar"
)

// PontosConhecidos são os valores aceitos em FALHAR_EM, na ordem da tabela da
// seção 9.5.
var PontosConhecidos = []Ponto{
	PontoCoordAposPreparar,
	PontoCoordAposDecidir,
	PontoCoordDuranteConfirmar,
	PontoPartAntesDeGravar,
	PontoPartAposGravar,
	PontoPartAposResponder,
	PontoPartAposConfirmar,
	PontoAvisoAposRegistrar,
}

// VariavelFalharEm é o nome da variável de ambiente que arma um ponto (D31).
//
// Fica como constante, e não espalhada como literal, pelo mesmo motivo de
// cliente.VariavelServidor: o nome aparece no boot, na mensagem de erro, no
// docker-compose.yml e na documentação, e quem o lê no código acha de onde vem.
const VariavelFalharEm = "FALHAR_EM"

// Injetor guarda o ponto armado nesta instância do servidor e o que fazer
// quando ele é alcançado.
//
// É **por instância**, e não um global do pacote, porque os testes multiempresa
// sobem as três empresas no mesmo processo (seção 11.3): um global armaria o
// ponto nas três ao mesmo tempo, e o cenário deixaria de ser "a empresa X caiu".
//
// A ação de cair é injetada, e por isso este arquivo não importa os: em
// produção, cmd/servidor passa uma função que escreve a linha de registro e
// chama os.Exit; nos testes em processo, uma que fecha as portas e o log da
// empresa e abandona a goroutine (seção 9.5). O protocolo é o mesmo nos dois
// casos; o que muda é o tamanho do que morre.
type Injetor struct {
	ponto Ponto
	cair  func(Ponto)
}

// NovoInjetor monta o injetor a partir do valor de FALHAR_EM.
//
// Nome vazio devolve (nil, nil): é o caso normal, e um *Injetor nil é inativo
// (ver Em). Nome desconhecido é erro, e o boot recusa subir — um ponto escrito
// errado viraria um teste de falha que passa sem que nada tenha caído, que é
// pior do que não ter o teste.
//
// cair nunca retorna. Quem o fornece garante isso; o código que chama Em conta
// com isso para não precisar de um `return` depois de cada ponto.
func NovoInjetor(nome string, cair func(Ponto)) (*Injetor, error) {
	if nome == "" {
		return nil, nil
	}
	for _, p := range PontosConhecidos {
		if Ponto(nome) == p {
			return &Injetor{ponto: p, cair: cair}, nil
		}
	}
	nomes := make([]string, 0, len(PontosConhecidos))
	for _, p := range PontosConhecidos {
		nomes = append(nomes, string(p))
	}
	return nil, fmt.Errorf("ponto de falha %q não existe; os pontos da seção 9.5 são: %s",
		nome, strings.Join(nomes, ", "))
}

// Ponto devolve o ponto armado, para a linha de registro do boot.
func (i *Injetor) Ponto() Ponto {
	if i == nil {
		return ""
	}
	return i.ponto
}

// Em derruba o servidor se este for o ponto armado, e não faz nada em qualquer
// outro caso.
//
// O receptor nil é inativo de propósito: é assim que o ponto fica desligado por
// padrão (D31) sem que cada chamador precise de um `if falha != nil` antes, e é
// o mesmo padrão do *registro nil da borda TCP e da RegistroDecisao nil do
// coordenador.
//
// Não retorna quando dispara.
func (i *Injetor) Em(ponto Ponto) {
	if i == nil || i.ponto != ponto {
		return
	}
	i.cair(ponto)
}
