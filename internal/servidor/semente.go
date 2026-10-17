package servidor

import (
	"fmt"
	"os"
	"path/filepath"

	"vaijunto/internal/dominio"
)

// CarregarSemente lê usuarios.json e caronas.json do diretório da empresa e
// monta o Estado inicial (D22, D37: a semente é carregada antes do log).
//
// A leitura de arquivo mora aqui, e não em internal/dominio, porque o domínio
// não importa pacotes de disco (regra 8); ele só recebe os bytes.
func CarregarSemente(empresa, diretorio string) (*dominio.Estado, error) {
	usuarios, err := os.ReadFile(filepath.Join(diretorio, "usuarios.json"))
	if err != nil {
		return nil, fmt.Errorf("servidor: ler usuários da semente: %w", err)
	}
	caronas, err := os.ReadFile(filepath.Join(diretorio, "caronas.json"))
	if err != nil {
		return nil, fmt.Errorf("servidor: ler caronas da semente: %w", err)
	}
	return dominio.CarregarEstado(empresa, usuarios, caronas)
}
