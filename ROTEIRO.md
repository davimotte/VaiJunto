# Roteiro de operação, apresentação e experimento

Documento único de operação do VAIJUNTO em duas máquinas. Três partes, na ordem:

| Parte | O que cobre | Quando |
|---|---|---|
| **0** | Preparar as duas máquinas: imagens, servidor, clientes, conectividade | Antes, fora do tempo |
| **1** | As 12 telas de `docs/apresentacao.html` (capa + 11 itens do Barema) | ~10 min |
| **2** | Demonstração ao vivo, sem interrupções | ~6 min |
| **3** | Experimento guiado: carga com e sem rede, e coleta para o relatório | Fora da apresentação |

As Partes 1 e 2 somam cerca de 16 minutos. A Parte 3 é o teste laboratorial do
`PROJETO.md` §8.3 e não cabe no tempo da apresentação: rode antes e leve os
resultados.

---

# Parte 0: preparação

## 0.1 Montar as imagens (com internet)

A montagem baixa as imagens base `golang:1.23-alpine` e `alpine:3.20`. Na raiz do
repositório, com as duas máquinas no mesmo commit (`git log -1 --oneline`):

```bash
# Máquina A: as duas imagens — a do cliente também roda a carga da Parte 3
docker build -f Dockerfile.servidor -t vaijunto-servidor .
docker build -f Dockerfile.cliente  -t vaijunto-cliente  .

# Máquina B: só a do cliente
docker build -f Dockerfile.cliente -t vaijunto-cliente .
```

Se o laboratório não tiver internet, monte antes e leve as imagens num pendrive:

```bash
docker save vaijunto-servidor vaijunto-cliente | gzip > vaijunto.tar.gz   # onde montou
docker load < vaijunto.tar.gz                                             # no laboratório
```

## 0.2 Subir o servidor na máquina A

Anote o IP (`hostname -I`) e suba o servidor **recém-iniciado** num terminal que
fique visível durante todo o teste:

```bash
docker run --rm --name vaijunto-servidor -p 9000:9000 -e TZ=America/Bahia \
  -v "$(pwd)/dados:/dados" vaijunto-servidor \
  --usuarios /dados/usuarios.json --caronas /dados/caronas.json
```

Esperado: `servidor: escutando em [::]:9000 (registro de operações: true)`. Se o
trecho entre parênteses não aparecer, a imagem é anterior ao registro: monte-a de
novo.

Cada operação sai assim (D19):

```
2026/10/01 08:15:02.431 [192.168.0.20:51234] maria    RESERVAR               id="3" → OK (0.312 ms)
```

Os campos são instante, endereço do cliente, usuário (`-` antes do login),
operação, `id` da requisição, resultado (`OK`, ou `ERRO` com o código) e duração.
A senha nunca aparece.

O `-e TZ=America/Bahia` só alinha o horário das linhas sem milissegundos — a de
boot e as de erro de conexão — com o das linhas de operação. O domínio não depende
de `TZ` (`PROJETO.md`, §10.1).

**Para reiniciar o servidor:** `Ctrl+C` no terminal dele e o comando acima de
novo. O estado vive só em memória, então reiniciar volta ao cenário inicial.

**Salve o registro antes de reiniciar.** O `--rm` apaga o contêiner no `Ctrl+C`, e
o `docker logs` vai junto. Em outro terminal da máquina A:

```bash
docker logs vaijunto-servidor > registro-demonstracao.txt 2>&1
```

## 0.3 Conferir a conectividade

Na máquina B, antes de abrir qualquer cliente, com `export IP_A=192.168.x.y`:

```bash
printf '{"id":"1","tipo":"PING","dados":{}}\n' | nc -q1 $IP_A 9000
```

Esperado: `"status":"OK"` e `servidor_em` terminando em `-03:00`. No terminal do
servidor, três linhas: `conexão aberta`, `-  PING  id="1" → OK` e
`conexão encerrada`. É a evidência isolada do item 2 do Barema, e separa "a rede
está errada" de "o cliente está errado" antes da apresentação.

A conectividade funciona porque o `-p 9000:9000` publica a porta do contêiner no
IP da máquina A. Redes *bridge* do Docker de máquinas diferentes não se enxergam
diretamente (`PROJETO.md`, §10.2).

## 0.4 Abrir os clientes e fazer login

`export IP_A=…` em cada terminal:

| Máquina | Terminal | Cliente | Usuário |
|---|---|---|---|
| A | servidor | — | — |
| A | PA | passageiro | `pedro` / `abcd` |
| B | PB | passageiro | `lucia` / `abcd` |
| B | MB | motorista | `joao` / `1234` (troca para `ana` só no D7) |

```bash
docker run --rm -it -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/passageiro                     # PA (em A)
docker run --rm -it --name cliente-pb -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/passageiro   # PB (em B)
docker run --rm -it -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/motorista                      # MB (em B)
```

O `--name cliente-pb` existe só para o D6: se o Ctrl+C não derrubar o cliente,
`docker kill cliente-pb` em outro terminal da máquina B mata o processo do mesmo
jeito, e o sistema operacional fecha o socket igual.

Na máquina A, `IP_A` é o IP da própria máquina: o cliente de A também passa pela
porta publicada. Deixar as sessões abertas é seguro: o servidor não tem prazo de
leitura, e o prazo do cliente só conta durante uma requisição (D17).

**Por que esses usuários.**

- **joao** é dono da `car-1`, que está em todos os itinerários com baldeação:
  publica, detalha passageiros por trecho e cancela em cascata sem trocar de
  usuário.
- **pedro** e **lucia** começam sem reservas, então disputam o mesmo itinerário
  sem esbarrar em `CONFLITO_HORARIO`.

## 0.5 Conferências finais

- `docs/apresentacao.html` aberto na capa.
- Relógio antes de **01/10/2026 às 05:00**. É o prazo do passageiro para cancelar
  a reserva do D5: uma hora antes da partida das 06:00 (D13). O motorista pode
  cancelar a `car-1` até as 06:00, a própria partida.
- Combinado quem aperta o outro Enter no D3 (um colega ou o professor).
- No ensaio, confirmado que **Ctrl+C** encerra o cliente passageiro no contêiner.
  Se não encerrar, o D6 usa `docker kill cliente-pb`.

---

# Parte 1: apresentação do HTML (cerca de 10 min)

Navegação: `→` e `←` trocam de tela, `Home` volta ao índice, `T` alterna o tema.

| Tela | Tempo | Frase-guia |
|---|---|---|
| Capa | 0:30 | Caronas controladas por trecho, baldeação entre motoristas diferentes, reserva tudo ou nada |
| 1 Arquitetura | 1:00 | Seis camadas; só a camada de estado conhece o mutex |
| 2 Comunicação | 0:45 | Uma goroutine por cliente; a conexão é a identidade |
| 3 Protocolo | 1:00 | JSON por linha, requisição e resposta, `id` ecoado |
| 4 Encapsulamento | 0:45 | Validação em camadas; erro de formato não derruba a conexão |
| 5 Busca | 1:15 | Pernas, DFS e ordenação; a busca não reserva nada |
| 6 Concorrência | 1:00 | Goroutines sobre poucas threads; um mutex protege o estado |
| 7 Atomicidade | 1:15 | Valida tudo, depois escreve; um recurso só, sem deadlock |
| 8 Interação | 0:30 | “Vocês vão ver ao vivo” |
| 9 Confiabilidade | 0:45 | Não existe assento em espera, então nada fica preso |
| 10 Testes | 0:45 | T2 prova o tudo ou nada; invariantes conferidas pelo protocolo |
| 11 Emulação | 0:45 | Porta publicada no host; é o que a demonstração usa agora |

---

# Parte 2: demonstração ao vivo (cerca de 6 min)

| # | Barema | Terminal · usuário | Ação | Esperado |
|---|---|---|---|---|
| D1 | **2, 11** | A: servidor | Mostrar o terminal do servidor | Linhas `LOGIN → OK` vindas dos clientes de A e de B |
| D2 | **8** | MB · joao | Publicar carona: Salvador `2026-10-05` 08:00 → Feira de Santana 10:00, 2 assentos, R$ 30,00. Carona só para demonstrar a publicação: a data 05/10 a mantém fora da busca de 01/10 | `Carona car-… publicada, com 2 assentos.` |
| D3 | **5, 6, 7** | PA · pedro e PB · lucia | Os dois buscam Salvador → Vitória da Conquista em `2026-10-01` e param em “Reservar qual?”. Na contagem “3, 2, 1”, cada um digita `4` e Enter na sua máquina. A disputa é pelo único assento da `car-4` (Feira de Santana → Jequié, motorista ana), a perna do meio do [4] | 4 itinerários, na ordem da tela 5. **Um** recebe `Reserva … confirmada`; o **outro** recebe `Assento esgotado no trecho Feira de Santana → Jequié.` No servidor: um `OK` e um `ERRO SEM_ASSENTO` |
| D4 | **7, 8** | MB · joao | Detalhar carona `car-1` | Salvador → Feira de Santana: `2 assentos livres`, **só o vencedor** listado. Feira de Santana → Jequié: `3 assentos livres`. O perdedor não ficou com assento na `car-1`, que tinha vaga: **nada foi reservado pela metade** |
| D5 | **8** | vencedor do D3 | `Minhas reservas`, depois `Cancelar reserva`, confirmando com `s` | A reserva aparece na lista; depois `Reserva … cancelada. Os assentos voltaram para os trechos.` |
| D6 | **9** | PB · lucia, depois PA · pedro | lucia busca de novo e para em “Reservar qual?”, vendo o [4], que usa o último assento da `car-4`. **Ctrl+C** no PB. pedro busca e reserva o **[4]** | A reserva do pedro é **confirmada**, com o assento que a lucia via. No servidor, a conexão da lucia termina sem nenhum `RESERVAR` |
| D7 | **7, 8** | MB · joao, depois ana | joao cancela `car-1`, confirmando com `s`. `Sair`, reabrir o motorista, entrar como `ana` e detalhar `car-4` | `Carona car-1 cancelada. 1 reserva de passageiro cancelada em cascata.` Depois, `car-4` com `1 assento livre` e nenhum passageiro: o assento disputado voltou numa carona de **outro** motorista |

O item 10 (Testes) é coberto pela tela do HTML e pela Parte 3. O item 1
(Arquitetura) aparece em toda a demonstração.

**O que se disputa no D3.** O [4] é um itinerário de três pernas, com três
motoristas, e os dois passageiros pedem o itinerário inteiro:

```
car-1 (joao)     Salvador → Feira de Santana    3 assentos
car-4 (ana)      Feira de Santana → Jequié      1 assento   ← disputado
car-2 (carlos)   Jequié → Vitória da Conquista  2 assentos
```

Só um leva o assento da `car-4`. O outro é recusado sem ficar com o assento da
`car-1`, que tinha vaga, e é isso que o D4 mostra: a atomicidade do item 7.

**Sobre o D3 e o D5.** Não importa quem vence: o vencedor cancela no próprio
terminal e continua logado. No D6 os papéis são fixos, com a lucia caindo e o
pedro reservando, sem conflito de horário, porque se o pedro venceu ele já
cancelou. O resultado do D3 também não depende de os dois Enter saírem no mesmo
milissegundo: o servidor serializa as confirmações, e quem chega depois encontra
o assento ocupado.

No terminal do servidor, a ordem entre as duas linhas de `RESERVAR` não prova
quem entrou primeiro na seção crítica: a linha é escrita depois que o lock é
liberado, e duas goroutines podem escrever em ordem trocada. O que prova a
serialização é o resultado, com exatamente um `OK`.

**Sobre o D6.** O cliente não trata sinais: o Ctrl+C mata o processo sem
`LOGOUT`, o sistema operacional fecha o socket, e o servidor vê o mesmo que numa
queda abrupta. Como a busca não bloqueia nada, não havia assento preso.

**Estado ao longo da demonstração** (assentos livres por trecho, para responder
perguntas):

| Depois de | car-1 | car-2 | car-4 |
|---|---|---|---|
| início | [3, 3] | [2] | [1] |
| D3 (disputa pelo #4) | [2, 3] | [1] | [0] |
| D5 (vencedor cancela) | [3, 3] | [2] | [1] |
| D6 (pedro reserva o #4) | [2, 3] | [1] | [0] |
| D7 (cascata) | cancelada | [2] | [1] |

Ao terminar, **salve o registro** (seção 0.2) antes de reiniciar o servidor.

---

# Parte 3: experimento guiado

Mede latência e vazão com e sem rede, para as curvas do relatório
(`PROJETO.md`, §8.3). É o item 10 do Barema. Não faz parte dos 16 minutos.

## 3.1 Regras da medição

- **Reinicie o servidor antes de cada rodada, com o registro desligado.** A
  latência cresce com o histórico de reservas.
- **Uma carga por vez, sem clientes manuais abertos**, para não distorcer os
  números nem gerar recusas.
- **Mesma duração nas duas rodadas** (5 s por ponto, por padrão). Só rodadas com
  a mesma duração e servidor novo são comparáveis.
- **Sem `-race`.** O detector multiplica o custo de cada acesso à memória, e o
  número medido seria o dele. O binário `/bin/carga` da imagem já é compilado
  sem ele.

## 3.2 Servidor desta parte

Na máquina A, `Ctrl+C` no servidor e:

```bash
docker run --rm --name vaijunto-servidor -p 9000:9000 -e TZ=America/Bahia \
  -v "$(pwd)/dados:/dados" vaijunto-servidor \
  --usuarios /dados/usuarios.json --caronas /dados/caronas.json \
  --log-operacoes=false
```

Esperado: `servidor: escutando em [::]:9000 (registro de operações: false)`.

É o comando da seção 0.2 com `--log-operacoes=false` (D19). Com o registro
ligado, cada requisição vira uma escrita síncrona no terminal; com centenas de
milhares de requisições, isso entra na latência medida e disputa com o mutex do
estado. **Durante a carga, o terminal do servidor deve ficar parado.**

## 3.3 Rodada com rede (máquina B)

Reinicie o servidor com o comando de 3.2 e rode:

```bash
mkdir -p resultados
docker run --rm --user "$(id -u):$(id -g)" \
  -e VAIJUNTO_CARGA=1 -e VAIJUNTO_CARGA_ENDERECO=$IP_A:9000 \
  -e VAIJUNTO_CARGA_ROTULO=rede \
  -v "$(pwd)/resultados:/carga/resultados" -w /carga/testes \
  vaijunto-cliente /bin/carga -test.run '^TestCarga$' -test.v
```

## 3.4 Rodada sem rede (máquina A)

Reinicie o servidor com o comando de 3.2 e, em outro terminal da **máquina A**,
rode o mesmo comando de 3.3 com `IP_A` apontando para o IP da própria máquina A e
`VAIJUNTO_CARGA_ROTULO=mesma-maquina`.

As duas curvas passam pelo mesmo caminho de contêiner, e só a rede muda.

**Esperado nas duas rodadas:** `PASS`, `recusas` igual a 0 nos quatro pontos
(N = 1, 10, 50 e 100 clientes), um CSV em `resultados/` e nenhuma linha nova no
terminal do servidor.

O `--user` evita que o CSV saia com dono `root`; o `-w` e o `-v` fazem o
`../resultados` do binário cair na pasta `resultados/` da máquina.

## 3.5 Coleta para o relatório

**Recolha:**

- os dois CSVs de `resultados/`, um da máquina A e outro da máquina B;
- o registro do servidor salvo na Parte 2 (`registro-demonstracao.txt`);
- fotos ou capturas dos passos D3, D4 e D7.

**Anote:**

| Item | Valor |
|---|---|
| Data e hora | |
| Commit (`git log -1 --oneline`) | |
| IPs de A e B | |
| Sistema e processador de cada máquina | |
| Rede: cabo ou Wi-Fi, e switch | |

**Marque os resultados:**

| Passo | Esperado | Observado |
|---|---|---|
| 0.3 | B conecta ao servidor em A pelo `PING` | |
| D2 | Carona publicada, fora da busca de 01/10 | |
| D3 | 4 itinerários na ordem da §9.2; 1 confirmação e 1 `SEM_ASSENTO`, nos clientes e no registro | |
| D4 | `car-1` com o vencedor só no primeiro trecho; nada reservado pela metade | |
| D5 e D6 | Cancelar devolve o assento; cliente morto não prende nada | |
| D7 | Cascata cancela 1 reserva e devolve o assento da `car-4` | |
| 3.3 e 3.4 | 2 rodadas, 0 recusas, servidor com `registro de operações: false` | |

## 3.6 Opcional: cabo desconectado

Mostra os prazos da D17, mas leva 3 minutos. Com `maria` logada num cliente da
máquina B, desconecte o cabo (ou o Wi-Fi) e escolha `Minhas reservas`.

- **Em B:** em até 30 s, a sessão termina com erro de rede, por exemplo
  `o servidor não respondeu em 30s`.
- **Em A:** depois de 2 a 3 minutos, o *keep-alive* TCP detecta o cliente sumido.
  A linha sai no formato do `log` padrão (D18), sem milissegundos, por exemplo
  `servidor: conexão … encerrada: read tcp …: connection timed out`. Se o
  endereço dos clientes aparecer como gateway do Docker, a linha pode sair como
  `conexão encerrada` comum.

---

# Se algo der errado

| Sintoma | Causa provável | O que fazer |
|---|---|---|
| O cliente não conecta | IP errado, servidor parado ou firewall | Em A, `docker ps` precisa mostrar `0.0.0.0:9000->9000/tcp`. Teste `ping $IP_A`. Libere a porta 9000/tcp no firewall |
| Porta 9000 ocupada na máquina A | Outro processo usando a porta | `ss -ltn \| grep 9000`. Suba com `-p 9100:9000` e use `$IP_A:9100` nos clientes |
| O cliente fecha logo ao abrir | Faltou `-it` | Rode com `docker run --rm -it …` |
| Busca com menos de 4 itinerários | O servidor não estava limpo | Reinicie o servidor, reabra os clientes e recomece do D1 |
| Cancelamento recusado por prazo | Já passou de 01/10/2026 05:00 (reserva) ou 06:00 (carona) | Pule o D5 e o D7 e registre o motivo |
| Ctrl+C não encerra o cliente | O contêiner não repassa o sinal | `docker kill cliente-pb` em outro terminal da máquina B. Para o servidor é a mesma coisa: o socket fecha igual |
| A rede do laboratório falha | — | Rode todos os clientes na máquina A. A disputa do D3 perde o efeito de duas máquinas, mas continua válida |
| Endereço no registro aparece como `172.17.0.1` | O Docker encaminha a porta por um proxy, e o servidor enxerga o gateway | Não afeta o teste. Identifique o cliente pelo usuário da linha |
| `escutando em` sem `(registro de operações: …)` | Imagem do servidor anterior ao registro | Monte a imagem do servidor de novo |
| Horários 3 h deslocados nos clientes ou em `servidor_em` | Imagem antiga | Monte as imagens de novo |
| Só as linhas sem milissegundos 3 h à frente | Servidor subiu sem `-e TZ=America/Bahia` | Não afeta o sistema. Para alinhar, reinicie com o comando da 0.2 |
| `permission denied` ao gravar o CSV | Faltou `mkdir -p resultados` ou o `--user` | Crie a pasta e rode de novo com `--user "$(id -u):$(id -g)"` |
| Carga com recusas | Servidor não reiniciado, cliente manual aberto ou duas cargas ao mesmo tempo | Reinicie o servidor e rode uma carga por vez |
| Terminal do servidor rolando durante a carga | Servidor subiu sem `--log-operacoes=false` | Reinicie com o comando da 3.2 e refaça a rodada |
| Registro perdido | O servidor foi reiniciado antes do `docker logs` | Refaça a parte, ou use as capturas de tela |
