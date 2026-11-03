# Roteiro de operação, demonstração e experimento

Documento único de operação do consórcio VAIJUNTO em **três máquinas**, uma
empresa em cada. Cinco partes, na ordem:

| Parte | O que cobre | Quando |
|---|---|---|
| **0** | Preparar as três máquinas: IPs, firewall, imagens, prazos | Antes, fora do tempo |
| **1** | Subir as três empresas e provar que elas se enxergam | ~3 min |
| **2** | Demonstrações D-1 a D-6, mais o abort presumido com `curl` | ~12 min |
| **3** | Medição com os servidores em máquinas distintas | Fora da apresentação |
| **4** | O que fazer quando o tutor derrubar uma empresa | Durante, a qualquer momento |

A especificação do que cada demonstração precisa exercitar é a seção 12.3 do
`PROJETO_P2.md`; aqui está o procedimento. Toda saída reproduzida neste
documento foi copiada de uma execução real.

---

# Parte 0: preparação

## 0.1 As três máquinas

Uma empresa por máquina, e cada uma com **duas** portas: 9000 para os clientes
dela e 8080 para os pares.

| Máquina | Empresa | IP | Variável |
|---|---|---|---|
| A | `vaijunto` | | `IP_A` |
| B | `rotacapixaba` | | `IP_B` |
| C | `rotafluminense` | | `IP_C` |

Anote os IPs com `hostname -I` e exporte os três **em todos os terminais** de
todas as máquinas — os comandos abaixo os usam:

```bash
export IP_A=192.168.0.10 IP_B=192.168.0.11 IP_C=192.168.0.12
```

As três máquinas precisam estar no **mesmo commit** (`git log -1 --oneline`).
Os relógios **não** precisam estar sincronizados, e isso faz parte do argumento
da seção 8 do `PROJETO_P2.md`: nenhuma decisão entre servidores usa o relógio
físico.

## 0.2 Firewall: 9000 e 8080

Cada contêiner fica na rede *bridge* da sua máquina, e redes *bridge* de
máquinas diferentes não se enxergam. O que resolve é publicar as portas no host
— por isso as duas precisam estar liberadas nas três máquinas:

```bash
sudo ufw allow 9000/tcp && sudo ufw allow 8080/tcp     # ou o firewall da distro
```

Esquecer a 8080 é a falha mais provável desta demonstração, e ela se disfarça:
os clientes conectam normalmente, a busca volta **parcial** e toda reserva
federada responde `EMPRESA_INDISPONIVEL`, como se os outros servidores
estivessem fora do ar.

## 0.3 Montar as imagens

A montagem baixa `golang:1.23-alpine` e `alpine:3.20`. Na raiz do repositório:

```bash
# Nas três máquinas: a imagem do servidor
docker build -f Dockerfile.servidor -t vaijunto-servidor .

# Onde for rodar cliente ou simulador (as três, por segurança)
docker build -f Dockerfile.cliente -t vaijunto-cliente .
```

Sem internet no laboratório, monte antes e leve num pendrive:

```bash
docker save vaijunto-servidor vaijunto-cliente | gzip > vaijunto.tar.gz
docker load < vaijunto.tar.gz
```

A imagem do cliente traz `/bin/passageiro`, `/bin/motorista`, `/bin/simulador` e
`/bin/carga` já compilados, então a máquina que só roda clientes não precisa de
Go instalado.

## 0.4 Diretório de estado

O log write-ahead (D22) fica num diretório **da máquina**, não dentro do
contêiner. É o que torna o `--rm` seguro: o contêiner é descartável, o estado
não. Em cada máquina, na raiz do repositório:

```bash
mkdir -p estado && rm -f estado/*.wal     # começa do zero; a semente recarrega (D37)
```

## 0.5 Prazos desta semente

A semente tem caronas em **05 e 06/11/2026** e os prazos de cancelamento são
aplicados com o relógio **real** (D13). Confira antes de ensaiar:

| Demonstração | Depende do relógio? | Limite |
|---|---|---|
| D-1, D-2, D-3 | Não | Reservar e buscar não conferem se a carona já partiu |
| D-4, D-5 | Não | — |
| **D-6** | **Sim** | `rotafluminense:car-1` parte em **06/11/2026 08:00**; depois disso, cancelar a carona é recusado |
| Cancelar reserva (opcional) | Sim | Uma hora antes da primeira perna: `vaijunto:car-1` parte 05/11 05:00, então o limite é **04/11 04:00** |

Passado o limite da D-6, a saída é deslocar as datas da semente nos três
`dados/<empresa>/caronas.json` — o que muda a seção 12.2 do `PROJETO_P2.md` e os
testes que citam 05 e 06/11. Não é mudança para se fazer na véspera.

## 0.6 Conferências finais

- `curl` e `nc` disponíveis nas três máquinas (`nc` é usado só no diagnóstico).
- Combinado quem aperta o segundo Enter nas disputas D-2 e D-3.
- `python3` na máquina A, se quiser ler a auditoria formatada (opcional: `curl`
  cru também serve).

---

# Parte 1: subir as três empresas

## 1.1 Um comando por máquina

É o comando da seção 13.2 do `PROJETO_P2.md`, com `--auditoria`. Cada empresa
monta **só** o seu diretório de dados; nenhum volume é compartilhado (RD07,
RD11).

```bash
# Máquina A — vaijunto
docker run --rm --name vaijunto -p 9000:9000 -p 8080:8080 -e TZ=America/Bahia \
  -v "$(pwd)/dados/vaijunto:/dados:ro" -v "$(pwd)/estado:/estado" \
  --user "$(id -u):$(id -g)" vaijunto-servidor \
  --empresa vaijunto \
  --pares rotacapixaba=http://$IP_B:8080,rotafluminense=http://$IP_C:8080 \
  --dados /dados --log /estado/vaijunto.wal --auditoria

# Máquina B — rotacapixaba
docker run --rm --name rotacapixaba -p 9000:9000 -p 8080:8080 -e TZ=America/Bahia \
  -v "$(pwd)/dados/rotacapixaba:/dados:ro" -v "$(pwd)/estado:/estado" \
  --user "$(id -u):$(id -g)" vaijunto-servidor \
  --empresa rotacapixaba \
  --pares vaijunto=http://$IP_A:8080,rotafluminense=http://$IP_C:8080 \
  --dados /dados --log /estado/rotacapixaba.wal --auditoria

# Máquina C — rotafluminense
docker run --rm --name rotafluminense -p 9000:9000 -p 8080:8080 -e TZ=America/Bahia \
  -v "$(pwd)/dados/rotafluminense:/dados:ro" -v "$(pwd)/estado:/estado" \
  --user "$(id -u):$(id -g)" vaijunto-servidor \
  --empresa rotafluminense \
  --pares vaijunto=http://$IP_A:8080,rotacapixaba=http://$IP_B:8080 \
  --dados /dados --log /estado/rotafluminense.wal --auditoria
```

Deixe cada terminal visível durante toda a demonstração: é nele que o 2PC
aparece.

**Por que `--auditoria`.** Ela expõe `GET /auditoria`, somente leitura, com os
contadores por trecho, as participações e o outbox de cada empresa. É o que a
D-5 usa para mostrar que nenhum assento ficou preso, e o que o verificador de
invariantes da Parte 3 lê. Não participa de nenhuma decisão: é instrumento de
observação, como o `-race`. Em produção ficaria desligada, porque mostra também
as participações, que nenhum par tem o direito de ver (D32).

**Por que `--user`.** Sem ela o `.wal` nasce com dono `root` no host, e o
próximo `docker run` sem `--user` ou a leitura do arquivo passam a falhar.

## 1.2 Boot esperado

Três linhas, nesta ordem:

```
servidor: log /estado/vaijunto.wal aberto, 0 registros reaplicados, relógio de Lamport em 0
servidor: empresa vaijunto atendendo clientes em [::]:9000 e pares em [::]:8080 (pares: 2, registro de operações: true)
servidor: recuperação: 0 reserva(s) com a fase 2 a retomar, 0 participação(ões) em PREPARADO com o prazo de dúvida armado, 0 aviso(s) no outbox a entregar
```

A terceira linha sai em **todo** boot, porque todo boot é uma recuperação
(seção 9.1): num servidor que subiu limpo as três listas vêm vazias. É a mesma
linha que, na D-5, vai mostrar `1 reserva(s) com a fase 2 a retomar`.

`pares: 2` confirma que o `--pares` foi lido. Se vier `pares: 0`, a lista está
errada e nenhuma reserva federada vai funcionar.

## 1.3 Provar que as três se enxergam

De cada máquina, pergunte a saúde das outras duas (seção 4.1 da API):

```bash
curl -s http://$IP_B:8080/saude && echo && curl -s http://$IP_C:8080/saude
```

```json
{"empresa":"rotacapixaba","versao_api":1,"instante":"2026-11-03T11:25:47.749706005-03:00"}
```

Nos terminais dos servidores, nada: `/saude` é diagnóstico e aceita chamada sem
os cabeçalhos do consórcio (seção 1.1 da API), então o `curl` de uma pessoa
funciona. Esta é a evidência isolada do item 11 do barema, e separa "a rede está
errada" de "o cliente está errado" antes de a apresentação começar.

Confirmação pelo caminho que a demonstração usa de verdade — uma busca federada
na máquina A, com um cliente ou com `nc`:

```bash
printf '{"id":"1","tipo":"LOGIN","dados":{"usuario":"maria","senha":"abcd"}}\n{"id":"2","tipo":"BUSCAR_ITINERARIOS","dados":{"origem":"Salvador","destino":"Petrópolis","data":"2026-11-05"}}\n' | nc -q1 localhost 9000
```

Precisa vir **um** itinerário, `"parcial":false` e `"empresas_indisponiveis":[]`.
No terminal de A aparecem as duas chamadas que ela fez:

```
[vaijunto] → rotacapixaba GET /federacao/caronas?ate=…&desde=… L=0 → 200 (2.380 ms)
[vaijunto] → rotafluminense GET /federacao/caronas?ate=…&desde=… L=0 → 200 (2.735 ms)
```

Se vier `"parcial":true`, pare aqui e volte à 0.2: é a porta 8080.

## 1.4 Abrir os clientes

| Máquina | Terminal | Cliente | Usuário | Usado em |
|---|---|---|---|---|
| A | servidor | — | — | todas |
| A | PA | passageiro | `maria` / `abcd` | D-1, D-6 |
| A | PA2 | passageiro | `pedro` / `abcd` | D-2 |
| A | PA3 | passageiro | `lucia` / `abcd` | D-4 |
| B | servidor | — | — | todas |
| B | PB | passageiro | `clara` / `abcd` | D-2 |
| B | PB2 | passageiro | `paulo` / `abcd` | D-3 |
| C | servidor | — | — | todas |
| C | PC | passageiro | `tiago` / `abcd` | D-3 |
| C | MC | motorista | `marcos` / `1234` | D-6 |

```bash
docker run --rm -it -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/passageiro
docker run --rm -it -e VAIJUNTO_SERVIDOR=$IP_C:9000 vaijunto-cliente /bin/motorista
```

Na própria máquina da empresa, use o IP dela: o cliente também passa pela porta
publicada. Os clientes precisam de `-it`, porque leem do terminal; sem isso o
processo morre ao ler EOF do stdin.

**Cada passageiro só se conecta ao servidor da própria empresa**, e é por isso
que a tabela amarra usuário a máquina. `maria` não existe na `rotacapixaba`, e
`clara` não existe na `vaijunto`: os nomes são únicos dentro da empresa (seção
12.1), e `vaijunto:teste01` e `rotacapixaba:teste01` são pessoas diferentes.

**Por que estes usuários.** Cada demonstração precisa de um passageiro **sem
reserva ativa que se sobreponha** ao itinerário pedido, senão a resposta é
`CONFLITO_HORARIO` e não o que se quer mostrar. maria, pedro e lucia começam
sem reservas; `lucia` fica reservada à D-4 por isso.

---

# Parte 2: demonstrações

A ordem importa: a D-2 depende do assento que a D-1 consome, e a D-6 cancela o
que a D-1 e a D-2 criaram.

## D-1 — Busca e reserva entre três empresas

**Onde:** PA (máquina A), `maria`.
**Ação:** `1) Buscar itinerários`, Salvador → Petrópolis, `2026-11-05`, e
reservar o único itinerário oferecido.

**Esperado no cliente:** um itinerário, com uma perna em cada empresa e a
empresa dona visível em cada linha, por **R$ 430,00**, de 05/11 05:00 a 06/11
14:30. Depois, `Reserva vaijunto:res-… confirmada.`

**Esperado no terminal de A** — é aqui que o 2PC fica visível:

```
[vaijunto] → rotacapixaba POST /transacoes/vaijunto:res-3c590165/preparar L=105 → 200 (5.697 ms)
[vaijunto] → rotafluminense POST /transacoes/vaijunto:res-3c590165/preparar L=105 → 200 (7.870 ms)
[vaijunto] DECISAO tx="vaijunto:res-3c590165" carimbo=(105,vaijunto) → CONFIRMAR
[172.18.0.1:47596] maria    RESERVAR               id="2" → OK (11.065 ms)
[vaijunto] → rotacapixaba POST /transacoes/vaijunto:res-3c590165/confirmar L=108 → 200 (2.453 ms)
[vaijunto] → rotafluminense POST /transacoes/vaijunto:res-3c590165/confirmar L=110 → 200 (2.642 ms)
[vaijunto] DECISAO tx="vaijunto:res-3c590165" → CONCLUIDA
```

**Esperado nos terminais de B e de C**, a mesma transação vista de dono:

```
[rotafluminense] ← vaijunto POST /transacoes/vaijunto:res-3c590165/preparar L=106 tx=vaijunto:res-3c590165 carimbo=(105,vaijunto) voto=SIM → 200 (7.267 ms)
[rotafluminense] ← vaijunto POST /transacoes/vaijunto:res-3c590165/confirmar L=111 tx=vaijunto:res-3c590165 CONFIRMAR situacao=CONFIRMADO → 200 (2.028 ms)
```

**O que apontar.** Três coisas, na tela:

1. **Os dois `preparar` saem em paralelo** e os dois votos voltam SIM. É na
   fase 1, sob o mutex de cada dono, que o assento é descontado — não na fase 2.
2. **`DECISAO … → CONFIRMAR` é o ponto de commit.** Antes dele nada está
   decidido: uma queda de A leva ao abort presumido. Depois, à retomada da
   fase 2.
3. **`RESERVAR → OK` sai antes dos `confirmar`.** A decisão já está no log com
   `fsync`, e os assentos em `PREPARADO` já contam como ocupados; maria não
   precisa esperar a fase 2 terminar.

## D-2 — Último assento disputado entre empresas diferentes

Depois da D-1, resta **um** assento no primeiro trecho de
`rotafluminense:car-1`.

**Onde:** PA2 (`pedro`, máquina A) e PB (`clara`, máquina B).
**Ação:** pedro busca **Salvador → Rio de Janeiro** em `2026-11-05`; clara busca
**Vitória → Rio de Janeiro** em `2026-11-05`. Os dois param em "Reservar
qual?". Na contagem "3, 2, 1", cada um dá Enter na sua máquina.

Os dois itinerários passam pelo mesmo trecho: `rotafluminense:car-1` de Cachoeiro
ao Rio, que tem um assento. O de pedro usa também `vaijunto:car-1` e
`rotacapixaba:car-1`; o de clara, só `rotacapixaba:car-1` — que tem vaga.

**Esperado:** exatamente um confirma. O outro recebe

```
Assento esgotado no trecho 0 da carona rotafluminense:car-1.
```

**O que apontar — é a atomicidade (item 5 do barema).** O perdedor **não** ficou
com o assento da `rotacapixaba`, que estava livre. Na máquina B:

```bash
curl -s http://localhost:8080/auditoria
```

A participação da transação perdedora está `ABORTADO` e `rotacapixaba:car-1` tem
`em_espera: [0,0]`:

```
rotacapixaba:car-1 livres [1,1] confirmados [2,2] em_espera [0,0]
participações: rotacapixaba:res-542c6365 ABORTADO, vaijunto:res-… CONFIRMADO, …
```

Ou todos os trechos, ou nenhum — e o "nenhum" é verificável, não uma afirmação.

**Nota para a arguição.** A ordem das duas linhas de `RESERVAR` no terminal não
prova quem chegou primeiro: a linha é escrita depois que o lock foi liberado. O
que prova a serialização é o resultado — exatamente um `OK`.

## D-3 — Disputa cruzada

Duas caronas de **um assento cada**, em empresas diferentes, pedidas na ordem
inversa por dois passageiros ao mesmo tempo. É o cenário clássico de deadlock
distribuído (item 7 do barema).

**Onde:** PB2 (`paulo`, máquina B) e PC (`tiago`, máquina C).
**Ação:** os dois buscam **Vitória → Nilópolis** em `2026-11-06` e param em
"Reservar qual?". Na contagem, Enter ao mesmo tempo.

O itinerário combina `rotacapixaba:car-2` (beatriz, Vitória → Cachoeiro, 1
assento) e `rotafluminense:car-2` (fernanda, Cachoeiro → Nilópolis, 1 assento).
paulo é coordenado por B e tiago por C, então os dois `PREPARAR` cruzam.

**Esperado:** exatamente um confirma; o outro recebe

```
O assento foi disputado por outra reserva; nenhum assento foi reservado. Tente de novo.
```

(`DISPUTA_PERDIDA`, depois das três tentativas da D36). **Não importa qual
vence** — quem vence é a transação de **carimbo mais velho**, e o carimbo é
atribuído na chegada.

**O que apontar nos terminais.** Os carimbos, que são a resposta ao item 6, e
as três decisões de abortar, que são a resposta ao item 7:

```
[rotafluminense] ← rotacapixaba POST …/res-542c6365/preparar  carimbo=(6,rotacapixaba)  voto=AGUARDE → 200
[rotafluminense] ← rotacapixaba POST …/res-542c6365/preparar  carimbo=(6,rotacapixaba)  voto=NAO motivo=SEM_ASSENTO → 200
[rotafluminense] ← rotacapixaba POST …/res-f59eb636/preparar  carimbo=(20,rotacapixaba) voto=SIM → 200
[rotafluminense] DECISAO tx="rotafluminense:res-f7c35691" carimbo=(22,rotafluminense) → ABORTAR
[rotafluminense] DECISAO tx="rotafluminense:res-4a6accba" carimbo=(22,rotafluminense) → ABORTAR
[rotafluminense] DECISAO tx="rotafluminense:res-248fb240" carimbo=(22,rotafluminense) → ABORTAR
```

`(20,rotacapixaba)` é mais velho que `(22,rotafluminense)`: a mais velha
**espera** (`AGUARDE`) e a mais nova **morre** (`PRECEDENCIA`) — é o wait-die, e
é o que garante que não há espera circular, logo não há deadlock. As três linhas
`ABORTAR` com o mesmo carimbo são as três tentativas da mesma reserva (D36): o
carimbo é o **mesmo** nas três, e é isso que faz uma transação que desistiu
envelhecer em vez de competir de novo em pé de igualdade.

**Honestidade sobre o que o terminal mostra.** `AGUARDE` e `PRECEDENCIA`
aparecem no registro REST só quando o voto perdedor vem de um dono **remoto**.
Se o trecho esgotado for de uma carona da própria empresa do perdedor, o voto é
decidido localmente, sem rede, e não gera linha REST — as três linhas `ABORTAR`
e os carimbos continuam lá. A prova determinística da disputa cruzada é o teste
TR3, com a rede em memória forçando a ordem de chegada; ao vivo, a ordem não é
garantida.

## D-4 — Queda de uma empresa

**Onde:** o tutor derruba a máquina B, ou:

```bash
docker kill rotacapixaba
```

Três coisas, nesta ordem, em PA3 (`lucia`, máquina A):

**(a) A busca volta parcial.** Salvador → Petrópolis em `2026-11-05`:

```
Aviso: busca incompleta — sem resposta de: rotacapixaba.
```

E nenhum itinerário, porque o único que existia tinha perna capixaba. A busca
**não** falha: ela entrega o que ouviu e diz quem não respondeu (D26, D30).

**(b) Reserva com perna capixaba é recusada, sem deixar nada preso:**

```
Uma das empresas do itinerário não respondeu; nenhum assento foi reservado.
```

(`EMPRESA_INDISPONIVEL`, depois do prazo de votação de 5 s.) No terminal de A,
duas linhas `→ rotacapixaba … → sem resposta`.

**(c) Reserva sem perna capixaba funciona normalmente.** lucia reserva
**Feira de Santana → Jequié** em `2026-11-05` (`vaijunto:car-2`, de carlos):
`Reserva vaijunto:res-… confirmada.` A queda de uma empresa não para o
consórcio; para só os passageiros dela (seção 9.7).

**(d) A empresa volta, e o estado volta com ela.** Religar é repetir o mesmo
comando da 1.1:

```
servidor: log /estado/rotacapixaba.wal aberto, 16 registros reaplicados, relógio de Lamport em 33
servidor: recuperação: 0 reserva(s) com a fase 2 a retomar, 0 participação(ões) em PREPARADO com o prazo de dúvida armado, 0 aviso(s) no outbox a entregar
```

`16 registros reaplicados` é o replay (D37, seção 9.2), e `relógio de Lamport em
33` é o relógio restaurado no maior valor do log, para nunca andar para trás. A
auditoria de B mostra as reservas da D-1 e da D-2 ainda `CONFIRMADO`. Repita a
busca do item (a): o itinerário voltou, e `"parcial":false`.

## D-5 — Queda no meio de uma reserva

O caso em que o 2PC bloqueia de verdade (seção 9.6): a decisão de confirmar está
no log do coordenador, e nenhum `CONFIRMAR` saiu.

**(a) Armar o ponto de falha na máquina A.** Pare o servidor de A e suba-o de
novo com a variável, acrescentando ao comando da 1.1:

```bash
docker run --rm --name vaijunto -p 9000:9000 -p 8080:8080 -e TZ=America/Bahia \
  -e FALHAR_EM=coord-apos-decidir \
  -v "$(pwd)/dados/vaijunto:/dados:ro" -v "$(pwd)/estado:/estado" \
  --user "$(id -u):$(id -g)" vaijunto-servidor \
  --empresa vaijunto \
  --pares rotacapixaba=http://$IP_B:8080,rotafluminense=http://$IP_C:8080 \
  --dados /dados --log /estado/vaijunto.wal --auditoria
```

O servidor avisa na subida que vai morrer de propósito:

```
servidor: ATENÇÃO: FALHAR_EM=coord-apos-decidir armado (D31); o servidor vai morrer de propósito neste ponto
```

**(b) Uma reserva derruba o coordenador.** Em PA, com `teste01` / `teste`
(maria já tem reserva no período), reserve Salvador → Cachoeiro de Itapemirim em
`2026-11-05`. O cliente **não recebe resposta**: o processo morreu entre gravar
a decisão e enviar o primeiro `CONFIRMAR`.

```
servidor: FALHAR_EM=coord-apos-decidir (D31): encerrando neste ponto do protocolo
```

**(c) O assento ficou em espera na outra empresa.** Na máquina B:

```bash
curl -s http://localhost:8080/auditoria
```

```
rotacapixaba:car-1 assentos 3 livres [0,0] confirmados [2,2] em_espera [1,1]
em PREPARADO: vaijunto:res-c4db8ba5
```

É o bloqueio residual, ao vivo: B votou SIM, descontou o assento, e **não pode
decidir sozinha** — ninguém sabe se A gravou a decisão antes de cair, e qualquer
escolha unilateral poderia contradizê-la.

**(d) A empresa volta e retoma a fase 2.** Repita o comando da 1.1, **sem**
`FALHAR_EM`:

```
servidor: log /estado/vaijunto.wal aberto, 16 registros reaplicados, relógio de Lamport em 41
servidor: recuperação: 1 reserva(s) com a fase 2 a retomar, 1 participação(ões) em PREPARADO com o prazo de dúvida armado, 0 aviso(s) no outbox a entregar
[vaijunto] → rotacapixaba POST /transacoes/vaijunto:res-c4db8ba5/confirmar L=41 → 200 (2.874 ms)
[vaijunto] DECISAO tx="vaijunto:res-c4db8ba5" → CONCLUIDA
```

**(e) Nenhum assento ficou preso.** A auditoria de B, de novo:

```
rotacapixaba:car-1 assentos 3 livres [0,0] confirmados [3,3] em_espera [0,0]
em PREPARADO: []
```

O assento saiu de `em_espera` e entrou em `confirmados`: a decisão chegou. É
exatamente o que o enunciado pede — nenhum assento bloqueado **após a queda e a
recuperação**.

**O que apontar.** O passageiro perdeu a resposta de uma operação que **deu
certo**, e isso é inerente a sistemas distribuídos (seção 9.5, TF2). O resultado
é observável depois: `2) Minhas reservas` em PA mostra a reserva de `teste01`.

**Cuidado.** Religar **sem** `FALHAR_EM`. Com a variável ainda armada, o próximo
`RESERVAR` mataria o servidor outra vez — é a limitação da fase 6B registrada na
seção 14: a variável é do contêiner, não da execução.

## D-6 — Cancelamento em cascata entre empresas

**Onde:** MC (`marcos`, máquina C).
**Ação:** `4) Cancelar carona`, escolher `rotafluminense:car-1`, confirmar com
`s`.

**Esperado no cliente:**

```
Carona rotafluminense:car-1 cancelada. 2 reservas de passageiro canceladas em cascata.
```

São duas porque a D-1 (maria) e a D-2 (pedro) usavam essa carona — as duas
coordenadas pela `vaijunto`.

**Esperado no terminal de A** — o aviso inverso e o leque do outbox:

```
[vaijunto] ← rotafluminense POST /avisos/carona-cancelada L=44 tx=vaijunto:res-074be78c AVISO_CARONA_CANCELADA situacao_reserva=CANCELADA → 200 (8.536 ms)
[vaijunto] → rotafluminense POST /transacoes/vaijunto:res-074be78c/cancelar L=45 → 200 (3.545 ms)
[vaijunto] → rotacapixaba   POST /transacoes/vaijunto:res-074be78c/cancelar L=45 → 200 (6.000 ms)
```

**O que apontar.**

1. **A direção se inverte.** `/avisos/carona-cancelada` é o único recurso em que
   um participante chama o coordenador, e é por isso que ele fica fora de
   `/transacoes/{tx}` (seção 4.8 da API).
2. **O itinerário é cancelado por inteiro.** Em PA, `2) Minhas reservas` de
   maria e de pedro vem vazio.
3. **Os assentos voltam nas caronas de joao e de rafael**, que nada tinham a ver
   com o cancelamento. A auditoria das três:

```
vaijunto:car-1       livres [2,2] confirmados [1,1] em_espera [0,0] cancelada false
rotacapixaba:car-1   livres [2,2] confirmados [1,1] em_espera [0,0] cancelada false
rotafluminense:car-1 livres [2,2] confirmados [0,0] em_espera [0,0] cancelada true
outbox das três: []
```

4. **O cancelamento não pode ser recusado por ninguém**, então ele não é 2PC:
   basta **entrega confiável** da decisão. Se A estivesse fora do ar, o aviso
   ficaria no outbox de C e seria entregue na volta — e até lá os assentos de
   joao e de rafael continuariam ocupados. A propagação é conservadora: o
   assento volta **depois**, nunca antes, e por isso nunca causa venda dupla
   (seção 10.3).

## D-7 — Abort presumido com `curl`

Mostra a terminação cooperativa **sem derrubar nenhum servidor**, e deixa claro
que entre os servidores só há HTTP e JSON. É a seção 7 do `API_CONSORCIO.md`.

**(a) Um `PREPARAR` à mão, da máquina C, fingindo ser a `vaijunto`:**

```bash
curl -s -i -X POST http://$IP_B:8080/transacoes/vaijunto:res-0000abcd/preparar \
  -H 'Content-Type: application/json' \
  -H 'X-VaiJunto-Empresa: vaijunto' \
  -H 'X-VaiJunto-Lamport: 100' \
  -d '{
    "coordenador": "vaijunto",
    "participantes": ["vaijunto", "rotacapixaba"],
    "carimbo": {"lamport": 100, "empresa": "vaijunto"},
    "passageiro": {"ref": "vaijunto:maria", "nome": "Maria Souza"},
    "itens": [{"carona_id": "rotacapixaba:car-1", "de": 0, "ate": 2}]
  }'
```

```
HTTP/1.1 200 OK
X-Vaijunto-Empresa: rotacapixaba
X-Vaijunto-Lamport: 101

{"voto":"SIM","pernas":[{"carona_id":"rotacapixaba:car-1","empresa":"rotacapixaba","motorista_nome":"Rafael Mendonça Pires","de":0,"ate":2,"cidades":["Vitória da Conquista","Vitória","Cachoeiro de Itapemirim"],"partida":"2026-11-05T14:00:00-03:00","chegada":"2026-11-06T06:30:00-03:00","preco_centavos":21500}]}
```

O `X-Vaijunto-Lamport: 101` é a regra de recebimento: `max(L, 100) + 1`. O Go
canoniza os nomes de cabeçalho, daí a grafia `X-Vaijunto`.

**(b) O assento ficou em espera.** `curl -s http://$IP_B:8080/auditoria` mostra
`em_espera: [1,1]` e a participação em `PREPARADO`. Mas a transação
`vaijunto:res-0000abcd` **nunca existiu** na `vaijunto`.

**(c) A própria B resolve, sozinha, em 10 s.** Passado o prazo de dúvida, ela
pergunta ao coordenador e a resposta é o abort presumido:

```
[rotacapixaba] → vaijunto POST /transacoes/vaijunto:res-0000abcd/consultar L=101 → 200 (3.541 ms)
[rotacapixaba] DECISAO tx="vaijunto:res-0000abcd" carimbo=(100,vaijunto) → TERMINACAO → ABORTADO por coordenador
```

E a auditoria volta a `livres [3,3] em_espera [0,0]`, com a participação
`ABORTADO`. **O assento voltou sozinho.**

**(d) A mesma pergunta, à mão**, para mostrar o que B perguntou:

```bash
curl -s -X POST http://$IP_A:8080/transacoes/vaijunto:res-0000abcd/consultar \
  -H 'Content-Type: application/json' \
  -H 'X-VaiJunto-Empresa: rotacapixaba' \
  -H 'X-VaiJunto-Lamport: 102' -d '{}'
```

```json
{"papel":"COORDENADOR","decisao":"ABORTADA"}
```

A `vaijunto` não encontra a transação nem na memória nem no log, e responde
`ABORTADA` **sem gravar nada** (D23). Não gravar o abort é o que torna o
protocolo econômico: o caso comum — a transação que ninguém mais procura — não
custa um `fsync`.

## Estado dos assentos ao longo da demonstração

Assentos livres por trecho, para responder perguntas sem abrir a auditoria:

| Depois de | vj:car-1 | vj:car-2 | rc:car-1 | rc:car-2 | rf:car-1 | rf:car-2 |
|---|---|---|---|---|---|---|
| início | [3,3] | [2] | [3,3] | [1,1] | [2,2] | [1,1] |
| D-1 (maria) | [2,2] | [2] | [2,2] | [1,1] | [1,1] | [1,1] |
| D-2 (um vence) | [1,1] | [2] | [1,1] | [1,1] | [0,1] | [1,1] |
| D-3 (um vence) | [1,1] | [2] | [1,1] | [0,1] | [0,1] | [0,1] |
| D-4c (lucia) | [1,1] | [1] | [1,1] | [0,1] | [0,1] | [0,1] |
| D-5 (teste01) | [0,0] | [1] | [0,0] | [0,1] | [0,1] | [0,1] |
| D-6 (cascata) | [2,2] | [1] | [2,2] | [0,1] | cancelada | [0,1] |

Em toda linha e para toda carona vale `livres + confirmados + em espera ==
assentos` — é a I-P2-1, e é o que `GET /auditoria` permite conferir linha a
linha.

## Mapa para o barema

| Item | Onde aparece |
|---|---|
| 1 Arquitetura | Parte 1 inteira: duas portas, três WALs, nenhum componente central |
| 2 Comunicação (API REST) | D-1 (recursos e códigos), D-7 (HTTP cru), D-4b (requisição sem resposta) |
| 3 Busca Distribuída | 1.3 e D-4a |
| 4 Exclusão Mútua | D-2 e D-3: um `OK` por assento, decidido sob o mutex do dono na fase 1 |
| 5 Atomicidade Distribuída | D-1 e D-2c: o perdedor não fica com a perna que tinha vaga |
| 6 Precedência | D-3: os carimbos `(lamport, empresa)` no terminal |
| 7 Deadlock Distribuído | D-3: wait-die, `AGUARDE` × `PRECEDENCIA`, três tentativas |
| 8 Tolerância a Falhas | D-4 (antes), D-5 (durante), D-6 (depois) |
| 9 Recuperação e Persistência | D-4d e D-5d: replay, relógio restaurado, fase 2 retomada |
| 10 Testes | Parte 3, mais a seção 11 do `PROJETO_P2.md` |
| 11 Emulação | Partes 0, 1 e 4 |

**Salve os registros antes de parar os servidores.** Com `--rm`, o `Ctrl+C` leva
o `docker logs` junto. Em outro terminal de cada máquina:

```bash
docker logs vaijunto > registro-vaijunto.txt 2>&1
```

---

# Parte 3: medição com os servidores em máquinas distintas

A seção 11.6 do `PROJETO_P2.md` pede três configurações. As duas primeiras —
reserva só local e reserva federada, as duas numa máquina — já estão medidas e
no documento. A terceira é esta, e só o laboratório a produz: **a mesma rodada
com os servidores em máquinas distintas**.

## 3.1 Regras da medição

- **Registro de operações desligado.** Cada linha é uma escrita síncrona no
  terminal, que entra na latência e disputa com o mutex do estado (D19).
- **`estado/` limpo antes de cada cenário.** O log cresce indefinidamente, e com
  ele crescem o replay do boot e a verificação de sobreposição. Só rodadas com o
  mesmo ponto de partida são comparáveis.
- **Mesma duração** das rodadas já medidas: 5 s por ponto.
- **Sem `-race`**, nem no simulador nem nos servidores. O `/bin/simulador` da
  imagem já é compilado sem ele.
- **Nenhum cliente manual aberto**, e uma carga por vez.

## 3.2 Subir as três para medir

Nas três máquinas, pare o servidor e repita o comando da 1.1 com uma mudança
só: acrescente `--log-operacoes=false`. O `--auditoria` **continua**, porque a
verificação de invariantes da 3.4 depende dele; ele é somente leitura e não é
consultado durante a carga, então não entra na medição.

```bash
rm -f estado/*.wal          # nas três máquinas, antes de cada cenário
docker run --rm --name vaijunto -p 9000:9000 -p 8080:8080 -e TZ=America/Bahia \
  -v "$(pwd)/dados/vaijunto:/dados:ro" -v "$(pwd)/estado:/estado" \
  --user "$(id -u):$(id -g)" vaijunto-servidor \
  --empresa vaijunto \
  --pares rotacapixaba=http://$IP_B:8080,rotafluminense=http://$IP_C:8080 \
  --dados /dados --log /estado/vaijunto.wal --auditoria --log-operacoes=false
```

Esperado: `(pares: 2, registro de operações: false)`. **Durante a carga, os três
terminais devem ficar parados.**

## 3.3 Rodar o simulador

De uma máquina só — A serve —, apontando para as três pelos IPs:

```bash
mkdir -p resultados
docker run --rm --user "$(id -u):$(id -g)" -e TZ=America/Bahia \
  -v "$(pwd)/resultados:/resultados" vaijunto-cliente /bin/simulador \
  --empresas=vaijunto=$IP_A:9000,rotacapixaba=$IP_B:9000,rotafluminense=$IP_C:9000 \
  --saida=/resultados --rotulo=tres-maquinas \
  --cenario=federado --curva=1,10,50,100 --duracao=5s
```

O número de passageiros é o **total no consórcio**, repartido entre as três (100
vira 34, 33 e 33): é o que mantém o eixo das curvas igual ao do P1 e ao das duas
rodadas já medidas. A linha de operação `total` do CSV é a diretamente
comparável.

Repita com `--cenario=local --rotulo=tres-maquinas-local`, apagando `estado/`
antes, para ter o par de curvas da mesma máquina física. A diferença entre as
duas é o custo da coordenação distribuída — rede mais `fsync`.

Saem dois CSVs em `resultados/`: `simulador-tres-maquinas-<instante>.csv`, com
p50, p95 e p99 por operação e a vazão, e `…-codigos.csv`, com a contagem por
código de resposta.

## 3.4 Verificar as invariantes depois da carga

O simulador fala **só TCP**: ele é um cliente, e conferir as invariantes é
trabalho de quem observa as três de fora. Daí serem dois comandos. De uma
máquina com Go:

```bash
VAIJUNTO_AUDITORIA=vaijunto=http://$IP_A:8080,rotacapixaba=http://$IP_B:8080,rotafluminense=http://$IP_C:8080 \
  go test -run TestInvariantesDoAmbiente ./testes -v
```

Ele espera a quiescência (até 90 s) e confere I-P2-1 a I-P2-5 nas três empresas.

## 3.5 Coleta para o relatório

**Recolha:**

- os CSVs de `resultados/`, das duas rodadas desta parte;
- os registros dos três servidores salvos na Parte 2;
- fotos ou capturas de D-2c, D-5c, D-5e e D-6.

**Anote:**

| Item | Valor |
|---|---|
| Data e hora | |
| Commit (`git log -1 --oneline`) | |
| IPs de A, B e C | |
| Sistema e processador de cada máquina | |
| Rede: cabo ou Wi-Fi, e switch | |

**Marque os resultados:**

| Passo | Esperado | Observado |
|---|---|---|
| 1.3 | `/saude` responde nas três; busca com `"parcial":false` | |
| D-1 | 1 itinerário, R$ 430,00, `DECISAO → CONFIRMAR` no terminal | |
| D-2 | 1 `OK` e 1 `SEM_ASSENTO`; perdedor com a perna capixaba em `ABORTADO` | |
| D-3 | 1 `OK` e 1 `DISPUTA_PERDIDA`; carimbos visíveis; sem travar | |
| D-4 | Busca parcial, `EMPRESA_INDISPONIVEL`, reserva local normal, replay na volta | |
| D-5 | `em_espera` 1 durante a queda, 0 depois da volta; fase 2 retomada | |
| D-6 | 2 reservas canceladas; assentos devolvidos nas outras duas empresas; outbox vazio | |
| D-7 | `decisao: ABORTADA` e assento devolvido sozinho | |
| 3.3 | 2 rodadas, 4 pontos cada, CSVs em `resultados/` | |
| 3.4 | `TestInvariantesDoAmbiente` PASS | |

---

# Parte 4: quando o tutor derrubar uma empresa

O enunciado manda demonstrar o sistema **sob a falha de um servidor**, então
isso vai acontecer. Não é incidente: é a D-4 fora de hora.

## 4.1 Primeiro, identifique qual caiu

```bash
curl -s --max-time 2 http://$IP_A:8080/saude; echo
curl -s --max-time 2 http://$IP_B:8080/saude; echo
curl -s --max-time 2 http://$IP_C:8080/saude; echo
```

Quem não responde é quem caiu. Os servidores de pé registram a tentativa de
falar com ela:

```
[vaijunto] → rotacapixaba POST /transacoes/… → sem resposta (2000.431 ms)
```

A detecção é por **timeout** (D30), e só por timeout: não há *heartbeat* nem
monitor. Cada operação tem o seu caminho seguro, e é por isso que a queda nunca
é erro fatal.

## 4.2 O que dizer, com a empresa ainda fora

A tabela da seção 9.7, que é a resposta ao item 8 do barema:

| Operação | Comportamento |
|---|---|
| Busca em outra empresa | Resultado parcial, sem as caronas dela, avisando a indisponibilidade |
| Reserva sem perna dela | Normal |
| Reserva com perna dela | `EMPRESA_INDISPONIVEL` após o prazo de votação; **nenhum assento fica preso** |
| Passageiros e motoristas dela | Sem serviço: eles só se conectam a ela |
| Cancelamento que precisa avisá-la | Fica no outbox e é entregue quando ela volta |
| Assento de outra empresa em `PREPARADO` por transação que ela coordenava | Bloqueio residual (seção 9.6), até ela voltar |

E mostre, não só diga: faça a busca parcial e a reserva local nas duas que
sobraram, como na D-4.

## 4.3 Religar

Repita **o mesmo comando** da seção 1.1 na máquina derrubada — sem nenhuma flag
nova, sem `FALHAR_EM`, e sem apagar `estado/`. O boot é a recuperação
(seção 9.1): o servidor não tem rotina de desligamento, `docker stop` e
`docker kill` têm o mesmo efeito do ponto de vista do protocolo, e todo boot
passa pelo mesmo caminho.

Aponte as três linhas do boot:

```
servidor: log /estado/rotacapixaba.wal aberto, 16 registros reaplicados, relógio de Lamport em 33
servidor: empresa rotacapixaba atendendo clientes em [::]:9000 e pares em [::]:8080 (pares: 2, registro de operações: true)
servidor: recuperação: 0 reserva(s) com a fase 2 a retomar, 0 participação(ões) em PREPARADO com o prazo de dúvida armado, 0 aviso(s) no outbox a entregar
```

- **`N registros reaplicados`**: o estado veio do log, aplicado sem revalidar
  (D37). A semente é recarregada primeiro, e o log vai por cima.
- **`relógio de Lamport em L`**: restaurado no maior valor do log, para nunca
  andar para trás — sem isso, duas transações distintas apareceriam como
  simultâneas na ordem da seção 8.1.
- **`recuperação: …`**: as três tarefas da seção 9.3. Números diferentes de zero
  são a recuperação trabalhando, não um problema.

## 4.4 Provar que nada ficou preso

É a afirmação que o enunciado cobra. Nas três máquinas:

```bash
curl -s http://localhost:8080/auditoria
```

Procure, em cada uma:

- **`em_espera` zerado** em toda carona — nenhuma participação ficou em
  `PREPARADO` (I-P2-3);
- **`outbox: []`** — todo aviso de cancelamento foi entregue (I-P2-3);
- **`livres + confirmados + em_espera == assentos`** em toda linha (I-P2-1).

Se houver Go numa das máquinas, o verificador faz as cinco invariantes de uma
vez, com o comando da 3.4.

Depois, mostre que a empresa voltou a **servir**, e não apenas a responder:
repita a busca Salvador → Petrópolis, que precisa voltar com `"parcial":false`,
e faça uma reserva federada nova. Uma empresa que religasse e recusasse tudo
passaria em todas as invariantes — é o mesmo argumento do passo 5 do TS1
(seção 11.5).

## 4.5 Se ele derrubar no meio de uma reserva

É a D-5 sem aviso. O passageiro fica sem resposta, e o assento fica em
`em_espera` nas outras empresas. Diga as três coisas, nesta ordem:

1. **Se a queda foi antes do ponto de commit**, nada foi decidido: o abort
   presumido devolve os assentos, e quem não recebeu o `abortar` descobre
   perguntando (D-7).
2. **Se foi depois**, a decisão está no log: na volta, a fase 2 é retomada e
   todos confirmam. O passageiro perdeu a resposta de uma operação que deu
   certo, e a encontra em `Minhas reservas`.
3. **Enquanto ela está fora**, os assentos daquela transação ficam presos nas
   outras empresas, e isso é o limite conhecido do 2PC (seção 9.6) — assumido,
   delimitado a uma transação, e resolvido na volta. Evitá-lo exigiria replicar
   o coordenador, como no Paxos Commit (seção 15).

---

# Se algo der errado

| Sintoma | Causa provável | O que fazer |
|---|---|---|
| Busca sempre `"parcial":true` | Porta **8080** fechada no firewall, ou `--pares` com IP errado | Teste `curl http://$IP_B:8080/saude` da outra máquina. Confira `pares: 2` na linha de boot |
| Toda reserva federada dá `EMPRESA_INDISPONIVEL` | O mesmo de cima | Idem. O sintoma é o mesmo de uma empresa fora do ar, de propósito (D30) |
| `pares: 0` no boot | `--pares` vazio ou malformado | Formato é `empresa=url,empresa=url`, com `http://` e a porta |
| `--empresa é obrigatória` | Faltou a flag | Sem default de propósito: uma empresa que sobe com o nome de outra geraria IDs com prefixo errado (D27) |
| O cliente não conecta | IP errado, servidor parado ou porta 9000 fechada | `docker ps` precisa mostrar `0.0.0.0:9000->9000/tcp` e `0.0.0.0:8080->8080/tcp` |
| O cliente fecha logo ao abrir | Faltou `-it` | `docker run --rm -it …` |
| Porta 9000 ou 8080 ocupada | Outro processo na máquina | Suba com `-p 9100:9000` e use `$IP_A:9100` nos clientes; para a 8080, mude também o `--pares` **das outras duas** |
| `permission denied` ao abrir o log | `.wal` com dono `root`, de uma execução sem `--user` | `sudo rm -f estado/*.wal` e suba com `--user "$(id -u):$(id -g)"` |
| `RESERVA_EM_ANDAMENTO` | O passageiro já tem um `RESERVAR` em curso (D35) | Espere o prazo de votação (5 s) e repita |
| `CONFLITO_HORARIO` | O passageiro já tem reserva que se sobrepõe ao período | Use outro usuário da tabela 1.4; é o motivo de `lucia` ficar reservada à D-4 |
| `CARONA_EM_TRANSACAO` na D-6 | Alguma participação em `PREPARADO` usa a carona (D39) | Espere alguns segundos e repita: é a disputa de outra demonstração terminando |
| Cancelamento recusado por prazo | Passou de 06/11/2026 08:00 (carona) ou 04/11 04:00 (reserva) | Seção 0.5. Pule a D-6 e registre o motivo |
| O servidor morre sozinho ao reservar | `FALHAR_EM` ainda armado de um ensaio da D-5 | Religue **sem** a variável; ela é do contêiner, não da execução |
| Busca sem o itinerário de R$ 430,00 | `estado/` com caronas de um ensaio anterior, ou carona cancelada na D-6 | `rm -f estado/*.wal` nas três e suba de novo: a semente é recarregada (D37) |
| Horários 3 h deslocados | Servidor sem `-e TZ=America/Bahia` | Não afeta o domínio — o fuso das cidades é propriedade dele —, só as linhas de boot. Reinicie com a variável |
| Nada aparece no terminal durante a carga | Correto: a Parte 3 roda com `--log-operacoes=false` | Se aparecer, reinicie com a flag e refaça a rodada |
| A rede do laboratório falha de vez | — | Rode as três empresas numa máquina com `docker compose up -d` e siga o mesmo roteiro pelas portas 9001–9003 e 8081–8083. As disputas perdem o efeito de três máquinas, mas continuam válidas |
| Registro perdido | O servidor foi reiniciado antes do `docker logs` | Refaça a parte, ou use as capturas de tela |
