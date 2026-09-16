# Protocolo de aplicação VAIJUNTO — v1

Especificação do protocolo de aplicação entre clientes e servidor central.
Este documento é o contrato: um cliente escrito em qualquer linguagem que o
respeite interopera com o servidor.

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
| Identificadores | String opaca gerada pelo servidor | `"car-3f2a"`, `"res-91c"` |

Cidades são comparadas por igualdade exata de string com a grafia canônica
da lista de cidades atendidas (seção 3.1). O servidor não normaliza nem aceita
variações de grafia — os clientes oficiais escolhem a cidade em um menu
enumerado, não digitam o nome, então a string que chega ao servidor já é sempre
a canônica. Qualquer outra string responde `CIDADE_DESCONHECIDA`.

### 3.1 Cidades atendidas

Conjunto fixo do sistema:

- Salvador
- Feira de Santana
- Jequié
- Vitória da Conquista

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
paradas informadas, no mesmo formato de `LISTAR_MINHAS_CARONAS`.

```json
{"id":"2","status":"OK","dados":{
  "carona_id":"car-3f2a",
  "rota":["Salvador","Feira de Santana","Jequié","Vitória da Conquista"],
  "horarios":["2026-09-15T08:00:00-03:00","2026-09-15T10:15:00-03:00",
              "2026-09-15T13:40:00-03:00","2026-09-15T16:00:00-03:00"]
}}
```

Erros: `PERFIL_INCORRETO`, `CIDADE_DESCONHECIDA`, `ROTA_INVALIDA`,
`PARTIDA_INVALIDA`, `CAMPO_INVALIDO`.

### 5.5 `LISTAR_MINHAS_CARONAS`

Requisição: `{"incluir_canceladas": false}`

```json
{"id":"3","status":"OK","dados":{"caronas":[{
  "carona_id":"car-3f2a",
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

Ordenação: caronas não canceladas primeiro, depois por partida. Máximo de **50**
caronas, cortadas depois da ordenação.

Erros: `PERFIL_INCORRETO`, `CAMPO_INVALIDO`.

### 5.6 `DETALHAR_CARONA`

Atende RF04: passageiros confirmados por trecho.

Requisição: `{"carona_id":"car-3f2a"}`

```json
{"id":"4","status":"OK","dados":{"carona_id":"car-3f2a","trechos":[
  {"indice":0,"origem":"Salvador","destino":"Feira de Santana","livres":2,
   "passageiros":[{"reserva_id":"res-91c","usuario":"maria","nome":"Maria Souza"}]},
  {"indice":1,"origem":"Feira de Santana","destino":"Jequié","livres":3,"passageiros":[]}
]}}
```

Erros: `CARONA_NAO_ENCONTRADA`, `NAO_E_DONO`, `PERFIL_INCORRETO`, `CAMPO_INVALIDO`.

### 5.7 `CANCELAR_CARONA`

Requisição: `{"carona_id":"car-3f2a"}`

Permitido até o instante de partida da carona.

Efeito, tudo dentro de uma única seção crítica: marca a carona como cancelada;
para cada reserva ativa que use qualquer trecho dela, marca a reserva como
cancelada e devolve os assentos de **todos** os seus trechos, inclusive os de
outras caronas.

O cancelamento em cascata **ignora** o prazo de uma hora do passageiro: aquele
prazo protege o motorista, não o contrário.

```json
{"id":"5","status":"OK","dados":{"carona_id":"car-3f2a","reservas_canceladas":2}}
```

Erros: `CARONA_NAO_ENCONTRADA`, `NAO_E_DONO`, `CARONA_CANCELADA`,
`PRAZO_CANCELAMENTO_EXPIRADO`, `PERFIL_INCORRETO`, `CAMPO_INVALIDO`.

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

Resposta, com um dos itinerários do cenário do `PROJETO.md` (seção 9.2); os
demais foram omitidos:

```json
{"id":"6","status":"OK","dados":{"itinerarios":[{
  "preco_total_centavos":10000,
  "partida":"2026-10-01T06:00:00-03:00",
  "chegada":"2026-10-01T14:30:00-03:00",
  "baldeacoes":1,
  "trechos":[
    {"carona_id":"car-1","motorista":"João Silva","de":0,"ate":2,
     "origem":"Salvador","destino":"Jequié",
     "partida":"2026-10-01T06:00:00-03:00","chegada":"2026-10-01T10:45:00-03:00",
     "preco_centavos":6000},
    {"carona_id":"car-2","motorista":"Carlos Lima","de":0,"ate":1,
     "origem":"Jequié","destino":"Vitória da Conquista",
     "partida":"2026-10-01T12:15:00-03:00","chegada":"2026-10-01T14:30:00-03:00",
     "preco_centavos":4000}
  ]
}]}}
```

Cada elemento de `trechos` é um segmento contíguo dentro de uma única carona, dos
índices `de` até `ate` da rota daquela carona, consumindo os trechos `de`,
`de+1`, …, `ate-1`.

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
  {"carona_id":"car-1","de":0,"ate":2},
  {"carona_id":"car-2","de":0,"ate":1}
]}}
```

Algoritmo do servidor, integralmente dentro de uma seção crítica:

1. Validar formato: lista não vazia, toda carona existe e não está cancelada,
   `de < ate`, índices dentro da rota, sem carona repetida.
2. Validar encadeamento: a cidade de chegada de cada perna é a de partida da
   seguinte, a partida da seguinte ocorre no mínimo **30 minutos** e no máximo
   **12 horas** após a chegada da anterior, e nenhuma cidade se repete no
   itinerário, contando as cidades intermediárias de cada perna.
3. Validar disponibilidade: para todo trecho `t` em `[de, ate)` de cada carona,
   `livres[t] >= 1`.
4. Validar sobreposição: o intervalo `[partida, chegada]` do novo itinerário não
   intersecta o de nenhuma reserva ativa do mesmo passageiro.
5. Se qualquer passo falhar, responder erro **sem alterar nada**.
6. Se tudo passar, decrementar todos os `livres` envolvidos, criar a reserva e
   responder `OK`.

A separação entre os passos 1–4 (que só verificam) e o passo 6 (que só escreve) é
o que garante a atomicidade. Nenhuma escrita ocorre antes de toda a validação
passar, então não existe estado parcial a desfazer.

```json
{"id":"7","status":"OK","dados":{"reserva_id":"res-91c","preco_total_centavos":10000}}
```

Erro por indisponibilidade, com detalhe suficiente para o CLI explicar:

```json
{"id":"7","status":"ERRO","codigo":"SEM_ASSENTO",
 "mensagem":"Assento esgotado no trecho Salvador → Feira de Santana.",
 "dados":{"carona_id":"car-1","indice_trecho":0}}
```

Erro por sobreposição:

```json
{"id":"7","status":"ERRO","codigo":"CONFLITO_HORARIO",
 "mensagem":"Você já tem a reserva res-91c nesse período.",
 "dados":{"reserva_id":"res-91c"}}
```

Outros erros: `CARONA_NAO_ENCONTRADA`, `CARONA_CANCELADA`,
`ITINERARIO_INVALIDO`, `CAMPO_INVALIDO`, `PERFIL_INCORRETO`.

### 5.10 `LISTAR_MINHAS_RESERVAS`

Requisição: `{"incluir_canceladas": false}`

```json
{"id":"8","status":"OK","dados":{"reservas":[{
  "reserva_id":"res-91c","ativa":true,
  "criada_em":"2026-08-27T14:02:11-03:00",
  "preco_total_centavos":10000,
  "partida":"2026-10-01T06:00:00-03:00",
  "chegada":"2026-10-01T14:30:00-03:00",
  "trechos":[
    {"carona_id":"car-1","motorista":"João Silva",
     "origem":"Salvador","destino":"Jequié",
     "partida":"2026-10-01T06:00:00-03:00","chegada":"2026-10-01T10:45:00-03:00",
     "preco_centavos":6000},
    {"carona_id":"car-2","motorista":"Carlos Lima",
     "origem":"Jequié","destino":"Vitória da Conquista",
     "partida":"2026-10-01T12:15:00-03:00","chegada":"2026-10-01T14:30:00-03:00",
     "preco_centavos":4000}
  ]
}]}}
```

Ordenação: reservas ativas primeiro, depois por partida. Máximo de **50**
reservas, cortadas depois da ordenação.

Erros: `PERFIL_INCORRETO`, `CAMPO_INVALIDO`.

### 5.11 `CANCELAR_RESERVA`

Requisição: `{"reserva_id":"res-91c"}`

Permitido até **1 hora antes** da partida do primeiro trecho da reserva.

Efeito: marca a reserva como inativa e devolve um assento a cada trecho que ela
consumia, em seção crítica única.

```json
{"id":"9","status":"OK","dados":{"reserva_id":"res-91c"}}
```

```json
{"id":"9","status":"ERRO","codigo":"PRAZO_CANCELAMENTO_EXPIRADO",
 "mensagem":"Cancelamento permitido até 1h antes da partida.",
 "dados":{"partida":"2026-10-01T06:00:00-03:00"}}
```

Outros erros: `RESERVA_NAO_ENCONTRADA`, `NAO_E_DONO`, `RESERVA_JA_CANCELADA`,
`PERFIL_INCORRETO`, `CAMPO_INVALIDO`.

---

## 6. Códigos de erro

| Código | Significado |
|---|---|
| `JSON_INVALIDO` | Linha não decodifica como JSON |
| `ENVELOPE_INVALIDO` | `id` ou `tipo` ausente, vazio ou que não é string, ou `dados` que não é objeto |
| `TIPO_DESCONHECIDO` | Operação não existe |
| `CAMPO_INVALIDO` | Campo ausente, com tipo errado ou fora de faixa |
| `NAO_AUTENTICADO` | Operação exige login |
| `JA_AUTENTICADO` | `LOGIN` em conexão já autenticada |
| `CREDENCIAIS_INVALIDAS` | Usuário ou senha incorretos |
| `PERFIL_INCORRETO` | Perfil não permite a operação |
| `CIDADE_DESCONHECIDA` | Cidade fora das cidades atendidas |
| `ROTA_INVALIDA` | Menos de duas paradas, cidade repetida, horários não estritamente crescentes, número de preços incompatível, ou origem igual ao destino na busca |
| `PARTIDA_INVALIDA` | Horário de parada malformado, ou primeira parada no passado |
| `CARONA_NAO_ENCONTRADA` | `carona_id` inexistente |
| `CARONA_CANCELADA` | Carona já cancelada |
| `NAO_E_DONO` | Recurso pertence a outro usuário |
| `ITINERARIO_INVALIDO` | Trechos não encadeiam no espaço ou no tempo, ou passam duas vezes pela mesma cidade |
| `SEM_ASSENTO` | Algum trecho sem disponibilidade |
| `CONFLITO_HORARIO` | Passageiro já tem reserva ativa no período |
| `RESERVA_NAO_ENCONTRADA` | `reserva_id` inexistente |
| `RESERVA_JA_CANCELADA` | Reserva já inativa |
| `PRAZO_CANCELAMENTO_EXPIRADO` | Fora do prazo permitido para cancelar |
| `ERRO_INTERNO` | Falha inesperada |

---

## 7. Exemplo de sessão completa

```
→ {"id":"1","tipo":"LOGIN","dados":{"usuario":"maria","senha":"abcd"}}
← {"id":"1","status":"OK","dados":{"usuario":"maria","nome":"Maria Souza","perfil":"PASSAGEIRO"}}

→ {"id":"2","tipo":"BUSCAR_ITINERARIOS","dados":{"origem":"Salvador","destino":"Vitória da Conquista","data":"2026-10-01"}}
← {"id":"2","status":"OK","dados":{"itinerarios":[ ... 4 opções ... ]}}

→ {"id":"3","tipo":"RESERVAR","dados":{"trechos":[{"carona_id":"car-1","de":0,"ate":1},{"carona_id":"car-4","de":0,"ate":1},{"carona_id":"car-2","de":0,"ate":1}]}}
← {"id":"3","status":"ERRO","codigo":"SEM_ASSENTO","mensagem":"...","dados":{"carona_id":"car-4","indice_trecho":0}}

→ {"id":"4","tipo":"RESERVAR","dados":{"trechos":[{"carona_id":"car-1","de":0,"ate":2},{"carona_id":"car-2","de":0,"ate":1}]}}
← {"id":"4","status":"OK","dados":{"reserva_id":"res-91c","preco_total_centavos":10000}}

→ {"id":"5","tipo":"LOGOUT","dados":{}}
← {"id":"5","status":"OK","dados":{}}
```

As caronas são as do cenário do `PROJETO.md` (seção 9.2). A requisição 3
falhando e a 4 tendo sucesso demonstra o comportamento exigido: o passageiro
perdeu a disputa pelo assento único de `car-4`, nada foi reservado pela metade —
nem o trecho de `car-1`, que tinha assento sobrando —, e ele escolheu outro
itinerário.