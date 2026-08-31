// Package protocolo define o contrato de mensagens entre clientes e servidor
// do VAIJUNTO: o envelope de requisição/resposta, os tipos de dados de cada
// operação, os códigos de erro e o enquadramento das mensagens na conexão
// TCP. É compartilhado entre servidor e clientes; não conhece rede nem
// domínio (ver PROTOCOL.md).
package protocolo

import "encoding/json"

// Requisicao é o envelope enviado pelo cliente (PROTOCOL.md, seção 2.1).
type Requisicao struct {
	ID    string          `json:"id"`
	Tipo  string          `json:"tipo"`
	Dados json.RawMessage `json:"dados"`
}

// Resposta é o envelope devolvido pelo servidor (PROTOCOL.md, seção 2.2).
//
// Codigo e Mensagem só são preenchidos quando Status == StatusErro; por isso
// levam omitempty, para que uma resposta OK não carregue campos vazios.
type Resposta struct {
	ID       string          `json:"id"`
	Status   string          `json:"status"`
	Codigo   string          `json:"codigo,omitempty"`
	Mensagem string          `json:"mensagem,omitempty"`
	Dados    json.RawMessage `json:"dados"`
}
