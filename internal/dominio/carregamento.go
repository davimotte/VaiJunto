package dominio

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// usuarioArquivo é o formato de uma linha de dados/usuarios.json.
type usuarioArquivo struct {
	Usuario string `json:"usuario"`
	Senha   string `json:"senha"`
	Nome    string `json:"nome"`
	Perfil  string `json:"perfil"`
}

// caronaArquivo é o formato de uma linha de dados/caronas.json: só origem,
// destino e partida, iguais ao que PUBLICAR_CARONA recebe (D09) — rota e
// horários completos são derivados na carga, não lidos do arquivo.
type caronaArquivo struct {
	ID             string    `json:"id"`
	Motorista      string    `json:"motorista"`
	Origem         string    `json:"origem"`
	Destino        string    `json:"destino"`
	Partida        time.Time `json:"partida"`
	Assentos       int       `json:"assentos"`
	PrecosCentavos []int     `json:"precos_centavos"`
}

// CarregarUsuarios lê dados/usuarios.json (D12): carga única no boot, sem
// operação de cadastro.
func CarregarUsuarios(caminho string) (map[string]*Usuario, error) {
	b, err := os.ReadFile(caminho)
	if err != nil {
		return nil, fmt.Errorf("dominio: ler %s: %w", caminho, err)
	}

	var brutos []usuarioArquivo
	if err := json.Unmarshal(b, &brutos); err != nil {
		return nil, fmt.Errorf("dominio: decodificar %s: %w", caminho, err)
	}

	usuarios := make(map[string]*Usuario, len(brutos))
	for _, u := range brutos {
		usuarios[u.Usuario] = &Usuario{
			Usuario: u.Usuario,
			Senha:   u.Senha,
			Nome:    u.Nome,
			Perfil:  u.Perfil,
		}
	}
	return usuarios, nil
}

// CarregarCaronas lê dados/caronas.json e deriva rota e horários de cada
// carona a partir do corredor (D09), com Livres inicializado igual a
// Assentos em todo trecho.
func CarregarCaronas(caminho string) (map[string]*Carona, error) {
	b, err := os.ReadFile(caminho)
	if err != nil {
		return nil, fmt.Errorf("dominio: ler %s: %w", caminho, err)
	}

	var brutos []caronaArquivo
	if err := json.Unmarshal(b, &brutos); err != nil {
		return nil, fmt.Errorf("dominio: decodificar %s: %w", caminho, err)
	}

	caronas := make(map[string]*Carona, len(brutos))
	for _, c := range brutos {
		rota, horarios, err := DerivarRotaEHorarios(c.Origem, c.Destino, c.Partida)
		if err != nil {
			return nil, fmt.Errorf("dominio: carona %s: %w", c.ID, err)
		}
		if len(c.PrecosCentavos) != len(rota)-1 {
			return nil, fmt.Errorf("dominio: carona %s: %d preços para %d trechos", c.ID, len(c.PrecosCentavos), len(rota)-1)
		}

		livres := make([]int, len(rota)-1)
		for i := range livres {
			livres[i] = c.Assentos
		}

		caronas[c.ID] = &Carona{
			ID:          c.ID,
			MotoristaID: c.Motorista,
			Rota:        rota,
			Horarios:    horarios,
			Assentos:    c.Assentos,
			PrecoTrecho: c.PrecosCentavos,
			Livres:      livres,
			Cancelada:   false,
		}
	}
	return caronas, nil
}

// CarregarEstado monta o Estado inicial do servidor a partir dos dois
// arquivos de carga (D02): nenhuma reserva existe antes do boot.
func CarregarEstado(caminhoUsuarios, caminhoCaronas string) (*Estado, error) {
	usuarios, err := CarregarUsuarios(caminhoUsuarios)
	if err != nil {
		return nil, err
	}
	caronas, err := CarregarCaronas(caminhoCaronas)
	if err != nil {
		return nil, err
	}
	return &Estado{
		usuarios: usuarios,
		caronas:  caronas,
		reservas: make(map[string]*Reserva),
	}, nil
}
