# Roteiro da apresentação

Duas partes em sequência: primeiro as 11 telas de `docs/apresentacao.html`, na
ordem, e depois a demonstração ao vivo, sem interrupções. Total de cerca de
16 minutos.

---

## Antes de entrar na sala (fora do tempo)

**Nas duas máquinas:** imagens montadas (`vaijunto-servidor` em A;
`vaijunto-cliente` em A **e** em B) e repositório no mesmo commit.

**1. Máquina A.** Anote o IP (`hostname -I`) e suba o servidor **recém-iniciado**
num terminal visível:

```bash
docker run --rm --name vaijunto-servidor -p 9000:9000 -e TZ=America/Bahia \
  -v "$(pwd)/dados:/dados" vaijunto-servidor \
  --usuarios /dados/usuarios.json --caronas /dados/caronas.json
```

**2. Abra os clientes e faça login em todos**, com `export IP_A=…` em cada
terminal:

| Máquina | Terminal | Cliente | Usuário |
|---|---|---|---|
| A | A1 | passageiro | `maria` / `abcd` |
| A | A2 | passageiro | `pedro` / `abcd` |
| B | B1 | motorista | `ana` / `1234` |
| B | B2 | motorista | `joao` / `1234` |
| B | B3 | motorista | `carlos` / `1234` |
| B | B4 | passageiro, **com nome** | `lucia` / `abcd` |
| B | B5 | shell livre | para o `docker kill` |

```bash
docker run --rm -it -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/passageiro                       # A1, A2
docker run --rm -it -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/motorista                        # B1, B2, B3
docker run --rm -it --name cliente-queda -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/passageiro  # B4
```

Na máquina A, `IP_A` é o IP da própria máquina: os clientes de A também passam
pela porta publicada.

Deixar as sessões abertas é seguro: o servidor não tem prazo de leitura, e o
prazo do cliente só conta durante uma requisição.

**3. Conferências.**

- `docs/apresentacao.html` aberto na capa.
- Relógio antes de **01/10/2026 às 05:00** (prazo de cancelamento das caronas do
  cenário).
- Combinado quem aperta o outro Enter no D4 (um colega ou o professor).

---

## Parte 1: apresentação do HTML (cerca de 10 min)

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

## Parte 2: demonstração ao vivo (cerca de 6 min)

| # | Barema | Terminal · usuário | Ação | Esperado |
|---|---|---|---|---|
| D1 | **2, 11** | A: servidor | Mostrar o terminal do servidor | Linhas `LOGIN → OK` vindas dos clientes de A e de B |
| D2 | **8** | B1 · ana | Publicar carona: Salvador `2026-10-05` 08:00 → Feira de Santana 10:00, 2 assentos, R$ 30,00 | `Carona car-… publicada, com 2 assentos.` |
| D3 | **5, 8** | A1 · maria | Buscar Salvador → Vitória da Conquista, `2026-10-01`, e reservar o **[3]** | 4 itinerários, na ordem da tela 5. Depois `Reserva … confirmada. Total: R$ 100,00` |
| D4 | **6, 7** | A2 · pedro e B4 · lucia | Os dois fazem a mesma busca e param em “Reservar qual?”. Na contagem “3, 2, 1”, cada um digita `4` e Enter na sua máquina | **Um** recebe `Reserva … confirmada`. O **outro** recebe `Assento esgotado no trecho Feira de Santana → Jequié.` No servidor: um `OK` e um `ERRO SEM_ASSENTO` |
| D5 | **8** | B1 · ana, depois o vencedor do D4 | ana detalha `car-4`. O vencedor cancela a reserva | `0 assentos livres`, com o vencedor listado. Depois `Reserva … cancelada. Os assentos voltaram para os trechos.` |
| D6 | **9** | B4 · lucia, B5, A2 · pedro | lucia busca de novo e para em “Reservar qual?”. Em B5: `docker kill cliente-queda`. pedro busca e reserva o **[4]** | A reserva do pedro é **confirmada**, com o assento que a lucia via. No servidor, a conexão da lucia termina sem nenhum `RESERVAR` |
| D7 | **7, 8** | B2 · joao, B3 · carlos | joao cancela `car-1`. carlos detalha `car-2` | `Carona car-1 cancelada. 2 reservas de passageiros canceladas em cascata.` e `car-2` com `2 assentos livres`: o assento voltou numa carona de **outro** motorista |

O item 10 (Testes) é coberto pela tela do HTML. O item 1 (Arquitetura) aparece
em toda a demonstração.

**Sobre o D4.** Não importa quem vence: o vencedor cancela no próprio terminal no
D5 e continua logado, e no D6 a lucia está sempre em B4 e o pedro sempre em A2. O
resultado também não depende de os dois Enter saírem no mesmo milissegundo: o
servidor serializa as confirmações, e quem chega depois encontra o assento
ocupado.

**Estado ao longo da demonstração** (assentos livres por trecho, para responder
perguntas):

| Depois de | car-1 | car-2 | car-4 |
|---|---|---|---|
| início | [3, 3] | [2] | [1] |
| D3 (maria reserva o #3) | [2, 2] | [1] | [1] |
| D4 (disputa pelo #4) | [1, 2] | [0] | [0] |
| D5 (vencedor cancela) | [2, 2] | [1] | [1] |
| D6 (pedro reserva o #4) | [1, 2] | [0] | [0] |
| D7 (cascata) | cancelada | [2] | [1] |

---

## Se algo der errado

| Sintoma | O que fazer |
|---|---|
| O cliente não conecta | Confira `IP_A` e se `docker ps` em A mostra `0.0.0.0:9000->9000/tcp`. Libere a porta 9000/tcp no firewall |
| O cliente fecha logo ao abrir | Faltou `-it` |
| Busca com menos de 4 itinerários | O servidor não estava limpo: reinicie, reabra os clientes e recomece do D1 |
| A rede do laboratório falha | Rode todos os clientes na máquina A. A disputa do D4 perde o efeito de duas máquinas, mas continua válida |
| Endereço no registro do servidor aparece como `172.17.0.1` | É o Docker encaminhando a porta. Identifique o cliente pelo usuário da linha |
