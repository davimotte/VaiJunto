// Comando servidor sobe o servidor central do VAIJUNTO.
package main

import (
	"flag"
	"log"

	_ "time/tzdata" // fusos embutidos no binário: necessário em imagens Alpine, que não trazem tzdata.

	"vaijunto/internal/dominio"
	"vaijunto/internal/servidor"
)

func main() {
	endereco := flag.String("endereco", "0.0.0.0:9000", "endereço e porta TCP em que o servidor escuta")
	caminhoUsuarios := flag.String("usuarios", "dados/usuarios.json", "arquivo de carga dos usuários")
	caminhoCaronas := flag.String("caronas", "dados/caronas.json", "arquivo de carga das caronas")
	flag.Parse()

	// Carga única no boot (D02, D12): daqui em diante o estado vive só em
	// memória. Falha de leitura aborta a subida em vez de deixar o servidor no
	// ar sem usuário nenhum, situação em que todo LOGIN falharia sem que a
	// causa aparecesse.
	estado, err := dominio.CarregarEstado(*caminhoUsuarios, *caminhoCaronas)
	if err != nil {
		log.Fatalf("servidor: carga inicial: %v", err)
	}

	s, err := servidor.Escutar(*endereco, estado)
	if err != nil {
		log.Fatalf("servidor: não foi possível escutar em %s: %v", *endereco, err)
	}
	log.Printf("servidor: escutando em %s", s.Endereco())

	if err := s.Aceitar(); err != nil {
		log.Fatalf("servidor: loop de aceitação encerrado: %v", err)
	}
}
