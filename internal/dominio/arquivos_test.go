package dominio

import "os"

// Os testes leem a semente do disco; o domínio, não (regra 8). Estes auxiliares
// fazem o papel de internal/servidor.CarregarSemente, que o domínio não pode
// importar.

func lerUsuarios(caminho string) (map[string]*Usuario, error) {
	b, err := os.ReadFile(caminho)
	if err != nil {
		return nil, err
	}
	return CarregarUsuarios(b)
}

func lerCaronas(empresa, caminho string) (map[string]*Carona, error) {
	b, err := os.ReadFile(caminho)
	if err != nil {
		return nil, err
	}
	return CarregarCaronas(empresa, b)
}

func lerEstado(empresa, caminhoUsuarios, caminhoCaronas string) (*Estado, error) {
	u, err := os.ReadFile(caminhoUsuarios)
	if err != nil {
		return nil, err
	}
	c, err := os.ReadFile(caminhoCaronas)
	if err != nil {
		return nil, err
	}
	return CarregarEstado(empresa, u, c)
}
