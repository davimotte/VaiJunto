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
func (r *Registro) recebida(origem, metodo, caminho string, lamport uint64, status int, duracao time.Duration) {
	if r == nil {
		return
	}
	r.escrever(fmt.Sprintf("[%s] ← %s %s %s L=%d → %d (%s)",
		r.empresa, nomeDeEmpresa(origem), metodo, caminho, lamport, status, emMilissegundos(duracao)))
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
