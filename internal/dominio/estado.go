package dominio

import "time"

// Este arquivo é a camada 6 do PROJETO.md (seção 5.1) e o **único** lugar do
// programa que adquire e.mu (D04, D05).
//
// Cada método segue a mesma forma: trava, chama a regra correspondente de
// caronas.go, destrava com defer. Como as regras são não exportadas, esta é a
// única porta de entrada no estado, e a propriedade "toda leitura e toda
// escrita do domínio acontecem sob o mutex" vale por construção, sem depender
// de disciplina de quem escreve o código depois.
//
// Um segundo efeito importante: como nenhum método devolve ponteiro para
// dentro do estado, nada do que sai daqui pode ser lido fora da seção crítica
// enquanto outra goroutine escreve. Todos devolvem cópias.

// NovoEstado cria um Estado vazio. Serve aos testes; a execução real usa
// CarregarEstado, que popula usuários e caronas a partir de dados/ (D02).
func NovoEstado() *Estado {
	return &Estado{
		usuarios: make(map[string]*Usuario),
		caronas:  make(map[string]*Carona),
		reservas: make(map[string]*Reserva),
	}
}

// Autenticar confere as credenciais de LOGIN e devolve o usuário
// correspondente (PROTOCOL.md, seção 5.2).
//
// Devolve uma cópia do Usuario justamente para que a goroutine da conexão
// possa guardá-la como identidade da sessão (D08) e consultá-la a cada
// requisição sem tocar no estado compartilhado de novo.
func (e *Estado) Autenticar(usuario, senha string) (Usuario, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return autenticar(e, usuario, senha)
}

// PublicarCarona registra uma nova carona do motorista (PROTOCOL.md, seção
// 5.4). Validação e escrita acontecem juntas, na mesma seção crítica.
func (e *Estado) PublicarCarona(motoristaID, origem, destino string, partida time.Time, assentos int, precos []int, agora time.Time) (Carona, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return publicarCarona(e, motoristaID, origem, destino, partida, assentos, precos, agora)
}

// CaronasDoMotorista lista as caronas publicadas pelo motorista
// (PROTOCOL.md, seção 5.5).
func (e *Estado) CaronasDoMotorista(motoristaID string, incluirCanceladas bool) []Carona {
	e.mu.Lock()
	defer e.mu.Unlock()
	return caronasDoMotorista(e, motoristaID, incluirCanceladas)
}

// DetalharCarona devolve a carona e os passageiros confirmados por trecho
// (PROTOCOL.md, seção 5.6), desde que motoristaID seja o dono.
func (e *Estado) DetalharCarona(caronaID, motoristaID string) (Carona, [][]PassageiroConfirmado, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return detalharCarona(e, caronaID, motoristaID)
}
