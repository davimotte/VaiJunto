package servidor

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"vaijunto/internal/dominio"
)

// ErrParesInvalidos — a flag --pares está mal formada (D20). Barra o boot do
// servidor; não nasce de nenhuma requisição.
var ErrParesInvalidos = errors.New("servidor: lista de pares inválida")

// ParsearPares lê a lista estática de pares da D20 ("empresa=url,empresa=url")
// e devolve empresa → URL base.
//
// Esta fase só lê e valida; ninguém usa o mapa ainda (o cliente HTTP entra na
// fase 3A). Validar já no boot é o que faz um erro de digitação aparecer na
// subida, e não no meio de uma busca federada, em outra máquina.
//
// As recusas existem por motivos concretos:
//   - a própria empresa: um servidor chamando a si mesmo por HTTP tomaria o
//     próprio lock numa rota que o P1 nunca percorreu;
//   - empresa repetida: duas URLs para o mesmo nome deixariam a escolha
//     dependente da ordem na linha de comando;
//   - URL malformada: sem esquema e host, a falha só apareceria na primeira
//     chamada, como "empresa indisponível" (D30), indistinguível de uma queda.
//
// Lista vazia é válida: um servidor pode subir sozinho (P1 e testes).
func ParsearPares(propria, lista string) (map[string]string, error) {
	pares := make(map[string]string)
	if lista == "" {
		return pares, nil
	}

	for _, item := range strings.Split(lista, ",") {
		empresa, endereco, ok := strings.Cut(item, "=")
		if !ok {
			return nil, fmt.Errorf("%w: %q não está no formato empresa=url", ErrParesInvalidos, item)
		}
		if err := dominio.ValidarEmpresa(empresa); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrParesInvalidos, err)
		}
		if empresa == propria {
			return nil, fmt.Errorf("%w: %q é a própria empresa", ErrParesInvalidos, empresa)
		}
		if _, repetida := pares[empresa]; repetida {
			return nil, fmt.Errorf("%w: %q aparece mais de uma vez", ErrParesInvalidos, empresa)
		}
		if err := validarURLDePar(endereco); err != nil {
			return nil, fmt.Errorf("%w: empresa %q: %v", ErrParesInvalidos, empresa, err)
		}
		pares[empresa] = endereco
	}
	return pares, nil
}

// validarURLDePar exige esquema http ou https e um host. url.Parse sozinho
// aceita quase tudo ("10.0.0.2:8080" vira esquema "10.0.0.2"), então o esquema
// e o host são conferidos à parte.
func validarURLDePar(endereco string) error {
	u, err := url.Parse(endereco)
	if err != nil {
		return fmt.Errorf("URL malformada %q: %v", endereco, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL %q precisa começar por http:// ou https://", endereco)
	}
	if u.Host == "" {
		return fmt.Errorf("URL %q sem host", endereco)
	}
	return nil
}
