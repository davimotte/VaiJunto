# VAIJUNTO

Sistema de caronas compartilhadas de média e longa distância, com servidor
central e clientes conectados por sockets TCP.

**Problema 1 de TEC502 — Concorrência e Conectividade.** Entrega individual, com
apresentação, arguição e relatório no formato SBC.

**Autor:** _(preencher)_

## Documentação

| Documento | Conteúdo |
|---|---|
| [`PROJETO.md`](PROJETO.md) | Requisitos, decisões de projeto (D01–D19), modelo de domínio, arquitetura, algoritmos, invariantes e plano de teste |
| [`PROTOCOL.md`](PROTOCOL.md) | Especificação do protocolo de aplicação: enquadramento, envelope, as 11 operações, códigos de erro e exemplos |
| [`ROTEIRO.md`](ROTEIRO.md) | Manual de operação: preparar as duas máquinas, apresentar, demonstrar ao vivo e medir a carga |

## Índice

- [O problema](#o-problema)
- [Escolhas técnicas](#escolhas-técnicas)
- [Limites conhecidos](#limites-conhecidos)
- [Pré-requisitos](#pré-requisitos)
- [Executando localmente](#executando-localmente)
- [Como usar o sistema](#como-usar-o-sistema)
- [Registro de operações](#registro-de-operações)
- [Executando com Docker](#executando-com-docker)
- [Testes](#testes)
- [Usuários de teste](#usuários-de-teste)
- [Cenário de demonstração](#cenário-de-demonstração)
- [Estrutura do repositório](#estrutura-do-repositório)

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

## Escolhas técnicas

Cada uma tem a justificativa completa na decisão indicada do `PROJETO.md`.

| Escolha | Decisão |
|---|---|
| **Go**, biblioteca padrão apenas, sem framework de RPC ou mensageria | D01 |
| **Estado apenas em memória**, carregado de `dados/` no boot | D02 |
| **Uma goroutine por conexão** | D03 |
| **Um único mutex global** sobre todo o estado de domínio | D04 |
| Só a camada de estado adquire o lock; o domínio recebe o estado já travado | D05 |
| **JSON delimitado por quebra de linha** sobre TCP, com conexão persistente | D06, [`PROTOCOL.md` §1](PROTOCOL.md#1-transporte-e-enquadramento) |
| **Sem reserva em duas fases**: a busca não bloqueia nada, e toda a validação e a escrita ocorrem juntas numa única seção crítica na confirmação | D07 |
| **Identidade ligada à conexão**, sem token de sessão | D08 |
| **Clientes com menu interativo** e uma única conexão TCP por sessão | D15 |
| **Servidor central único**, sem réplicas | RNF10 |

O protocolo é o contrato: um cliente escrito em qualquer linguagem que respeite o
[`PROTOCOL.md`](PROTOCOL.md) interopera com o servidor. Porta padrão **9000**,
uma mensagem JSON por linha, teto de **64 KB** por linha
([§1](PROTOCOL.md#1-transporte-e-enquadramento)).

## Limites conhecidos

Assumidos e documentados, não descobertos em produção:

- A busca devolve **no máximo 10 itinerários** e as listagens, **no máximo 50
  itens**, porque a resposta é uma única linha sujeita ao teto de 64 KB. A
  ordenação vem antes do corte, então o limite nunca descarta um itinerário com
  menos baldeações para manter um com mais (D16).
- **Nenhum prazo toca o estado de domínio.** Há prazo de escrita no servidor
  (10 s) e de resposta no cliente (30 s), só no transporte. Como não existe
  assento em espera (D07), não existe nada que precise expirar (D17).
- A latência cresce com o histórico de reservas, porque a verificação de
  sobreposição percorre todas as reservas do estado. Só são comparáveis rodadas
  de carga com a mesma duração e servidor recém-iniciado (`PROJETO.md`, §8.3).

## Pré-requisitos

- **Go 1.23** para compilar e rodar os testes.
- **Docker** para a execução em contêiner e o teste com duas máquinas.

Nenhuma dependência externa: `go.mod` não tem nenhum `require`.

## Executando localmente

```bash
go build ./...
go run ./cmd/servidor --usuarios dados/usuarios.json --caronas dados/caronas.json
```

O servidor escuta em `0.0.0.0:9000` por padrão; `--endereco` muda isso.

Em outros terminais, os dois clientes:

```bash
VAIJUNTO_SERVIDOR=localhost:9000 go run ./cmd/passageiro
VAIJUNTO_SERVIDOR=localhost:9000 go run ./cmd/motorista
```

Verificando o servidor sem cliente, com uma requisição `PING` crua
([`PROTOCOL.md` §5.1](PROTOCOL.md#51-ping)):

```bash
printf '{"id":"1","tipo":"PING","dados":{}}\n' | nc -q1 localhost 9000
```

```json
{"id":"1","status":"OK","dados":{"servidor_em":"2026-09-17T19:51:54.562738584-03:00"}}
```

O `-q1` faz o `nc` encerrar um segundo depois do fim da entrada; sem ele, o
comando fica aguardando indefinidamente.

## Como usar o sistema

Os clientes são navegados por **menu numérico**. Uma execução mantém **uma
única** conexão TCP aberta, do `LOGIN` ao `LOGOUT`, que é o cenário para o qual o
protocolo foi desenhado (D15). O usuário nunca digita identificador: ao escolher
um item pelo número, o cliente devolve ao servidor os campos que ele mesmo
recebeu na resposta anterior.

### Passageiro

`1) Buscar itinerários` · `2) Minhas reservas` · `3) Cancelar reserva` ·
`4) Sair`

```
=== VAIJUNTO — Passageiro ===
Servidor: localhost:9000

Usuário: maria
Senha: abcd

Conectado a localhost:9000 como Maria Souza.

O que você quer fazer?
1) Buscar itinerários
2) Minhas reservas
3) Cancelar reserva
4) Sair
Escolha: 1

Origem:
1) Salvador
2) Feira de Santana
3) Jequié
4) Vitória da Conquista
Escolha: 1

Destino:
1) Salvador
2) Feira de Santana
3) Jequié
4) Vitória da Conquista
Escolha: 4

Data (AAAA-MM-DD): 2026-10-01

4 itinerários encontrados:

[1] R$ 110,00 — 01/10/2026 10:30 → 17:30 (direta)
    Salvador → Vitória da Conquista          10:30 → 17:30  João Silva   R$ 110,00

[2] R$ 80,00 — 01/10/2026 06:00 → 16:45 (1 baldeação)
    Salvador → Feira de Santana              06:00 → 07:45  João Silva   R$ 25,00
    Feira de Santana → Vitória da Conquista  09:30 → 16:45  Ana Ribeiro  R$ 55,00

[3] R$ 100,00 — 01/10/2026 06:00 → 14:30 (1 baldeação)
    Salvador → Jequié                        06:00 → 10:45  João Silva   R$ 60,00
    Jequié → Vitória da Conquista            12:15 → 14:30  Carlos Lima  R$ 40,00

[4] R$ 95,00 — 01/10/2026 06:00 → 14:30 (2 baldeações)
    Salvador → Feira de Santana              06:00 → 07:45  João Silva   R$ 25,00
    Feira de Santana → Jequié                08:15 → 11:15  Ana Ribeiro  R$ 30,00
    Jequié → Vitória da Conquista            12:15 → 14:30  Carlos Lima  R$ 40,00

Reservar qual? (0 para voltar): 3

Reserva res-05b7 confirmada. Total: R$ 100,00
```

Buscar e reservar são operações independentes: entre uma e outra nada fica
reservado nem bloqueado (D07). Se outro passageiro levar o último assento nesse
intervalo, a confirmação volta como `SEM_ASSENTO` e a mensagem diz qual trecho
esgotou.

### Motorista

`1) Publicar carona` · `2) Minhas caronas` ·
`3) Detalhar carona (passageiros por trecho)` · `4) Cancelar carona` · `5) Sair`

Ao publicar, o cliente pede as paradas na ordem em que o carro passa por elas —
cidade, data e hora de cada uma —, depois os assentos e o preço de cada trecho.
As cidades já usadas na rota aparecem marcadas com `(já na rota)`, porque uma
rota não pode repetir cidade (D09). O detalhamento mostra, trecho a trecho, os
assentos livres e os passageiros confirmados:

```
[2] car-1 — Salvador → Feira de Santana → Jequié
    01/10/2026 06:00 → 10:45  •  3 assentos
    Salvador → Feira de Santana      06:00 → 07:45  R$ 25,00   3 de 3 assentos livres
    Feira de Santana → Jequié        07:45 → 10:45  R$ 35,00   3 de 3 assentos livres
```

Cancelar uma carona cancela em cascata as reservas dos passageiros, ignorando o
prazo de uma hora que vale para o passageiro: esse prazo protege o motorista
contra desistência de última hora, não o contrário (D13).

### Roteiro guiado

Para operar o sistema em duas máquinas, apresentar e medir o desempenho, siga o
[`ROTEIRO.md`](ROTEIRO.md).

## Registro de operações

O terminal do servidor mostra uma linha por conexão aberta, por operação atendida
e por conexão encerrada, com instante, endereço remoto, usuário, tipo, `id`,
resultado e duração. A senha e o restante do campo `dados` nunca aparecem (D19):

```
2026/10/01 08:15:02.431 [127.0.0.1:51234] maria    RESERVAR               id="3" → ERRO SEM_ASSENTO (0.312 ms)
```

A escrita acontece depois que a seção crítica terminou, então o terminal nunca é
escrito com o mutex do estado preso. Para desligar o registro, por exemplo ao
medir carga, use `--log-operacoes=false`.

## Executando com Docker

Montando as imagens:

```bash
docker build -f Dockerfile.servidor -t vaijunto-servidor .
docker build -f Dockerfile.cliente  -t vaijunto-cliente  .
```

A imagem do cliente traz os dois CLIs e o teste de carga já compilado em
`/bin/carga`, então a máquina que só roda clientes não precisa de Go instalado.

### Desenvolvimento em uma máquina

```bash
docker compose up --build servidor
docker compose run --rm passageiro
docker compose run --rm motorista
```

O `docker-compose.yml` serve **só ao desenvolvimento**, porque resolve
`servidor:9000` pela rede interna do Docker. No laboratório, use `docker run` em
cada máquina, conforme abaixo.

### Duas máquinas

```bash
# Máquina A — servidor
docker run --rm --name vaijunto-servidor -p 9000:9000 -e TZ=America/Bahia \
  -v "$(pwd)/dados:/dados" vaijunto-servidor \
  --usuarios /dados/usuarios.json --caronas /dados/caronas.json

# Máquina B — cliente
docker run --rm -it -e VAIJUNTO_SERVIDOR=192.168.0.10:9000 \
  vaijunto-cliente /bin/passageiro

# Máquina B — carga pela rede, com o servidor de A recém-iniciado
# e subido com --log-operacoes=false
mkdir -p resultados
docker run --rm --user "$(id -u):$(id -g)" \
  -e VAIJUNTO_CARGA=1 -e VAIJUNTO_CARGA_ENDERECO=192.168.0.10:9000 \
  -e VAIJUNTO_CARGA_ROTULO=rede \
  -v "$(pwd)/resultados:/carga/resultados" -w /carga/testes \
  vaijunto-cliente /bin/carga -test.run '^TestCarga$' -test.v
```

Quatro pontos que costumam consumir tempo:

- **O servidor escuta em `0.0.0.0`, nunca em `127.0.0.1`**, ou o mapeamento de
  porta não recebe nada de fora do contêiner.
- **Os clientes precisam de `-it`**, porque leem do terminal. Sem isso, o
  processo morre ao ler EOF do stdin.
- **`-e TZ=America/Bahia`** alinha o horário das linhas sem milissegundos (boot e
  erros de conexão) com o das linhas de operação. O domínio não depende de `TZ`:
  o fuso das cidades é uma propriedade dele (`PROJETO.md`, §10.1).
- **`--name`** permite salvar o registro com `docker logs vaijunto-servidor` antes
  de reiniciar; com `--rm`, o Ctrl+C leva o registro junto.

Contêineres em máquinas diferentes não se enxergam pela rede *bridge* do Docker.
A solução é publicar a porta do servidor no host com `-p 9000:9000` e o cliente
usar o IP da máquina A (`PROJETO.md`, §10.2).

## Testes

```bash
go vet ./...
go test -race ./...                                  # unidade e integração
go test -race -count=20 ./testes -run '^TestT[0-9]'  # concorrência (T1 a T8)
VAIJUNTO_T4_DURACAO=30s go test -race ./testes -run '^TestT4'   # T4 completo, 30 s
```

Carga, fora da suíte normal:

```bash
VAIJUNTO_CARGA=1 VAIJUNTO_CARGA_ROTULO=processo \
  go test ./testes -run '^TestCarga$' -v

VAIJUNTO_CARGA=1 VAIJUNTO_CARGA_ROTULO=rede \
  VAIJUNTO_CARGA_ENDERECO=192.168.0.10:9000 \
  go test ./testes -run '^TestCarga$' -v
```

Sem `VAIJUNTO_CARGA_ENDERECO`, o teste sobe um servidor no próprio processo; com
ele, mede um servidor já no ar. A curva sai no log e em
`resultados/carga-<rotulo>-<instante>.csv`.

Duas regras de verificação, do `PROJETO.md` §8.2 e §8.3:

- **Concorrência sempre com `-race` e `-count` alto.** O detector encontra acesso
  concorrente não sincronizado mesmo quando o teste passa por sorte de
  escalonamento, e uma corrida que aparece uma vez em vinte execuções é normal.
  Rodar sem `-race` não vale como evidência.
- **Carga sempre sem `-race`.** O detector multiplica o custo de cada acesso à
  memória, e o número medido seria o dele.

Os oito cenários T1 a T8 e as seis invariantes que cada um confere ao final estão
no `PROJETO.md`, §8.1 e §8.2. As invariantes são verificadas **pelo protocolo**,
sem operação administrativa: o teste valida o sistema pela mesma interface que os
usuários reais usam.

## Usuários de teste

| Usuário | Senha | Perfil |
|---|---|---|
| `joao`, `carlos`, `ana` | `1234` | MOTORISTA |
| `maria`, `pedro`, `lucia` | `abcd` | PASSAGEIRO |
| `teste01` … `teste50` | `teste` | PASSAGEIRO |

Os 50 usuários genéricos existem para o teste de carga e para os cenários T1 e
T8, que precisam autenticar 50 conexões distintas. As senhas ficam em texto claro
em `dados/usuarios.json`, o que é deliberado e está registrado como trabalho
futuro (`PROJETO.md`, §12).

## Cenário de demonstração

Buscar **Salvador → Vitória da Conquista em 01/10/2026**. A busca devolve
exatamente quatro itinerários: uma carona direta, duas baldeações com preços
diferentes (uma delas é o exemplo do enunciado, combinando dois motoristas) e um
itinerário de três pernas com três motoristas. Eles saem ordenados por menos
baldeações, depois por preço, depois por chegada (D16).

O `dados/caronas.json` também traz três **controles negativos**, que cumprem
todas as regras da busca exceto uma:

| Nunca pode aparecer | Regra que o recusa |
|---|---|
| `car-1` até Jequié (10:45) + `car-6` (11:00) | `MARGEM_BALDEACAO`: folga de só 15 min |
| `car-7`, de 02/10 | Filtro de data da primeira perna; e 24 h de espera como conexão |
| `car-1` + `car-8` + `car-5`, voltando a Salvador | Cidade repetida |

Se um deles aparecer no resultado, a regra que ele isola está quebrada. O cenário
completo, a derivação da ordem e o resultado esperado estão no `PROJETO.md`,
§9.2; a demonstração ao vivo está no [`ROTEIRO.md`](ROTEIRO.md).

## Estrutura do repositório

```
cmd/servidor         binário do servidor central
cmd/motorista        CLI do motorista
cmd/passageiro       CLI do passageiro
internal/protocolo   envelope, structs de mensagem, códigos de erro, enquadramento
internal/dominio     cidades atendidas, caronas, reservas, estado e mutex
internal/servidor    listener, sessão, roteador, tradução de erros
internal/cliente     conexão e terminal reaproveitados pelos dois CLIs
testes               integração, concorrência (T1 a T8) e carga
dados                usuarios.json, caronas.json
docs                 apresentacao.html
resultados           CSVs das rodadas de carga
```

Nenhum pacote de `internal/` importa `cmd/`. `internal/dominio` não importa
`internal/servidor` nem `internal/protocolo`: **o domínio não conhece a rede**.
Os erros de domínio são sentinelas tipados, e a tradução deles para os códigos do
`PROTOCOL.md` fica numa tabela única em `internal/servidor` (`PROJETO.md`, §5.3).
