# Teste laboratorial com três computadores

## Objetivo

Verificar o VAIJUNTO com o servidor e os clientes em computadores distintos
(RNF11), usando contêineres Docker em todas as máquinas (item 11 do Barema):

| Máquina | Papel |
|---|---|
| **A** | Servidor |
| **B** | Clientes (motorista, passageiro e teste de carga) |
| **C** | Clientes (passageiro e teste de carga) |

O roteiro tem cinco fases: conectividade, fluxo completo com dois clientes em
máquinas diferentes, queda abrupta de cliente, teste de carga e registro dos
resultados.

---

## Antes do laboratório

**Data.** As caronas de demonstração são de **01/10/2026**. A Fase 2 cancela
`car-1`, o que só é permitido até a partida dela, **01/10/2026 às 06:00**. Depois
disso, o passo 2.6 é recusado com `PRAZO_CANCELAMENTO_EXPIRADO`.

**Requisitos em cada máquina:**

- Docker instalado, com permissão para o usuário rodar `docker`.
- Cópia do repositório no mesmo commit nas três máquinas. Confira com
  `git log -1 --oneline`.
- As três máquinas na mesma rede.

Os comandos supõem Linux com `bash`.

**Monte as imagens com internet.** A montagem baixa as imagens base `golang` e
`alpine`.

Na máquina A:

```bash
docker build -f Dockerfile.servidor -t vaijunto-servidor .
```

Nas máquinas B e C:

```bash
docker build -f Dockerfile.cliente -t vaijunto-cliente .
```

Se o laboratório não tiver internet, monte as imagens antes e leve-as num
pendrive:

```bash
docker save vaijunto-cliente | gzip > vaijunto-cliente.tar.gz   # onde montou
docker load < vaijunto-cliente.tar.gz                           # no laboratório
```

---

## Fase 0: servidor na máquina A

**0.1. Descubra e anote o IP da máquina A:**

```bash
hostname -I
```

**0.2. Suba o servidor** num terminal dedicado. O log fica visível durante todo
o teste:

```bash
docker run --rm --name vaijunto-servidor -p 9000:9000 \
  -v "$(pwd)/dados:/dados" \
  vaijunto-servidor --usuarios /dados/usuarios.json --caronas /dados/caronas.json
```

Esperado: `servidor: escutando em [::]:9000`.

**0.3. Teste local, em outro terminal da máquina A:**

```bash
printf '{"id":"1","tipo":"PING","dados":{}}\n' | nc -q1 localhost 9000
```

Esperado: `"status":"OK"` e `servidor_em` com `-03:00`.

**Para reiniciar o servidor** (pedido nas fases seguintes): `Ctrl+C` no terminal
dele, depois o comando 0.2 de novo. O estado vive só em memória, então reiniciar
volta ao cenário inicial.

---

## Fase 1: conectividade (máquinas B e C)

Em **cada terminal** das máquinas B e C, defina o IP da máquina A:

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

Esperado: o cliente mostra o cabeçalho e pede `Usuário:`. Entre com `maria` /
`abcd` e escolha `Sair`.

**Critério:** B e C conectam. Se falhar, veja a seção
[Solução de problemas](#solução-de-problemas) antes de seguir.

---

## Fase 2: fluxo completo com clientes em máquinas diferentes

É o roteiro da seção 9.2 do `PROJETO.md`, distribuído entre B e C. Comece com o
servidor recém-iniciado.

Usuários: motoristas `joao`, `carlos` e `ana` (senha `1234`); passageiros
`maria`, `pedro` e `lucia` (senha `abcd`).

Comandos dos clientes:

```bash
docker run --rm -it -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/motorista
docker run --rm -it -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/passageiro
```

| Passo | Máquina | Usuário | Ação | Esperado |
|---|---|---|---|---|
| 2.1 | B | `ana` (motorista) | Publicar carona: Salvador `2026-10-05` `08:00` → Feira de Santana `10:00`, 2 assentos, R$ 30,00 | Carona publicada. A data fica fora de 01/10, para não mudar a busca |
| 2.2 | C | `maria` | Buscar itinerários: Salvador → Vitória da Conquista, `2026-10-01` | Exatamente 4 itinerários, nesta ordem: [1] R$ 110,00 direta · [2] R$ 80,00 · [3] R$ 100,00 · [4] R$ 95,00 com 2 baldeações |
| 2.3 | C | `maria` | Reservar o [3] | `Reserva res-… confirmada. Total: R$ 100,00` |
| 2.4 | B e C | `pedro` em B, `lucia` em C | Os dois fazem a mesma busca e param em `Reservar qual?`. Numa contagem "3, 2, 1", os dois digitam `4` e Enter | **Um** recebe `Reserva … confirmada`. O **outro** recebe `Assento esgotado no trecho Feira de Santana → Jequié.` |
| 2.5 | B | `ana` e depois `carlos` | Detalhar carona: `car-4` (ana) e `car-2` (carlos) | `car-4`: 1 passageiro, 0 livres. `car-2`: 2 passageiros (maria e o vencedor), 0 livres |
| 2.6 | B | `joao` | Cancelar carona `car-1` | Cancelada, com **2 reservas** caídas na cascata |
| 2.7 | C | `maria` | Minhas reservas, incluindo canceladas (`s`) | A reserva aparece como cancelada |
| 2.8 | B | `ana` e depois `carlos` | Detalhar `car-4` e `car-2` de novo | `car-4`: 1 livre. `car-2`: 2 livres. Os assentos voltaram nas caronas de **outros** motoristas |

Para trocar de usuário, escolha `Sair` e rode o cliente de novo. Cada máquina
pode ter vários terminais com clientes ao mesmo tempo.

**Sobre o passo 2.4:** o resultado é o mesmo mesmo que os dois Enter não saiam no
mesmo milissegundo. O servidor serializa as confirmações, e quem chega depois
encontra o assento já ocupado. A prova de que isso vale sob disputa real é o T1
da suíte automatizada; aqui, a disputa fica visível em duas máquinas.

---

## Fase 3: queda abrupta de cliente

Reinicie o servidor antes de começar.

### 3.1. Cliente encerrado no meio de uma operação

1. **Na máquina C,** abra o passageiro com um nome de contêiner:

   ```bash
   docker run --rm -it --name cliente-c -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/passageiro
   ```

   Entre como `lucia`, busque Salvador → Vitória da Conquista em `2026-10-01` e
   **pare** em `Reservar qual?`.

2. **Em outro terminal da máquina C,** mate o cliente:

   ```bash
   docker kill cliente-c
   ```

3. **Na máquina B,** entre como `pedro`, faça a mesma busca e reserve o [4].

Esperado: a reserva do `pedro` é **confirmada**, mesmo usando o assento único de
`car-4` que a `lucia` estava vendo. A busca não bloqueia nada (D07), então o
cliente morto não deixou nenhum assento preso. O servidor não registra nada no
log: fechamento de conexão é desconexão normal.

### 3.2. Cabo de rede desconectado (opcional)

1. **Na máquina C,** entre como `maria` e fique no menu.
2. **Desconecte o cabo** (ou desligue o Wi-Fi) da máquina C e **mantenha
   desconectado por 3 minutos**.
3. **Durante esse tempo,** escolha `Minhas reservas` em C.

Esperado:

- **Na máquina C:** em até 30 s, a sessão termina com erro de rede, por exemplo
  `o servidor não respondeu em 30s` (prazo do cliente, D17).
- **Na máquina A:** depois de cerca de 2 a 3 minutos, uma linha de log de
  conexão encerrada com o endereço de C. É o *keep-alive* TCP detectando o
  cliente sumido (D17).
- **Na máquina B:** durante todo o tempo, uma busca continua respondendo
  normalmente.

---

## Fase 4: teste de carga (seção 8.3)

**Regras:**

- **Nunca rode duas cargas ao mesmo tempo.** As duas usam os mesmos usuários de
  teste e os mesmos dias, e se recusariam uma à outra.
- **Reinicie o servidor antes de cada rodada.** A latência cresce com o
  histórico de reservas (seção 8.3).
- **Não use clientes manuais durante a carga,** para não distorcer os números.
- **Use a mesma duração em todas as rodadas** (o padrão é 5 s por ponto).

Em cada máquina que for rodar a carga, uma vez:

```bash
mkdir -p resultados
```

**4.1. Máquina B (com rede).** Reinicie o servidor e rode:

```bash
docker run --rm --user "$(id -u):$(id -g)" \
  -e VAIJUNTO_CARGA=1 -e VAIJUNTO_CARGA_ENDERECO=$IP_A:9000 \
  -e VAIJUNTO_CARGA_ROTULO=rede-B \
  -v "$(pwd)/resultados:/carga/resultados" -w /carga/testes \
  vaijunto-cliente /bin/carga -test.run '^TestCarga$' -test.v
```

**4.2. Máquina C (com rede, segunda amostra).** Reinicie o servidor e rode o
mesmo comando com `VAIJUNTO_CARGA_ROTULO=rede-C`.

**4.3. Máquina A (sem rede).** Reinicie o servidor. Monte a imagem do cliente
também na máquina A e rode o mesmo comando com o **IP da própria máquina A** e
`VAIJUNTO_CARGA_ROTULO=mesma-maquina`.

Esperado em cada rodada: `PASS`, `recusas` igual a 0 nos quatro pontos, e um CSV
em `resultados/`.

---

## Fase 5: registro para o relatório

**Recolha:**

- os CSVs de `resultados/` das máquinas A, B e C;
- fotos ou capturas da Fase 2 (passos 2.2, 2.4 e 2.6) e do log do servidor na
  Fase 3.

**Anote:**

| Item | Valor |
|---|---|
| Data e hora | |
| Commit (`git log -1 --oneline`) | |
| IPs de A, B e C | |
| Sistema e processador de cada máquina | |
| Rede: cabo ou Wi-Fi, e switch | |

**Marque os resultados:**

| Fase | Esperado | Observado |
|---|---|---|
| 1 | B e C conectam | |
| 2.2 | 4 itinerários na ordem da seção 9.2 | |
| 2.4 | 1 confirmação e 1 `SEM_ASSENTO` | |
| 2.6 a 2.8 | Cascata devolve assentos em `car-2` e `car-4` | |
| 3.1 | `pedro` reserva o assento que `lucia` via | |
| 3.2 | Cliente encerra em até 30 s; servidor registra a queda | |
| 4 | 3 rodadas, 0 recusas | |

---

## Solução de problemas

| Sintoma | Causa provável | O que fazer |
|---|---|---|
| `não foi possível conectar` | IP errado, servidor parado ou firewall | Na máquina A, `docker ps` precisa mostrar `0.0.0.0:9000->9000/tcp`. Teste `ping $IP_A`. Libere a porta 9000/tcp no firewall |
| Porta 9000 ocupada na máquina A | Outro processo usando a porta | `ss -ltn \| grep 9000`. Suba com `-p 9100:9000` e use `$IP_A:9100` nos clientes |
| Cliente sai logo ao abrir | Faltou `-it` | Rode com `docker run --rm -it …` |
| Busca com menos de 4 itinerários | Servidor não foi reiniciado | Reinicie o servidor |
| Cancelar `car-1` recusado por prazo | Já passou de 01/10/2026 06:00 | Pule os passos 2.6 a 2.8 e registre o motivo |
| `permission denied` ao gravar o CSV | Faltou `mkdir -p resultados` antes de rodar | Crie a pasta e rode de novo |
| Carga com recusas | Servidor não reiniciado ou duas cargas simultâneas | Reinicie o servidor e rode uma carga por vez |
| Horários 3 h deslocados | Imagem antiga | Monte as imagens de novo |
