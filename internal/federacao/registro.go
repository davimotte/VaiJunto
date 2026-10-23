package federacao

import (
	"fmt"
	"io"
	"log"
	"strconv"
	"time"

	"vaijunto/internal/dominio"
)

// Registro escreve o registro de operações entre servidores (D19), no formato
// do exemplo da seção 5.2 do PROJETO_P2.md:
//
//	2026/11/05 08:15:02.431 [rotacapixaba] ← vaijunto GET /saude L=41 → 200 (1.8 ms)
//	2026/11/05 08:15:02.437 [vaijunto] → rotacapixaba GET /saude L=42 → 200 (2.1 ms)
//
// Nos recursos de transação, o handler acrescenta à mesma linha o que só ele
// sabe — txID, carimbo e voto —, e é por ela que o 2PC fica visível:
//
//	2026/11/05 08:15:02.431 [rotacapixaba] ← vaijunto POST /transacoes/vaijunto:res-3f0b11c4/preparar L=42 tx=vaijunto:res-3f0b11c4 carimbo=(41,vaijunto) voto=SIM → 200 (1.800 ms)
//
// A seta diz o sentido: ← é requisição recebida de um par, → é chamada que
// este servidor fez. É por estas linhas que o protocolo fica visível na
// demonstração.
//
// É um tipo próprio, e não o registro de internal/servidor: aquele pacote
// importa este (seção 5.3), e exportar o tipo de lá para cá inverteria a
// dependência. As duas implementações compartilham o formato do carimbo de
// instante — se um dia mudar, mude nos dois.
//
// Um *Registro nil não escreve nada, e é assim que o registro fica desligado:
// quem chama não precisa de um if antes de cada linha, e os testes, que sobem
// handlers sem registro, ficam sem ele por padrão.
//
// A escrita passa por um log.Logger porque ele é seguro para uso concorrente:
// uma goroutine por requisição HTTP escreve aqui, e cada linha sai inteira,
// sem se misturar com outra. Esse mutex nada tem a ver com o do estado (D04).
type Registro struct {
	empresa string
	saida   *log.Logger
}

// NovoRegistro liga o registro de operações REST desta empresa.
func NovoRegistro(saida io.Writer, empresa string) *Registro {
	// Sem prefixo e sem as flags de data do log: o instante é montado em
	// escrever, no fuso das cidades.
	return &Registro{empresa: empresa, saida: log.New(saida, "", 0)}
}

// recebida registra uma requisição REST atendida (camada 1–4' da seção 5.2).
//
// lamport é o L desta empresa depois do max+1 do recebimento, isto é, o mesmo
// valor que foi para o cabeçalho da resposta. Isso faz a linha do servidor que
// recebeu e a do que chamou poderem ser comparadas numa demonstração com os
// dois terminais lado a lado.
func (r *Registro) recebida(origem, metodo, caminho string, lamport uint64, detalhe string, status int, duracao time.Duration) {
	if r == nil {
		return
	}
	if detalhe != "" {
		detalhe = " " + detalhe
	}
	r.escrever(fmt.Sprintf("[%s] ← %s %s %s L=%d%s → %d (%s)",
		r.empresa, nomeDeEmpresa(origem), metodo, caminho, lamport, detalhe, status, emMilissegundos(duracao)))
}

// violacao registra uma transição impossível pela seção 7.4 do PROJETO_P2.md,
// respondida com 409 VIOLACAO_PROTOCOLO (seção 5.1 da API).
//
// Pelo argumento de atomicidade ela nunca deveria acontecer: um CONFIRMAR para
// participação abortada significaria que o coordenador decidiu confirmar sem
// unanimidade. A linha existe para que, se acontecer, o erro seja visível,
// registrado e verificável pelo teste, em vez de ficar escondido entre as
// respostas normais.
func (r *Registro) violacao(origem, metodo, caminho, detalhe string) {
	if r == nil {
		return
	}
	r.escrever(fmt.Sprintf("[%s] ‼ VIOLACAO_PROTOCOLO de %s em %s %s: %s",
		r.empresa, nomeDeEmpresa(origem), metodo, caminho, detalhe))
}

// enviada registra uma chamada a um par. resultado é o status HTTP ou o motivo
// da falha (prazo estourado, conexão recusada), que é o que a D30 trata como
// "o par não respondeu".
func (r *Registro) enviada(destino, metodo, caminho string, lamport uint64, resultado string, duracao time.Duration) {
	if r == nil {
		return
	}
	r.escrever(fmt.Sprintf("[%s] → %s %s %s L=%d → %s (%s)",
		r.empresa, nomeDeEmpresa(destino), metodo, caminho, lamport, resultado, emMilissegundos(duracao)))
}

// descartou registra uma carona recebida de um par e jogada fora no passo 5 da
// busca (PROJETO_P2.md, seção 6).
//
// É a linha que o documento pede ("descartar, com registro no log"), e ela é o
// que torna visível o caso que a validação existe para cobrir: um par rodando
// outra versão do binário. Sem ela, a carona simplesmente não apareceria na
// busca, e a diferença entre "o par não tem essa carona" e "o par mandou uma
// carona que recusamos" ficaria invisível na demonstração.
//
// O ID vem de fora e sai entre aspas, com escapes, pelo mesmo motivo de
// nomeDeEmpresa: sem isso, um par poria "\n" nele e escreveria linhas inteiras
// no nosso registro.
func (r *Registro) descartou(empresa, caronaID, motivo string) {
	if r == nil {
		return
	}
	r.escrever(fmt.Sprintf("[%s] ✗ %s carona %s descartada: %s",
		r.empresa, nomeDeEmpresa(empresa), strconv.Quote(caronaID), motivo))
}

// nomeDeEmpresa protege o registro de uma linha forjada: o nome vem do
// cabeçalho X-VaiJunto-Empresa, que é texto de fora. Nome fora do formato da
// D27 sai entre aspas, com escapes; nome ausente sai como "-".
//
// Sem isso, um par mal-intencionado poria "\n" no cabeçalho e escreveria
// linhas inteiras no registro de outra empresa.
func nomeDeEmpresa(empresa string) string {
	if empresa == "" {
		return "-"
	}
	if err := dominio.ValidarEmpresa(empresa); err != nil {
		return strconv.Quote(empresa)
	}
	return empresa
}

// emMilissegundos formata a duração com três casas, como na borda TCP. float
// só na exibição.
func emMilissegundos(d time.Duration) string {
	return fmt.Sprintf("%.3f ms", float64(d.Microseconds())/1000)
}

// escrever acrescenta o instante à linha e a entrega ao Logger.
//
// O instante é convertido para o fuso das cidades, e não deixado em
// time.Local, pelo mesmo motivo da borda TCP: no contêiner Alpine o fuso local
// é UTC, e o terminal mostraria 3 h a mais que os horários das caronas.
func (r *Registro) escrever(texto string) {
	instante := time.Now().In(dominio.FusoDasCidades()).Format("2006/01/02 15:04:05.000")
	r.saida.Print(instante + " " + texto)
}

// Decisao registra a decisão do coordenador sobre uma transação (D19; formato
// da seção 5.2 do PROJETO_P2.md):
//
//	2026/11/05 08:15:02.437 [vaijunto] DECISAO tx=vaijunto:res-3f0b11c4 → CONFIRMAR
//
// Não leva seta de sentido nem duração: não é uma mensagem trocada com um par,
// e sim o ponto em que esta empresa decidiu. É a linha que, lida junto com os
// "← PREPARAR … → SIM" dos participantes, torna o 2PC visível na arguição.
//
// É exportada porque quem a chama é o coordenador, em internal/transacao, pela
// interface RegistroDecisao que ele próprio declara — a federação importa a
// camada 5, nunca o contrário (seção 5.3).
//
// O txID vem do nosso próprio gerador, mas sai pelo mesmo caminho dos demais
// identificadores por disciplina: nada que vá para uma linha de registro é
// escrito sem passar pela montagem desta função.
func (r *Registro) Decisao(txID, decisao string) {
	if r == nil {
		return
	}
	r.escrever(fmt.Sprintf("[%s] DECISAO tx=%s → %s", r.empresa, strconv.Quote(txID), decisao))
}
