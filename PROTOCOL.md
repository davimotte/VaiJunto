# Protocolo de aplicação VAIJUNTO — v2

Especificação do protocolo de aplicação entre clientes e servidor central.
Este documento é o contrato: um cliente escrito em qualquer linguagem que o
respeite interopera com o servidor.

## Índice

1. [Transporte e enquadramento](#1-transporte-e-enquadramento)
2. [Envelope](#2-envelope)
3. [Convenções de tipos](#3-convenções-de-tipos)
4. [Fluxo de conexão](#4-fluxo-de-conexão)
5. [Operações](#5-operações) — [`PING`](#51-ping) · [`LOGIN`](#52-login) · [`LOGOUT`](#53-logout) · [`PUBLICAR_CARONA`](#54-publicar_carona) · [`LISTAR_MINHAS_CARONAS`](#55-listar_minhas_caronas) · [`DETALHAR_CARONA`](#56-detalhar_carona) · [`CANCELAR_CARONA`](#57-cancelar_carona) · [`BUSCAR_ITINERARIOS`](#58-buscar_itinerarios) · [`RESERVAR`](#59-reservar) · [`LISTAR_MINHAS_RESERVAS`](#510-listar_minhas_reservas) · [`CANCELAR_RESERVA`](#511-cancelar_reserva)
6. [Códigos de erro](#6-códigos-de-erro)
7. [Exemplo de sessão completa](#7-exemplo-de-sessão-completa)

---

## 1. Transporte e enquadramento

- Transporte: TCP. Porta padrão **9000**.
- Cada mensagem é um objeto JSON serializado em UTF-8, em **uma única linha**,
  terminada por `\n`.
- Conexão **persistente**: o cliente abre uma conexão, executa várias operações e
  a encerra ao final.
- Modelo estritamente **requisição/resposta**. O servidor nunca envia mensagem
  não solicitada.
- Tamanho máximo de uma linha: **64 KB**. Linha maior é descartada e a conexão é
  encerrada, sem resposta.
- Uma mensagem só é considerada completa quando o `\n` é recebido. Bytes
  pendentes sem terminador no momento do EOF são **descartados sem resposta**. O
  receptor não deve tentar processar uma linha incompleta.
- O cliente precisa ler cada resposta em até **10 s**. Se o servidor não
  conseguir entregá-la nesse prazo, a conexão é encerrada.
- Linha vazia é ignorada silenciosamente.
- Linha que não decodifica como JSON válido gera resposta `JSON_INVALIDO` e a
  conexão permanece aberta.

**Não há prazo de leitura no servidor.** Uma conexão pode ficar ociosa
indefinidamente entre duas requisições: os clientes são interativos, e derrubar
a conexão por ociosidade obrigaria a um novo `LOGIN`. Um cliente que some sem
fechar a conexão é detectado pelo *keep-alive* TCP.

Os clientes oficiais aplicam dois prazos próprios, que são política de cliente e
não exigência do contrato: **30 s** para receber a resposta de cada requisição,
renovado a cada uma, e **10 s** para estabelecer a conexão. Ao estourar o prazo
da resposta, o cliente **descarta a conexão** em vez de reutilizá-la — é o que
impede que uma resposta atrasada seja lida como se fosse da requisição seguinte.
Um cliente de outra implementação pode escolher outros valores, mas essa regra
de descarte vale para qualquer um: o modelo requisição/resposta desta seção não
tem como parear respostas fora de ordem.

O delimitador `\n` é seguro porque serializadores JSON escapam quebras de linha
dentro de strings como a sequência de dois caracteres `\` e `n`, tornando
impossível um delimitador falso vindo do conteúdo de um campo.

---

## 2. Envelope

### 2.1 Requisição

```json
{"id":"req-7","tipo":"LOGIN","dados":{}}
```

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `id` | string não vazia | sim | Identificador da requisição, gerado pelo cliente. Ecoado na resposta. |
| `tipo` | string não vazia | sim | Nome da operação, em maiúsculas. |
| `dados` | objeto | sim | Payload específico da operação. Objeto vazio quando não há campos. |

Validação estrita do envelope: `id` e `tipo` precisam ser **strings não vazias**
e `dados` precisa ser um **objeto JSON**. Campo ausente, `null`, string vazia
(`""`), ou de outro tipo (número, array, booleano) responde `ENVELOPE_INVALIDO`.

**Campos desconhecidos dentro de `dados` são ignorados,** tanto na requisição
quanto na resposta. A validação estrita para no envelope: o payload de cada
operação é decodificado pelos campos que a seção 5 define, e o que sobra é
descartado sem erro. É o que permite acrescentar um campo opcional a uma
operação sem quebrar um cliente escrito antes dele — e, do outro lado, um
cliente antigo continua lendo uma resposta que ganhou campos novos.

O receptor decodifica o envelope em dois estágios. Primeiro extrai o `id`, se ele
for uma string não vazia; só depois valida `tipo` e `dados`. Isso garante que uma
resposta de erro por envelope malformado ainda ecoe o `id` correto, o que é
necessário para que o cliente e o teste de carga consigam parear resposta e
requisição mesmo em rajada de mensagens inválidas.

### 2.2 Resposta

```json
{"id":"req-7","status":"OK","dados":{}}
```

```json
{"id":"req-7","status":"ERRO","codigo":"NAO_AUTENTICADO","mensagem":"Autentique-se antes.","dados":{}}
```

| Campo | Tipo | Presente | Descrição |
|---|---|---|---|
| `id` | string | sempre | Mesmo `id` da requisição. `""` apenas quando a linha não decodifica como JSON ou o `id` não é uma string não vazia. |
| `status` | string | sempre | `OK` ou `ERRO`. |
| `codigo` | string | só em erro | Código da tabela da seção 6. |
| `mensagem` | string | só em erro | Texto legível, para exibição no CLI. |
| `dados` | objeto | sempre | Resultado da operação, ou detalhes do erro. |

O campo `id` não é decorativo: é ele que permite ao teste de carga correlacionar
cada resposta com a requisição que a originou e medir latência individual.

---

## 3. Convenções de tipos

| Conceito | Representação | Exemplo |
|---|---|---|
| Instante | String RFC 3339 com fuso | `"2026-09-15T08:00:00-03:00"` |
| Data (busca) | String `AAAA-MM-DD` | `"2026-09-15"` |
| Dinheiro | Inteiro em centavos | `4500` (R$ 45,00) |
| Cidade | String | `"Feira de Santana"` |
| Identificadores | String opaca gerada pelo servidor, com o prefixo da empresa que a criou (`<empresa>:<tipo>-<sufixo>`) | `"vaijunto:car-3f2a1b7c"`, `"vaijunto:res-91c4e2d8"` |

Cidades são comparadas por igualdade exata de string com a grafia canônica
do catálogo de cidades atendidas (seção 3.1). O servidor não normaliza nem aceita
variações de grafia — os clientes oficiais escolhem a cidade em um menu
enumerado, não digitam o nome, então a string que chega ao servidor já é sempre
a canônica. Qualquer outra string responde `CIDADE_DESCONHECIDA`.

### 3.1 Cidades atendidas

Catálogo fixo, comum a todas as empresas do consórcio (D33). O servidor aceita
caronas entre quaisquer cidades dele:

| UF | Cidades |
|---|---|
| BA | Salvador, Feira de Santana, Jequié, Vitória da Conquista |
| ES | Vitória, Cachoeiro de Itapemirim, Marataízes |
| RJ | Rio de Janeiro, Nilópolis, Petrópolis |

O **nome é a chave**: é a string do campo `cidade` em toda mensagem, e a UF
nunca trafega no protocolo. A UF serve só à exibição nos menus dos clientes
("Vitória (ES)", "Vitória da Conquista (BA)"). "Vitória" e "Vitória da
Conquista" são cidades distintas; a comparação continua sendo por igualdade
exata de string.

O conjunto não impõe ordem nem duração de trajeto. A sequência de paradas de cada
carona e o horário de cada parada são informados pelo motorista (seção 5.4).

---

## 4. Fluxo de conexão

```
cliente                              servidor
   |-------- TCP connect ------------->|
   |-------- LOGIN ------------------->|
   |<------- OK (perfil) --------------|
   |-------- operações... ------------>|
   |<------- respostas... -------------|
   |-------- LOGOUT (opcional) ------->|
   |<------- OK -----------------------|
   |-------- TCP close --------------->|
```

Regras:

- Enquanto a conexão não estiver autenticada, apenas `LOGIN` e `PING` são
  aceitos. Qualquer outro tipo da seção 5 responde `NAO_AUTENTICADO`, e um tipo
  inexistente responde `TIPO_DESCONHECIDO`.
- `LOGIN` em conexão já autenticada responde `JA_AUTENTICADO`.

**Precedência das recusas.** Quando mais de uma regra falha, o código devolvido
é o da primeira desta ordem:

| Ordem | Regra | Código |
|---|---|---|
| 1 | O tipo existe na seção 5 | `TIPO_DESCONHECIDO` |
| 2 | `LOGIN` só em conexão não autenticada | `JA_AUTENTICADO` |
| 3 | A operação exige autenticação e a conexão a tem | `NAO_AUTENTICADO` |
| 4 | O perfil autenticado é o que a operação exige | `PERFIL_INCORRETO` |

A ordem não é arbitrária. Um tipo inexistente responde `TIPO_DESCONHECIDO` mesmo
sem login, porque "só `LOGIN` e `PING` antes de autenticar" fala do conjunto de
operações do protocolo, e uma operação que não existe não pertence a ele em
nenhum estado da conexão. E a autenticação vem antes do perfil porque responder
`PERFIL_INCORRETO` a quem sequer fez `LOGIN` diria ao cliente que ele errou o
perfil quando o que faltou foi autenticar.

A posse do recurso — as linhas "dono" da tabela da seção 5 — é verificada
**depois** de tudo isso, já dentro da operação, e responde `NAO_E_DONO`.
- Fechamento abrupto (EOF, RST, queda de rede) é tratado como desconexão normal:
  a goroutine encerra, a sessão desaparece e nenhum estado de domínio é alterado.
  Reservas e caronas persistem, porque não existe estado transitório amarrado à
  conexão.

---

## 5. Operações

| Tipo | Perfil exigido |
|---|---|
| `PING` | nenhum |
| `LOGIN` | nenhum |
| `LOGOUT` | autenticado |
| `PUBLICAR_CARONA` | MOTORISTA |
| `LISTAR_MINHAS_CARONAS` | MOTORISTA |
| `DETALHAR_CARONA` | MOTORISTA, dono |
| `CANCELAR_CARONA` | MOTORISTA, dono |
| `BUSCAR_ITINERARIOS` | PASSAGEIRO |
| `RESERVAR` | PASSAGEIRO |
| `LISTAR_MINHAS_RESERVAS` | PASSAGEIRO |
| `CANCELAR_RESERVA` | PASSAGEIRO, dono |

### 5.1 `PING`

Diagnóstico. Não requer autenticação.

Requisição `dados`: vazio.
Resposta `dados`: `{"servidor_em":"2026-09-15T10:00:00-03:00"}`

### 5.2 `LOGIN`

```json
{"id":"1","tipo":"LOGIN","dados":{"usuario":"joao","senha":"1234"}}
```

```json
{"id":"1","status":"OK","dados":{"usuario":"joao","nome":"João Silva","perfil":"MOTORISTA"}}
```

`perfil` ∈ {`MOTORISTA`, `PASSAGEIRO`}.

Erros: `CREDENCIAIS_INVALIDAS`, `JA_AUTENTICADO`, `CAMPO_INVALIDO`.

### 5.3 `LOGOUT`

`dados` vazio na requisição e na resposta. Marca a conexão como não autenticada
sem fechá-la.

### 5.4 `PUBLICAR_CARONA`

O motorista informa cada parada, na ordem em que o carro passa por ela, com o
horário da passagem. Há um único horário por parada: o instante de chegada a uma
cidade é também o de partida dela.

```json
{"id":"2","tipo":"PUBLICAR_CARONA","dados":{
  "paradas":[
    {"cidade":"Salvador",             "horario":"2026-09-15T08:00:00-03:00"},
    {"cidade":"Feira de Santana",     "horario":"2026-09-15T10:15:00-03:00"},
    {"cidade":"Jequié",               "horario":"2026-09-15T13:40:00-03:00"},
    {"cidade":"Vitória da Conquista", "horario":"2026-09-15T16:00:00-03:00"}
  ],
  "assentos":3,
  "precos_centavos":[3000,4500,4000]
}}
```

`precos_centavos[t]` é o preço do trecho entre `paradas[t]` e `paradas[t+1]`.

Validações, com o código de cada recusa:

| Regra | Código |
|---|---|
| Todo campo presente e com o tipo correto | `CAMPO_INVALIDO` |
| `assentos >= 1`; cada preço `>= 0` | `CAMPO_INVALIDO` |
| Toda `cidade` pertence às cidades atendidas (seção 3.1) | `CIDADE_DESCONHECIDA` |
| Ao menos duas paradas | `ROTA_INVALIDA` |
| Nenhuma cidade repetida | `ROTA_INVALIDA` |
| Horários estritamente crescentes (igual ao anterior também é recusado) | `ROTA_INVALIDA` |
| `len(precos_centavos) == len(paradas) − 1` | `ROTA_INVALIDA` |
| Todo `horario` em RFC 3339 com fuso | `PARTIDA_INVALIDA` |
| Horário da primeira parada no futuro | `PARTIDA_INVALIDA` |

O servidor não calcula rota nem horário: a resposta devolve exatamente as
paradas informadas, como confirmação do que foi guardado, mais o identificador
gerado. É uma resposta deliberadamente enxuta — `assentos`, `cancelada` e os
`trechos` com `livres` vêm de `LISTAR_MINHAS_CARONAS` (seção 5.5), porque
acabaram de ser informados pelo próprio cliente ou são conhecidos por
construção: numa carona recém-publicada, todo trecho tem `livres == assentos` e
`cancelada` é sempre `false`.

```json
{"id":"2","status":"OK","dados":{
  "carona_id":"vaijunto:car-3f2a1b7c",
  "rota":["Salvador","Feira de Santana","Jequié","Vitória da Conquista"],
  "horarios":["2026-09-15T08:00:00-03:00","2026-09-15T10:15:00-03:00",
              "2026-09-15T13:40:00-03:00","2026-09-15T16:00:00-03:00"]
}}
```

Erros: `PERFIL_INCORRETO`, `CIDADE_DESCONHECIDA`, `ROTA_INVALIDA`,
`PARTIDA_INVALIDA`, `CAMPO_INVALIDO`.

### 5.5 `LISTAR_MINHAS_CARONAS`

Requisição: `{"incluir_canceladas": false}`. O campo é opcional e vale `false`
quando ausente, então `dados` vazio lista só as caronas de pé.

```json
{"id":"3","status":"OK","dados":{"caronas":[{
  "carona_id":"vaijunto:car-3f2a1b7c",
  "rota":["Salvador","Feira de Santana","Jequié"],
  "horarios":["2026-09-15T06:00:00-03:00","2026-09-15T08:00:00-03:00","2026-09-15T11:00:00-03:00"],
  "assentos":3,
  "cancelada":false,
  "trechos":[
    {"indice":0,"origem":"Salvador","destino":"Feira de Santana","preco_centavos":3000,"livres":2},
    {"indice":1,"origem":"Feira de Santana","destino":"Jequié","preco_centavos":4500,"livres":3}
  ]
}]}}
```

Ordenação, nesta ordem: caronas não canceladas primeiro, depois por partida,
depois por `carona_id`. Máximo de **50** caronas, cortadas depois da ordenação.

O terceiro critério existe para que a ordem seja **total**: duas chamadas
idênticas devolvem sempre a mesma lista, e o corte de 50 sempre descarta os
mesmos itens. Sem ele, caronas com a mesma partida sairiam em ordem
imprevisível.

Erros: `PERFIL_INCORRETO`, `CAMPO_INVALIDO`.

### 5.6 `DETALHAR_CARONA`

Atende RF04: passageiros confirmados por trecho.

Requisição: `{"carona_id":"vaijunto:car-3f2a1b7c"}`

```json
{"id":"4","status":"OK","dados":{"carona_id":"vaijunto:car-3f2a1b7c","trechos":[
  {"indice":0,"origem":"Salvador","destino":"Feira de Santana","livres":2,
   "passageiros":[{"reserva_id":"vaijunto:res-91c4e2d8","usuario":"maria","nome":"Maria Souza"}]},
  {"indice":1,"origem":"Feira de Santana","destino":"Jequié","livres":3,"passageiros":[]}
]}}
```

Um passageiro de outra empresa aparece com `usuario` no formato
`<empresa>:<usuario>` (seção 3), em vez do nome de usuário puro — é a mesma
referência opaca que a empresa dona da reserva remota guarda (D32). O cliente
só usa esse prefixo para rotular a linha como "de outra empresa"; nunca para
decidir nada.

Erros: `CARONA_NAO_ENCONTRADA`, `NAO_E_DONO`, `PERFIL_INCORRETO`, `CAMPO_INVALIDO`.

### 5.7 `CANCELAR_CARONA`

Requisição: `{"carona_id":"vaijunto:car-3f2a1b7c"}`

Permitido até o instante de partida da carona.

Efeito, tudo dentro de uma única seção crítica: marca a carona como cancelada
e cancela a perna local de cada transação atingida, devolvendo os assentos de
**todos** os seus trechos desta empresa, inclusive os de outras caronas
(D29). Se esta empresa coordena a reserva, ela é decidida cancelada aqui
mesmo; se não, um aviso é enfileirado para o coordenador remoto decidir
(D39, seção 10.2 do PROJETO_P2.md) — a entrega é assíncrona e confiável, e a
reserva pode continuar aparecendo ativa no coordenador por um instante, até o
aviso chegar.

O cancelamento em cascata **ignora** o prazo de uma hora do passageiro: aquele
prazo protege o motorista, não o contrário.

```json
{"id":"5","status":"OK","dados":{"carona_id":"vaijunto:car-3f2a1b7c","reservas_canceladas":2}}
```

`reservas_canceladas` conta as transações com perna local cancelada **nesta
chamada** — o estrago imediato que o motorista pode observar —, não as
reservas já fechadas em coordenadores remotos, que ainda dependem do aviso
chegar.

Se alguma transação usando a carona está em preparação (`PREPARADO`), o
cancelamento é recusado por inteiro, sem cancelar nada: cancelar não pode
abortar quem já votou SIM (D39). O motorista tenta de novo em alguns
segundos, quando a transação já tiver confirmado ou abortado:

```json
{"id":"5","status":"ERRO","codigo":"CARONA_EM_TRANSACAO",
 "mensagem":"A carona tem uma reserva em preparação; tente cancelar novamente em alguns segundos."}
```

Fora do prazo, o erro traz a partida da qual o prazo foi contado, no mesmo
formato da seção 5.11:

```json
{"id":"5","status":"ERRO","codigo":"PRAZO_CANCELAMENTO_EXPIRADO",
 "mensagem":"Fora do prazo de cancelamento.",
 "dados":{"partida":"2026-10-01T06:00:00-03:00"}}
```

Erros: `CARONA_NAO_ENCONTRADA`, `NAO_E_DONO`, `CARONA_CANCELADA`,
`CARONA_EM_TRANSACAO`, `PRAZO_CANCELAMENTO_EXPIRADO`, `PERFIL_INCORRETO`,
`CAMPO_INVALIDO`.

### 5.8 `BUSCAR_ITINERARIOS`

```json
{"id":"6","tipo":"BUSCAR_ITINERARIOS","dados":{
  "origem":"Salvador","destino":"Vitória da Conquista","data":"2026-10-01"
}}
```

`data` filtra pelo horário de partida do **primeiro** trecho do itinerário, o que
permite baldeação atravessando a meia-noite. O dia é lido no fuso das cidades
atendidas (`America/Bahia`, −03:00), e não no fuso da máquina do servidor: uma
partida às `2026-10-01T22:00:00-03:00` pertence à busca de `2026-10-01`.

A busca é **federada**: o servidor consulta em paralelo as demais empresas do
consórcio e devolve itinerários que combinam caronas de várias delas
(`PROJETO_P2.md`, seção 6). O passageiro continua falando só com a empresa a que
está conectado.

Resposta, com o itinerário interestadual da seção 12.2 do `PROJETO_P2.md`: três
pernas, uma em cada empresa, por R$ 430,00.

```json
{"id":"6","status":"OK","dados":{
  "parcial":false,
  "empresas_indisponiveis":[],
  "itinerarios":[{
    "preco_total_centavos":43000,
    "partida":"2026-11-05T05:00:00-03:00",
    "chegada":"2026-11-06T14:30:00-03:00",
    "baldeacoes":2,
    "trechos":[
      {"carona_id":"vaijunto:car-1","empresa":"vaijunto",
       "motorista":"João Silva","de":0,"ate":2,
       "origem":"Salvador","destino":"Vitória da Conquista",
       "partida":"2026-11-05T05:00:00-03:00","chegada":"2026-11-05T12:30:00-03:00",
       "preco_centavos":9500},
      {"carona_id":"rotacapixaba:car-1","empresa":"rotacapixaba",
       "motorista":"Rafael Mendonça Pires","de":0,"ate":2,
       "origem":"Vitória da Conquista","destino":"Cachoeiro de Itapemirim",
       "partida":"2026-11-05T14:00:00-03:00","chegada":"2026-11-06T06:30:00-03:00",
       "preco_centavos":21500},
      {"carona_id":"rotafluminense:car-1","empresa":"rotafluminense",
       "motorista":"Marcos Antunes Barreto","de":0,"ate":2,
       "origem":"Cachoeiro de Itapemirim","destino":"Petrópolis",
       "partida":"2026-11-06T08:00:00-03:00","chegada":"2026-11-06T14:30:00-03:00",
       "preco_centavos":12000}
    ]
  }]
}}
```

Cada elemento de `trechos` é um segmento contíguo dentro de uma única carona, dos
índices `de` até `ate` da rota daquela carona, consumindo os trechos `de`,
`de+1`, …, `ate-1`.

`empresa` é a dona da carona daquele trecho, e é sempre o prefixo do
`carona_id` (D27). Vem explícito para que o cliente possa dizer ao passageiro
com quantas empresas ele vai viajar sem interpretar o identificador.

#### Resultado parcial

`parcial` é `true` quando ao menos uma empresa do consórcio não respondeu dentro
do prazo, e `empresas_indisponiveis` nomeia essas empresas, em ordem alfabética.
A lista vem sempre presente, vazia quando todas responderam, e nunca como
`null`.

Resultado parcial **é resposta `OK`, não erro**: os itinerários devolvidos são
válidos, e o que falta é a informação de que podem existir outros. Tratar a
queda de uma empresa como falha da busca faria uma empresa fora do ar derrubar a
consulta de todas as outras (D26, D30).

```json
{"id":"6","status":"OK","dados":{
  "parcial":true,
  "empresas_indisponiveis":["rotacapixaba"],
  "itinerarios":[]
}}
```

Uma empresa que respondeu, mas mandou uma carona malformada ou com cidade fora
do catálogo, **não** entra na lista: aquela carona é descartada e o resto do que
ela mandou continua valendo. A lista é de empresas de quem não se sabe nada.

Um itinerário nunca passa duas vezes pela mesma cidade, contando as cidades
intermediárias de cada trecho.

`itinerarios` vazio é resposta `OK`, não erro.

Ordenação: menos baldeações primeiro, empate por menor preço total, depois por
chegada mais cedo. Máximo de **10** resultados, cortados depois da ordenação: o
limite nunca descarta um itinerário com menos baldeações enquanto mantém um com
mais.

**A resposta desta operação não reserva nem bloqueia nada.** Os valores refletem
o instante da consulta e podem estar desatualizados quando o passageiro
confirmar. É essa escolha que garante que nenhum assento fique permanentemente
bloqueado por uma reserva nunca concluída.

Erros: `PERFIL_INCORRETO`, `CIDADE_DESCONHECIDA`, `ROTA_INVALIDA`, `CAMPO_INVALIDO`.

### 5.9 `RESERVAR`

O cliente devolve os trechos do itinerário escolhido, na ordem:

```json
{"id":"7","tipo":"RESERVAR","dados":{"trechos":[
  {"carona_id":"vaijunto:car-1","de":0,"ate":2},
  {"carona_id":"vaijunto:car-2","de":0,"ate":1}
]}}
```

As caronas podem ser de empresas diferentes. O servidor que recebe o
`RESERVAR` é o **coordenador** da transação, porque é a empresa do passageiro
(D21), e a reserva é concluída por *two-phase commit* — não há mais uma seção
crítica única que abranja o itinerário inteiro, porque os assentos estão em
servidores diferentes. O algoritmo completo está na seção 7.1 do
`PROJETO_P2.md`; em resumo:

1. Validar formato: lista não vazia, `de < ate`, sem carona repetida, e toda
   carona com prefixo de uma empresa do consórcio.
2. Recusar um segundo `RESERVAR` do mesmo passageiro enquanto o primeiro está em
   andamento, com `RESERVA_EM_ANDAMENTO` (D35).
3. Enviar `PREPARAR` em paralelo a cada empresa dona de alguma perna. Cada dona
   valida o que só ela sabe — a carona existe, não está cancelada, os índices
   cabem na rota e há assento — e, votando SIM, **desconta o assento** e grava
   o voto em disco antes de responder.
4. Sem unanimidade dentro do prazo de votação, abortar: nenhum assento fica
   reservado em nenhuma empresa, e a resposta é o erro correspondente.
5. Com todos os votos SIM, validar o encadeamento sobre os dados que as donas
   devolveram: a cidade de chegada de cada perna é a de partida da seguinte, a
   partida da seguinte ocorre no mínimo **30 minutos** e no máximo **12 horas**
   após a chegada da anterior, nenhuma cidade se repete no itinerário (contando
   as intermediárias), e o intervalo `[partida, chegada]` não intersecta o de
   nenhuma reserva ativa do mesmo passageiro.
6. Gravar a decisão de confirmar e responder `OK`. Os `CONFIRMAR` seguem para
   as donas em segundo plano.

A atomicidade não vem mais de um mutex, e sim de duas regras: nenhuma dona
confirma sem que a decisão esteja gravada, e a decisão só é tomada com todos os
votos SIM — cada um deles dado depois de o assento já estar descontado. Ou
todos os trechos valem, ou nenhum. O argumento completo está na seção 7.4 do
`PROJETO_P2.md`.

```json
{"id":"7","status":"OK","dados":{"reserva_id":"vaijunto:res-91c4e2d8","preco_total_centavos":10000}}
```

Erro por indisponibilidade, com detalhe suficiente para o CLI explicar:

```json
{"id":"7","status":"ERRO","codigo":"SEM_ASSENTO",
 "mensagem":"Assento esgotado no trecho Salvador → Feira de Santana.",
 "dados":{"carona_id":"vaijunto:car-1","indice_trecho":0}}
```

Erro por sobreposição:

```json
{"id":"7","status":"ERRO","codigo":"CONFLITO_HORARIO",
 "mensagem":"Você já tem a reserva vaijunto:res-91c4e2d8 nesse período.",
 "dados":{"reserva_id":"vaijunto:res-91c4e2d8"}}
```

Erro por reserva já em andamento do mesmo passageiro (D35):

```json
{"id":"7","status":"ERRO","codigo":"RESERVA_EM_ANDAMENTO",
 "mensagem":"Você já tem uma reserva em andamento; aguarde o resultado dela.",
 "dados":{}}
```

Erro por empresa fora do ar, quando alguma dona de um trecho não responde
dentro do prazo de votação (D30). Nenhum assento fica reservado:

```json
{"id":"7","status":"ERRO","codigo":"EMPRESA_INDISPONIVEL",
 "mensagem":"Uma das empresas do itinerário não respondeu; nenhum assento foi reservado.",
 "dados":{}}
```

Erro por disputa perdida. O assento existia, mas outra reserva ficou com ele:
na disputa pelo último assento, a transação mais nova cede à mais velha, e o
servidor refaz a reserva até três vezes antes de desistir (D36). Nenhum assento
fica reservado, e tentar de novo é legítimo:

```json
{"id":"7","status":"ERRO","codigo":"DISPUTA_PERDIDA",
 "mensagem":"O assento foi disputado por outra reserva; nenhum assento foi reservado. Tente de novo.",
 "dados":{}}
```

A diferença para `SEM_ASSENTO` é o que o passageiro pode fazer a seguir:
`SEM_ASSENTO` afirma que o trecho está cheio de assentos já vendidos, e repetir
não adianta; `DISPUTA_PERDIDA` diz que havia assento e outra reserva chegou
antes, então repetir pode dar certo.

Outros erros: `CARONA_NAO_ENCONTRADA`, `CARONA_CANCELADA`,
`ITINERARIO_INVALIDO`, `CAMPO_INVALIDO`, `PERFIL_INCORRETO`.

### 5.10 `LISTAR_MINHAS_RESERVAS`

Requisição: `{"incluir_canceladas": false}`. Como na seção 5.5, o campo é
opcional e vale `false` quando ausente.

```json
{"id":"8","status":"OK","dados":{"reservas":[{
  "reserva_id":"vaijunto:res-91c4e2d8","ativa":true,
  "criada_em":"2026-08-27T14:02:11-03:00",
  "preco_total_centavos":10000,
  "partida":"2026-10-01T06:00:00-03:00",
  "chegada":"2026-10-01T14:30:00-03:00",
  "trechos":[
    {"carona_id":"vaijunto:car-1","empresa":"vaijunto","motorista":"João Silva",
     "origem":"Salvador","destino":"Jequié",
     "partida":"2026-10-01T06:00:00-03:00","chegada":"2026-10-01T10:45:00-03:00",
     "preco_centavos":6000},
    {"carona_id":"rotacapixaba:car-2","empresa":"rotacapixaba","motorista":"Carlos Lima",
     "origem":"Jequié","destino":"Vitória da Conquista",
     "partida":"2026-10-01T12:15:00-03:00","chegada":"2026-10-01T14:30:00-03:00",
     "preco_centavos":4000}
  ]
}]}}
```

`empresa` é a dona da carona daquele trecho — igual ao campo de mesmo nome em
`BUSCAR_ITINERARIOS` (seção 5.8) —, útil para o cliente identificar o trecho
remoto se o par cair antes de o aviso assíncrono de cancelamento chegar
(seção 10.2 do `PROJETO_P2.md`).

Ordenação, nesta ordem: reservas ativas primeiro, depois por partida, depois por
`reserva_id`. Máximo de **50** reservas, cortadas depois da ordenação. O
terceiro critério torna a ordem total, pelo mesmo motivo da seção 5.5.

Erros: `PERFIL_INCORRETO`, `CAMPO_INVALIDO`.

### 5.11 `CANCELAR_RESERVA`

Requisição: `{"reserva_id":"vaijunto:res-91c4e2d8"}`

Permitido até **1 hora antes** da partida do primeiro trecho da reserva.

Efeito: marca a reserva como inativa e devolve um assento a cada trecho que ela
consumia, em seção crítica única.

```json
{"id":"9","status":"OK","dados":{"reserva_id":"vaijunto:res-91c4e2d8"}}
```

```json
{"id":"9","status":"ERRO","codigo":"PRAZO_CANCELAMENTO_EXPIRADO",
 "mensagem":"Fora do prazo de cancelamento.",
 "dados":{"partida":"2026-10-01T06:00:00-03:00"}}
```

Outros erros: `RESERVA_NAO_ENCONTRADA`, `NAO_E_DONO`, `RESERVA_JA_CANCELADA`,
`PERFIL_INCORRETO`, `CAMPO_INVALIDO`.

---

## 6. Códigos de erro

A coluna `dados` diz o que vem no campo `dados` da resposta de erro. Só três
códigos carregam conteúdo; em todos os demais, `dados` é `{}` — presente, mas
vazio, porque a seção 2.2 exige o campo em toda resposta.

| Código | Significado | `dados` |
|---|---|---|
| `JSON_INVALIDO` | Linha não decodifica como JSON | `{}` |
| `ENVELOPE_INVALIDO` | `id` ou `tipo` ausente, vazio ou que não é string, ou `dados` que não é objeto | `{}` |
| `TIPO_DESCONHECIDO` | Operação não existe | `{}` |
| `CAMPO_INVALIDO` | Campo ausente, com tipo errado ou fora de faixa | `{}` |
| `NAO_AUTENTICADO` | Operação exige login | `{}` |
| `JA_AUTENTICADO` | `LOGIN` em conexão já autenticada | `{}` |
| `CREDENCIAIS_INVALIDAS` | Usuário ou senha incorretos | `{}` |
| `PERFIL_INCORRETO` | Perfil não permite a operação | `{}` |
| `CIDADE_DESCONHECIDA` | Cidade fora das cidades atendidas | `{}` |
| `ROTA_INVALIDA` | Menos de duas paradas, cidade repetida, horários não estritamente crescentes, número de preços incompatível, ou origem igual ao destino na busca | `{}` |
| `PARTIDA_INVALIDA` | Horário de parada malformado, ou primeira parada no passado | `{}` |
| `CARONA_NAO_ENCONTRADA` | `carona_id` inexistente | `{}` |
| `CARONA_CANCELADA` | Carona já cancelada | `{}` |
| `NAO_E_DONO` | Recurso pertence a outro usuário | `{}` |
| `ITINERARIO_INVALIDO` | Trechos não encadeiam no espaço ou no tempo, ou passam duas vezes pela mesma cidade | `{}` |
| `SEM_ASSENTO` | Algum trecho sem disponibilidade | **`carona_id`, `indice_trecho`** (5.9) |
| `CONFLITO_HORARIO` | Passageiro já tem reserva ativa no período | **`reserva_id`** (5.9) |
| `RESERVA_NAO_ENCONTRADA` | `reserva_id` inexistente | `{}` |
| `RESERVA_JA_CANCELADA` | Reserva já inativa | `{}` |
| `PRAZO_CANCELAMENTO_EXPIRADO` | Fora do prazo permitido para cancelar | **`partida`** (5.7 e 5.11) |
| `RESERVA_EM_ANDAMENTO` | O passageiro já tem uma reserva em andamento (D35) | `{}` |
| `EMPRESA_INDISPONIVEL` | Uma empresa dona de um trecho não respondeu no prazo (D30) | `{}` |
| `DISPUTA_PERDIDA` | Outra reserva levou o assento disputado, nas três tentativas (D36) | `{}` |
| `CARONA_EM_TRANSACAO` | A carona tem alguma transação em `PREPARADO`; cancelar não pode abortar quem já votou SIM (D39, seção 10.2 do `PROJETO_P2.md`) | `{}` |
| `ERRO_INTERNO` | Falha inesperada | `{}` |

Os três com conteúdo são os que o cliente precisa para montar uma mensagem
acionável: qual trecho esgotou, qual reserva ocupa o período, e de que partida o
prazo foi contado. Nos demais, a `mensagem` basta.

---

## 7. Exemplo de sessão completa

```
→ {"id":"1","tipo":"LOGIN","dados":{"usuario":"maria","senha":"abcd"}}
← {"id":"1","status":"OK","dados":{"usuario":"maria","nome":"Maria Souza","perfil":"PASSAGEIRO"}}

→ {"id":"2","tipo":"BUSCAR_ITINERARIOS","dados":{"origem":"Salvador","destino":"Petrópolis","data":"2026-11-05"}}
← {"id":"2","status":"OK","dados":{"parcial":false,"empresas_indisponiveis":[],"itinerarios":[ ... o itinerário de 3 pernas da seção 5.8, por vaijunto, rotacapixaba e rotafluminense ... ]}}

→ {"id":"3","tipo":"RESERVAR","dados":{"trechos":[{"carona_id":"vaijunto:car-1","de":0,"ate":2},{"carona_id":"rotacapixaba:car-1","de":0,"ate":2},{"carona_id":"rotafluminense:car-1","de":0,"ate":2}]}}
← {"id":"3","status":"ERRO","codigo":"DISPUTA_PERDIDA","mensagem":"O assento foi disputado por outra reserva; nenhum assento foi reservado. Tente de novo.","dados":{}}

→ {"id":"4","tipo":"RESERVAR","dados":{"trechos":[{"carona_id":"vaijunto:car-1","de":0,"ate":2},{"carona_id":"rotacapixaba:car-1","de":0,"ate":2},{"carona_id":"rotafluminense:car-1","de":0,"ate":2}]}}
← {"id":"4","status":"OK","dados":{"reserva_id":"vaijunto:res-91c4e2d8","preco_total_centavos":43000}}

→ {"id":"5","tipo":"LOGOUT","dados":{}}
← {"id":"5","status":"OK","dados":{}}
```

O itinerário é o interestadual da seção 12.2 do `PROJETO_P2.md`, com pernas de
três empresas (seção 5.8). A requisição 3 falhando e a 4 tendo sucesso
demonstra o comportamento exigido pelo *two-phase commit* entre empresas
(seção 5.9): maria perdeu a disputa pelo último assento de um dos trechos,
nada ficou reservado pela metade em nenhuma das três empresas — cada dona
desfez o assento que havia descontado ao votar — e ela repetiu exatamente a
mesma requisição, que teve sucesso na segunda tentativa (D36). O
`reserva_id` sai com o prefixo `vaijunto`, a empresa do passageiro, porque é
ela que coordena a transação (D21) — mesmo quando, como aqui, só uma das três
pernas é dela.
