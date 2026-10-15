package dominio

import (
	"fmt"
	"regexp"
	"strings"
)

// separadorEmpresa divide o prefixo da empresa do restante do identificador
// (D27): "vaijunto:car-1". Por isso o nome da empresa não pode conter ":".
const separadorEmpresa = ":"

// formatoEmpresa é o formato aceito para o nome de uma empresa. É estreito de
// propósito: o nome entra em IDs, em URLs de pares e em nomes de arquivo, e
// maiúscula, espaço ou ":" em qualquer um desses lugares viraria bug de
// comparação que só apareceria entre duas máquinas.
var formatoEmpresa = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ValidarEmpresa confere o formato do identificador de empresa (D27).
func ValidarEmpresa(nome string) error {
	if !formatoEmpresa.MatchString(nome) {
		return fmt.Errorf("%w: %q (use minúsculas, dígitos e hífen, começando por letra)", ErrEmpresaInvalida, nome)
	}
	return nil
}

// EmpresaDoID extrai a empresa dona de um identificador de carona ou de
// reserva (D27), sem consultar ninguém: o prefixo responde sozinho.
//
// Recusa ID sem prefixo, e não assume a empresa local, porque um ID que perdeu
// o prefixo em algum ponto do caminho seria roteado para a empresa errada sem
// que nada acusasse.
func EmpresaDoID(id string) (string, error) {
	empresa, resto, ok := strings.Cut(id, separadorEmpresa)
	if !ok || empresa == "" || resto == "" {
		return "", fmt.Errorf("%w: %q", ErrIDSemEmpresa, id)
	}
	return empresa, nil
}

// prefixar monta um identificador global a partir do sufixo local (D27).
func prefixar(empresa, local string) string {
	return empresa + separadorEmpresa + local
}
