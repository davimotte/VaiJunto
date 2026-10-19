package federacao

import (
	"time"

	"vaijunto/internal/dominio"
)

// Saude é o corpo de GET /saude (seção 4.1 da API). Também é o que o cliente
// decodifica ao chamar um par, por isso é exportado.
type Saude struct {
	Empresa   string `json:"empresa"`
	VersaoAPI int    `json:"versao_api"`
	// Instante é informativo, para conferência no laboratório. NENHUMA decisão
	// do consórcio usa o relógio físico de outro servidor (D24, seção 8.4): as
	// máquinas do laboratório não precisam estar sincronizadas, e a precedência
	// entre transações vem de (lamport, empresa).
	Instante time.Time `json:"instante"`
}

// saudeAtual monta a resposta de GET /saude (D30).
//
// O instante sai no fuso das cidades, e não em time.Local, para que no
// contêiner Alpine (onde o fuso local é UTC) a conferência no laboratório
// mostre a mesma hora dos horários das caronas.
func saudeAtual(empresa string) Saude {
	return Saude{
		Empresa:   empresa,
		VersaoAPI: VersaoAPI,
		Instante:  time.Now().In(dominio.FusoDasCidades()),
	}
}
