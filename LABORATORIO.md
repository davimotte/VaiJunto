# Teste laboratorial com dois computadores

## Objetivo

Verificar o VAIJUNTO com o servidor e os clientes em computadores distintos
(RNF11), usando contêineres Docker nas duas máquinas (item 11 do Barema):

| Máquina | Papel |
|---|---|
| **A** | Servidor. Também roda a carga "sem rede" na Fase 4 |
| **B** | Clientes em três terminais, e a carga "com rede" |

O roteiro tem cinco fases:

| Fase | O que prova | Barema |
|---|---|---|
| 0 e 1 | Servidor no ar e conectividade entre contêineres de máquinas diferentes | 2, 11 |
| 2 | Todas as operações, busca com baldeação, disputa pelo último assento e cancelamentos | 5, 6, 7, 8 |
| 3 | Cliente encerrado de forma abrupta não prende assento | 9 |
| 4 | Latência e vazão com e sem rede | 10 |
| 5 | Registro dos resultados para o relatório | — |

Durante todo o teste, o terminal do servidor mostra uma linha por conexão e por
operação atendida (D19). Esse registro é a evidência do lado do servidor.

---

## Antes do laboratório

**Data.** As caronas de demonstração são de **01/10/2026**. A Fase 2 cancela
uma reserva e a carona `car-1`, o que só é permitido até **01/10/2026 às 05:00**
(uma hora antes da partida) para a reserva, e até 06:00 para a carona.

**Nas duas máquinas:**

- Docker instalado, com permissão para o usuário rodar `docker`.
- Cópia do repositório no mesmo commit. Confira com `git log -1 --oneline`.
- As duas máquinas na mesma rede.

Os comandos supõem Linux com `bash` e são executados na raiz do repositório.

**Monte as imagens com internet.** A montagem baixa as imagens base `golang` e
`alpine`.

Na máquina A, as duas imagens (a do cliente serve à carga da Fase 4):

```bash
docker build -f Dockerfile.servidor -t vaijunto-servidor .
docker build -f Dockerfile.cliente -t vaijunto-cliente .
```

Na máquina B:

```bash
docker build -f Dockerfile.cliente -t vaijunto-cliente .
```

Se o laboratório não tiver internet, monte as imagens antes e leve-as num
pendrive:

```bash
docker save vaijunto-servidor vaijunto-cliente | gzip > vaijunto.tar.gz   # onde montou
docker load < vaijunto.tar.gz                                             # no laboratório
```

---

## Fase 0: servidor na máquina A

**0.1. Descubra e anote o IP da máquina A:**

```bash
hostname -I
```

**0.2. Suba o servidor** num terminal dedicado, que fica visível durante todo o
teste:

```bash
docker run --rm --name vaijunto-servidor -p 9000:9000 \
  -e TZ=America/Bahia \
  -v "$(pwd)/dados:/dados" \
  vaijunto-servidor --usuarios /dados/usuarios.json --caronas /dados/caronas.json
```

Esperado: `servidor: escutando em [::]:9000 (registro de operações: true)`. Se o
trecho entre parênteses não aparecer, a imagem é anterior ao registro: monte-a
de novo.

Cada operação aparece assim:

```
2026/10/01 08:15:02.431 [192.168.0.20:51234] maria    RESERVAR               id="3" → OK (0.312 ms)
```

Os campos são: instante, endereço do cliente, usuário (`-` antes do login),
operação, id da requisição, resultado (`OK`, ou `ERRO` com o código) e duração.
A senha nunca aparece.

O `-e TZ=America/Bahia` só alinha o horário das linhas sem milissegundos, que
são a de boot e as de erro de conexão, com o das linhas de operação. O domínio
não depende de `TZ` (`PROJETO.md`, seção 10.1).

**0.3. Teste local, em outro terminal da máquina A:**

```bash
printf '{"id":"1","tipo":"PING","dados":{}}\n' | nc -q1 localhost 9000
```

Esperado: `"status":"OK"` e `servidor_em` com `-03:00`. No terminal do servidor,
três linhas: `conexão aberta`, `-  PING  id="1" → OK` e `conexão encerrada`.

**Para reiniciar o servidor** (pedido nas fases seguintes): `Ctrl+C` no terminal
dele, depois o comando 0.2 de novo. O estado vive só em memória, então reiniciar
volta ao cenário inicial. A Fase 4 usa outro comando, sem o registro.

**Salve o registro antes de reiniciar.** O `--rm` apaga o contêiner no `Ctrl+C`,
e o `docker logs` vai junto. No fim das Fases 2 e 3, rode em outro terminal da
máquina A, trocando o número da fase:

```bash
docker logs vaijunto-servidor > registro-fase2.txt 2>&1
```

---

## Fase 1: conectividade (máquina B)

Abra **três terminais** na máquina B (T1, T2 e T3) e, em cada um, defina o IP
da máquina A:

```bash
export IP_A=192.168.x.y
```

**1.1. PING pela rede,** se o `nc` estiver instalado:

```bash
printf '{"id":"1","tipo":"PING","dados":{}}\n' | nc -q1 $IP_A 9000
```

**1.2. Cliente em contêiner:**

```bash
docker run --rm -it -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/passageiro
```

Entre com `maria` / `abcd` e escolha `Sair`.

**Critério:** o cliente conecta e aceita o login, e o terminal do servidor mostra
`maria  LOGIN … → OK`. Se falhar, veja
[Solução de problemas](#solução-de-problemas) antes de seguir.

A conectividade funciona porque o `-p 9000:9000` publica a porta do contêiner no
IP da máquina A, e o cliente em B usa esse IP. As redes *bridge* do Docker de
máquinas diferentes não se enxergam diretamente (`PROJETO.md`, seção 10.2).

---

## Fase 2: fluxo completo

Comece com o **servidor recém-iniciado**. É o roteiro da seção 9.2 do
`PROJETO.md`, com o cancelamento de reserva pelo passageiro.

Comandos dos clientes, na máquina B:

```bash
docker run --rm -it -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/motorista    # T1
docker run --rm -it -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/passageiro   # T2 e T3
```

Usuários: motoristas `joao`, `carlos` e `ana` (senha `1234`); passageiros
`maria`, `pedro` e `lucia` (senha `abcd`). Para trocar de usuário num terminal,
escolha `Sair` e rode o cliente de novo.

| Passo | Terminal | Usuário | Ação | Esperado |
|---|---|---|---|---|
| 2.1 | T1 | `ana` | Publicar carona: Salvador `2026-10-05` `08:00` → Feira de Santana `10:00`, 2 assentos, R$ 30,00. Depois, `Minhas caronas` | `Carona car-… publicada, com 2 assentos.` A lista mostra `car-3`, `car-4`, `car-7` e a nova. A data fica fora de 01/10 para não mudar a busca |
| 2.2 | T2 | `maria` | Buscar itinerários: Salvador → Vitória da Conquista, `2026-10-01`. Reservar o [3] | Exatamente 4 itinerários, nesta ordem: [1] R$ 110,00 direta · [2] R$ 80,00 · [3] R$ 100,00 · [4] R$ 95,00 com 2 baldeações. Depois, `Reserva res-… confirmada. Total: R$ 100,00` |
| 2.3 | T2 e T3 | `pedro` em T2, `lucia` em T3 | Os dois fazem a mesma busca e param em `Reservar qual?`. Numa contagem "3, 2, 1", os dois digitam `4` e Enter | **Um** recebe `Reserva … confirmada`. O **outro** recebe `Assento esgotado no trecho Feira de Santana → Jequié.` No servidor: dois `RESERVAR`, um `→ OK` e outro `→ ERRO SEM_ASSENTO` |
| 2.4 | T1 | `ana` | Detalhar carona `car-4` | Feira de Santana → Jequié: `0 assentos livres`, com o vencedor do 2.3 como passageiro |
| 2.5 | T2 ou T3 | o vencedor do 2.3 | Cancelar reserva, confirmar com `s` | `Reserva res-… cancelada. Os assentos voltaram para os trechos.` |
| 2.6 | T1 | `ana` | Detalhar `car-4` de novo | `1 assento livre` e `(nenhum passageiro)` |
| 2.7 | T1 | `joao` | Cancelar carona `car-1`, confirmar com `s` | `Carona car-1 cancelada. 1 reserva de passageiro cancelada em cascata.` É a da `maria` |
| 2.8 | T1 | `carlos` | Detalhar `car-2` | `2 assentos livres` e `(nenhum passageiro)`. O assento da `maria` voltou numa carona de **outro** motorista |
| 2.9 | T2 | `maria` | `Minhas reservas`, incluindo canceladas (`s`) | A reserva do 2.2 aparece como cancelada |

**Sobre o passo 2.3:** o resultado é o mesmo mesmo que os dois Enter não saiam no
mesmo milissegundo. O servidor serializa as confirmações, e quem chega depois
encontra o assento já ocupado. A prova de que isso vale sob disputa real é o T1
da suíte automatizada; aqui, a disputa fica visível.

No terminal do servidor, a ordem entre as duas linhas de `RESERVAR` não prova
quem entrou primeiro na seção crítica: a linha é escrita depois que o lock é
liberado, e duas goroutines podem escrever em ordem trocada. O que prova a
serialização é o resultado, com exatamente um `OK`.

Ao terminar, **salve o registro** (`registro-fase2.txt`, veja a Fase 0).

---

## Fase 3: queda abrupta de cliente

Saia dos clientes abertos e **reinicie o servidor**.

1. **Em T3,** abra o passageiro com um nome de contêiner:

   ```bash
   docker run --rm -it --name cliente-queda -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/passageiro
   ```

   Entre como `lucia`, busque Salvador → Vitória da Conquista em `2026-10-01` e
   **pare** em `Reservar qual?`.

2. **Em T2,** mate o cliente:

   ```bash
   docker kill cliente-queda
   ```

3. **Ainda em T2,** abra o passageiro, entre como `pedro`, faça a mesma busca e
   reserve o [4].

Esperado: a reserva do `pedro` é **confirmada**, mesmo usando o assento único de
`car-4` que a `lucia` estava vendo. A busca não bloqueia nada (D07), então o
cliente morto não deixou nenhum assento preso.

No terminal do servidor:

```
… lucia    LOGIN                  id="…" → OK (…)
… lucia    BUSCAR_ITINERARIOS     id="…" → OK (…)
… conexão encerrada                                   ← docker kill
… pedro    LOGIN                  id="…" → OK (…)
… pedro    BUSCAR_ITINERARIOS     id="…" → OK (…)
… pedro    RESERVAR               id="…" → OK (…)
```

A conexão da `lucia` termina sem nenhum `RESERVAR`, e como `conexão encerrada`
comum, não como erro. Matar o contêiner fecha o socket do mesmo jeito que sair
do cliente, então para o servidor é uma desconexão normal.

Ao terminar, **salve o registro** (`registro-fase3.txt`).

**Opcional, cabo desconectado.** Mostra os prazos da D17, mas leva 3 minutos.
Com `maria` logada em T2, desconecte o cabo (ou o Wi-Fi) da máquina B e escolha
`Minhas reservas`.
- **Em B:** em até 30 s, a sessão termina com erro de rede, por exemplo
  `o servidor não respondeu em 30s`.
- **Em A:** depois de 2 a 3 minutos, o *keep-alive* TCP detecta o cliente sumido.
  A linha sai no formato do `log` padrão (D18), sem milissegundos, por exemplo
  `servidor: conexão … encerrada: read tcp …: connection timed out`. Se o
  endereço dos clientes aparecer como gateway do Docker (veja
  [Solução de problemas](#solução-de-problemas)), a linha pode sair como
  `conexão encerrada` comum.

---

## Fase 4: teste de carga (seção 8.3)

**Regras:**

- **Reinicie o servidor antes de cada rodada, com o registro desligado.** A
  latência cresce com o histórico de reservas (seção 8.3).
- **Uma carga por vez, sem clientes manuais abertos,** para não distorcer os
  números nem gerar recusas.
- **Use a mesma duração nas duas rodadas** (o padrão é 5 s por ponto).

**Comando do servidor nesta fase.** Na máquina A, `Ctrl+C` no servidor e:

```bash
docker run --rm --name vaijunto-servidor -p 9000:9000 \
  -e TZ=America/Bahia \
  -v "$(pwd)/dados:/dados" \
  vaijunto-servidor --usuarios /dados/usuarios.json --caronas /dados/caronas.json \
  --log-operacoes=false
```

Esperado: `servidor: escutando em [::]:9000 (registro de operações: false)`.

É o comando 0.2 com `--log-operacoes=false` (D19). Com o registro ligado, cada
requisição vira uma escrita síncrona no terminal. Com centenas de milhares de
requisições, isso entra na latência medida e disputa com o mutex do estado.
Durante a carga, o terminal do servidor deve ficar parado.

**4.1. Com rede, na máquina B.** Reinicie o servidor com o comando acima e rode:

```bash
mkdir -p resultados
docker run --rm --user "$(id -u):$(id -g)" \
  -e VAIJUNTO_CARGA=1 -e VAIJUNTO_CARGA_ENDERECO=$IP_A:9000 \
  -e VAIJUNTO_CARGA_ROTULO=rede \
  -v "$(pwd)/resultados:/carga/resultados" -w /carga/testes \
  vaijunto-cliente /bin/carga -test.run '^TestCarga$' -test.v
```

**4.2. Sem rede, na máquina A.** Reinicie o servidor com o comando acima e, em
outro terminal da máquina A, rode o mesmo comando com `IP_A` definido para o IP
da própria máquina A e `VAIJUNTO_CARGA_ROTULO=mesma-maquina`.

Esperado nas duas rodadas: `PASS`, `recusas` igual a 0 nos quatro pontos, um CSV
em `resultados/` e nenhuma linha nova no terminal do servidor.

---

## Fase 5: registro para o relatório

**Recolha:**

- os dois CSVs de `resultados/`, um da máquina A e outro da máquina B;
- `registro-fase2.txt` e `registro-fase3.txt`, da máquina A;
- fotos ou capturas dos passos 2.2, 2.3 e 2.7.

**Anote:**

| Item | Valor |
|---|---|
| Data e hora | |
| Commit (`git log -1 --oneline`) | |
| IPs de A e B | |
| Sistema e processador de cada máquina | |
| Rede: cabo ou Wi-Fi, e switch | |

**Marque os resultados:**

| Fase | Esperado | Observado |
|---|---|---|
| 1 | B conecta ao servidor em A | |
| 2.2 | 4 itinerários na ordem da seção 9.2 | |
| 2.3 | 1 confirmação e 1 `SEM_ASSENTO`, nos clientes e no registro | |
| 2.4 a 2.6 | Cancelar a reserva devolve o assento de `car-4` | |
| 2.7 e 2.8 | Cascata cancela 1 reserva e devolve o assento de `car-2` | |
| 3 | `pedro` reserva o assento que `lucia` via; conexão da `lucia` encerrada sem `RESERVAR` | |
| 4 | 2 rodadas, 0 recusas, servidor com `registro de operações: false` | |

---

## Solução de problemas

| Sintoma | Causa provável | O que fazer |
|---|---|---|
| `não foi possível conectar` | IP errado, servidor parado ou firewall | Na máquina A, `docker ps` precisa mostrar `0.0.0.0:9000->9000/tcp`. Teste `ping $IP_A`. Libere a porta 9000/tcp no firewall |
| Porta 9000 ocupada na máquina A | Outro processo usando a porta | `ss -ltn \| grep 9000`. Suba com `-p 9100:9000` e use `$IP_A:9100` nos clientes |
| Cliente sai logo ao abrir | Faltou `-it` | Rode com `docker run --rm -it …` |
| Busca com menos de 4 itinerários | Servidor não foi reiniciado | Reinicie o servidor |
| Cancelamento recusado por prazo | Já passou de 01/10/2026 05:00 (reserva) ou 06:00 (carona) | Pule os passos 2.5 a 2.9 e registre o motivo |
| `permission denied` ao gravar o CSV | Faltou `mkdir -p resultados` | Crie a pasta e rode de novo |
| Carga com recusas | Servidor não reiniciado, cliente manual aberto ou duas cargas ao mesmo tempo | Reinicie o servidor e rode uma carga por vez |
| Terminal do servidor rolando durante a carga | Servidor subiu com o comando 0.2 | Reinicie com o comando da Fase 4 e refaça a rodada |
| `escutando em` sem `(registro de operações: …)` | Imagem do servidor anterior ao registro | Monte a imagem do servidor de novo |
| Horários 3 h deslocados nos clientes ou em `servidor_em` | Imagem antiga | Monte as imagens de novo |
| Só as linhas sem milissegundos (boot e erros) 3 h à frente | Servidor subiu sem `-e TZ=America/Bahia` | Não afeta o sistema. Para alinhar, reinicie com o comando 0.2 |
| Endereço no registro não é o IP de B (por exemplo `172.17.0.1`) | O Docker encaminha a porta por um proxy, e o servidor enxerga o gateway | Não afeta o teste. Identifique o cliente pelo usuário da linha |
| Registro da fase perdido | O servidor foi reiniciado antes do `docker logs` | Refaça a fase, ou use as capturas de tela |
