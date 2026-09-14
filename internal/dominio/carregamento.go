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

// caronaArquivo é o formato de uma entrada de dados/caronas.json. Rota e
// horários vêm escritos por inteiro, parada a parada, e são guardados como
// estão: o servidor não deriva nada (D09).
//
// Um arquivo ainda no formato antigo (origem, destino e partida) chega aqui
// sem rota e sem horários, e é recusado pela validação em vez de subir um
// servidor com caronas vazias.
type caronaArquivo struct {
	ID             string      `json:"id"`
	Motorista      string      `json:"motorista"`
	Rota           []string    `json:"rota"`
	Horarios       []time.Time `json:"horarios"`
	Assentos       int         `json:"assentos"`
	PrecosCentavos []int       `json:"precos_centavos"`
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

// CarregarCaronas lê dados/caronas.json, valida cada carona com as mesmas
// regras da publicação (menos a da partida no futuro) e inicializa Livres igual
// a Assentos em todo trecho.
//
// A validação aqui não é zelo a mais: o arquivo é uma porta de entrada do
// estado tão real quanto o protocolo, e uma carona com horário fora de ordem
// quebraria a busca e a reserva do mesmo jeito, só que desde o boot.
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
		if err := validarCarona(c.Rota, c.Horarios, c.Assentos, c.PrecosCentavos); err != nil {
			return nil, fmt.Errorf("dominio: carona %s: %w", c.ID, err)
		}

		livres := make([]int, len(c.Rota)-1)
		for i := range livres {
			livres[i] = c.Assentos
		}

		caronas[c.ID] = &Carona{
			ID:          c.ID,
			MotoristaID: c.Motorista,
			Rota:        c.Rota,
			Horarios:    c.Horarios,
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
