package dominio

import (
	"encoding/json"
	"fmt"
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

// CarregarUsuarios decodifica o conteúdo de dados/usuarios.json (D12): carga
// única no boot, sem operação de cadastro.
//
// Recebe os bytes, e não o caminho, porque o domínio não toca o disco (regra
// 8): quem lê o arquivo é internal/servidor.CarregarSemente.
func CarregarUsuarios(conteudo []byte) (map[string]*Usuario, error) {
	var brutos []usuarioArquivo
	if err := json.Unmarshal(conteudo, &brutos); err != nil {
		return nil, fmt.Errorf("dominio: decodificar usuários: %w", err)
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

// CarregarCaronas decodifica o conteúdo do arquivo de caronas da empresa, confere que cada ID leva
// o prefixo dela (D27), valida cada carona com as mesmas
// regras da publicação (menos a da partida no futuro) e inicializa Livres igual
// a Assentos em todo trecho.
//
// A validação aqui não é zelo a mais: o arquivo é uma porta de entrada do
// estado tão real quanto o protocolo, e uma carona com horário fora de ordem
// quebraria a busca e a reserva do mesmo jeito, só que desde o boot.
func CarregarCaronas(empresa string, conteudo []byte) (map[string]*Carona, error) {
	var brutos []caronaArquivo
	if err := json.Unmarshal(conteudo, &brutos); err != nil {
		return nil, fmt.Errorf("dominio: decodificar caronas: %w", err)
	}

	caronas := make(map[string]*Carona, len(brutos))
	for _, c := range brutos {
		// O prefixo é o que diz quem é o dono do trecho (D27): semente com ID
		// de outra empresa, ou sem prefixo, faria o roteamento por ID mentir.
		if dona, err := EmpresaDoID(c.ID); err != nil || dona != empresa {
			return nil, fmt.Errorf("dominio: carona %q: o ID precisa começar por %q", c.ID, empresa+separadorEmpresa)
		}
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

// CarregarEstado monta o Estado inicial do servidor a partir do conteúdo dos
// dois arquivos de carga: nenhuma reserva existe antes do boot (seção 14,
// limitação das fases 2 a 4). O log do estado começa em memória e o relógio
// parado; o servidor troca os dois com UsarLog e UsarRelogio antes de aceitar
// conexões.
func CarregarEstado(empresa string, usuariosJSON, caronasJSON []byte) (*Estado, error) {
	if err := ValidarEmpresa(empresa); err != nil {
		return nil, err
	}
	usuarios, err := CarregarUsuarios(usuariosJSON)
	if err != nil {
		return nil, err
	}
	caronas, err := CarregarCaronas(empresa, caronasJSON)
	if err != nil {
		return nil, err
	}
	return &Estado{
		empresa:             empresa,
		usuarios:            usuarios,
		caronas:             caronas,
		reservas:            make(map[string]*Reserva),
		participacoes:       make(map[string]*Participacao),
		emAndamento:         make(map[string]bool),
		transacoesEmVotacao: make(map[string]bool),
		log:                 NovaLogMemoria(),
		lamport:             relogioParado,
	}, nil
}
