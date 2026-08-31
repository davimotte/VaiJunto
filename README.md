# VAIJUNTO

Sistema de caronas compartilhadas de média e longa distância, com servidor
central e clientes conectados por sockets TCP.

Problema 1 de TEC502 — Concorrência e Conectividade.

## O problema

Motoristas publicam caronas com rota, horário, assentos e preço por trecho.
Passageiros buscam itinerários entre duas cidades e reservam assentos. Como o
veículo passa por cidades intermediárias, a disponibilidade é controlada **por
trecho**: um assento ocupado entre a primeira e a segunda cidade permanece livre
nos trechos seguintes.

Um itinerário pode combinar trechos de caronas de motoristas diferentes, e sua
confirmação é **atômica**: ou todos os trechos são reservados, ou nenhum. Com
vários passageiros reservando ao mesmo tempo, o sistema nunca pode vender o mesmo
assento duas vezes nem deixar assentos bloqueados por uma reserva que não se
concluiu.

## Documentação

| Documento | Conteúdo |
|---|---|
| [`PROJETO.md`](PROJETO.md) | Requisitos, decisões de projeto, modelo de domínio, arquitetura, algoritmos, invariantes e plano de teste |
| [`PROTOCOL.md`](PROTOCOL.md) | Especificação do protocolo de aplicação: envelope, operações, códigos de erro, exemplos |

## Escolhas técnicas em uma frase cada

- **Go**, biblioteca padrão apenas, sem framework de RPC ou mensageria.
- **JSON delimitado por quebra de linha** sobre TCP, com conexão persistente.
- **Uma goroutine por conexão** e **um mutex global** sobre o estado.
- **Sem reserva em duas fases**: a busca não bloqueia nada, e toda a validação e
  escrita ocorrem juntas numa única seção crítica na confirmação.
- **Identidade ligada à conexão**, sem token de sessão.
- **Estado apenas em memória**, carregado de `dados/` no boot.

## Estrutura

```
cmd/servidor      binário do servidor central
cmd/motorista     CLI do motorista
cmd/passageiro    CLI do passageiro
internal/protocolo  envelope, structs de mensagem, códigos de erro
internal/dominio    corredor, caronas, reservas, estado e mutex
internal/servidor   listener, sessão, roteador
internal/cliente    conexão reaproveitada pelos dois CLIs
testes            teste de concorrência e carga
dados             usuarios.json e caronas.json
```

## Executando localmente

```bash
go run ./cmd/servidor --endereco 0.0.0.0:9000 \
  --usuarios dados/usuarios.json --caronas dados/caronas.json

VAIJUNTO_SERVIDOR=localhost:9000 go run ./cmd/passageiro
VAIJUNTO_SERVIDOR=localhost:9000 go run ./cmd/motorista
```

Verificando o servidor sem cliente:

```bash
printf '{"id":"1","tipo":"PING","dados":{}}\n' | nc localhost 9000
```

## Executando com Docker

Desenvolvimento em uma máquina:

```bash
docker compose up --build servidor
docker compose run --rm passageiro
```

Duas máquinas, como exigido na apresentação:

```bash
# Máquina A — servidor
docker build -f Dockerfile.servidor -t vaijunto-servidor .
docker run --rm -p 9000:9000 -v $(pwd)/dados:/dados vaijunto-servidor \
  --endereco 0.0.0.0:9000 --usuarios /dados/usuarios.json --caronas /dados/caronas.json

# Máquina B — cliente
docker build -f Dockerfile.cliente -t vaijunto-cliente .
docker run --rm -it -e VAIJUNTO_SERVIDOR=192.168.0.10:9000 vaijunto-cliente /bin/passageiro
```

O servidor precisa escutar em `0.0.0.0`, não em `127.0.0.1`, ou o mapeamento de
porta não recebe nada de fora do contêiner. Os clientes precisam de `-it`, porque
leem do terminal.

## Testes

```bash
go test -race ./...                  # unidade e integração
go test -race ./testes -run Carga -v # concorrência e carga
```

O detector de corrida do Go encontra acesso concorrente não sincronizado ao
estado mesmo quando o teste passa por sorte de escalonamento. Rodar sem `-race`
não vale como evidência.

## Usuários de teste

| Usuário | Senha | Perfil |
|---|---|---|
| joao, carlos, ana | `1234` | MOTORISTA |
| maria, pedro, lucia | `abcd` | PASSAGEIRO |
| teste01 … teste50 | `teste` | PASSAGEIRO |

## Cenário de demonstração

Buscar **Salvador → Vitória da Conquista em 15/09/2026**. Não existe carona
direta na data, então a baldeação é obrigatória, exatamente como no exemplo do
enunciado. Os detalhes do cenário e dos controles negativos estão na seção 9 do
`PROJETO.md`.
