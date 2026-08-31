// Comando servidor sobe o servidor central do VAIJUNTO.
package main

import (
	"flag"
	"log"

	_ "time/tzdata" // fusos embutidos no binário: necessário em imagens Alpine, que não trazem tzdata.

	"vaijunto/internal/servidor"
)

func main() {
	endereco := flag.String("endereco", "0.0.0.0:9000", "endereço e porta TCP em que o servidor escuta")
	flag.Parse()

	s, err := servidor.Escutar(*endereco)
	if err != nil {
		log.Fatalf("servidor: não foi possível escutar em %s: %v", *endereco, err)
	}
	log.Printf("servidor: escutando em %s", s.Endereco())

	if err := s.Aceitar(); err != nil {
		log.Fatalf("servidor: loop de aceitação encerrado: %v", err)
	}
}
