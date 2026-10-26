package testes

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Verificação de invariantes contra um consórcio **já no ar** — o compose da
// seção 13.3 ou as três máquinas do laboratório.
//
// O simulador fala só TCP e não conhece /auditoria: ele é um cliente, e a carga
// que gera é a de um cliente. Conferir as invariantes da seção 11.1 é trabalho
// de quem observa as três empresas de fora, e é este teste. Rodar a carga e
// rodar esta verificação são, portanto, dois comandos:
//
//	go run ./cmd/simulador --cenario=federado --passageiros=20 --duracao=60s
//	VAIJUNTO_AUDITORIA=vaijunto=http://localhost:8081,rotacapixaba=http://localhost:8082,rotafluminense=http://localhost:8083 \
//	  go test -run TestInvariantesDoAmbiente ./testes
//
// É a mesma verificação dos testes em processo, sobre os mesmos retratos: o que
// muda é só de onde vêm as URLs.

// variavelAuditoria lista as empresas a observar, no formato
// empresa=urlBase,empresa=urlBase. As empresas precisam ter subido com
// --auditoria, que é o que o compose faz.
const variavelAuditoria = "VAIJUNTO_AUDITORIA"

// prazoDeQuiescenciaNoAmbiente é mais largo que o dos testes em processo: aqui
// os prazos da D41 valem inteiros — a repetição da fase 2 é de 2 s e a dúvida
// do participante, de 10 s —, e depois de uma carga pode haver aviso de outbox
// a entregar.
const prazoDeQuiescenciaNoAmbiente = 90 * time.Second

// TestInvariantesDoAmbiente confere I-P2-1 a I-P2-5 num consórcio já no ar,
// depois de esperar a quiescência.
//
// Fica fora da suíte normal porque depende de um ambiente externo: sem a
// variável, nada a observar.
func TestInvariantesDoAmbiente(t *testing.T) {
	urls, err := empresasDaVariavel(os.Getenv(variavelAuditoria))
	if err != nil {
		t.Fatalf("%s: %v", variavelAuditoria, err)
	}
	if len(urls) == 0 {
		t.Skipf("verificação de ambiente: aponte as empresas em %s=empresa=urlBase,empresa=urlBase (seção 11.2)", variavelAuditoria)
	}

	t.Logf("observando %d empresa(s) por até %s", len(urls), prazoDeQuiescenciaNoAmbiente)
	esperarQuiescenciaEm(t, prazoDeQuiescenciaNoAmbiente, urls)
	verificarInvariantesP2(t, auditoriasDeURLs(t, urls))
}

// empresasDaVariavel lê a lista no mesmo formato de --pares do servidor (D20).
func empresasDaVariavel(lista string) (map[string]string, error) {
	if strings.TrimSpace(lista) == "" {
		return nil, nil
	}
	urls := make(map[string]string)
	for _, item := range strings.Split(lista, ",") {
		nome, urlBase, achou := strings.Cut(strings.TrimSpace(item), "=")
		nome, urlBase = strings.TrimSpace(nome), strings.TrimSpace(urlBase)
		if !achou || nome == "" || urlBase == "" {
			return nil, fmt.Errorf("item %q: use o formato empresa=urlBase", item)
		}
		urls[nome] = strings.TrimSuffix(urlBase, "/")
	}
	return urls, nil
}
