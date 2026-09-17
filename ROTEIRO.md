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

**2. Abra os clientes e faça login**, com `export IP_A=…` em cada terminal:

| Máquina | Terminal | Cliente | Usuário |
|---|---|---|---|
| A | servidor | — | — |
| A | PA | passageiro | `pedro` / `abcd` |
| B | PB | passageiro | `lucia` / `abcd` |
| B | MB | motorista | `joao` / `1234` (troca para `ana` só no D7) |

```bash
docker run --rm -it -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/passageiro   # PA (em A) e PB (em B)
docker run --rm -it -e VAIJUNTO_SERVIDOR=$IP_A:9000 vaijunto-cliente /bin/motorista    # MB (em B)
```

Na máquina A, `IP_A` é o IP da própria máquina: o cliente de A também passa pela
porta publicada. Deixar as sessões abertas é seguro: o servidor não tem prazo de
leitura, e o prazo do cliente só conta durante uma requisição.

**Por que esses usuários.**

- **joao** é dono da `car-1`, que está em todos os itinerários com baldeação:
  publica, detalha passageiros por trecho e cancela em cascata sem trocar de
  usuário.
- **pedro** e **lucia** começam sem reservas, então disputam o mesmo itinerário
  sem esbarrar em `CONFLITO_HORARIO`.

**3. Conferências.**

- `docs/apresentacao.html` aberto na capa.
- Relógio antes de **01/10/2026 às 05:00** (prazo de cancelamento das caronas do
  cenário).
- Combinado quem aperta o outro Enter no D3 (um colega ou o professor).
- No ensaio, confirmado que **Ctrl+C** encerra o cliente passageiro no contêiner.
  Se não encerrar, no D6 feche a janela do terminal.

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
| D2 | **8** | MB · joao | Publicar carona: Salvador `2026-10-05` 08:00 → Feira de Santana 10:00, 2 assentos, R$ 30,00. Carona só para demonstrar a publicação: a data 05/10 a mantém fora da busca de 01/10 | `Carona car-… publicada, com 2 assentos.` |
| D3 | **5, 6, 7** | PA · pedro e PB · lucia | Os dois buscam Salvador → Vitória da Conquista em `2026-10-01` e param em “Reservar qual?”. Na contagem “3, 2, 1”, cada um digita `4` e Enter na sua máquina. A disputa é pelo único assento da `car-4` (Feira de Santana → Jequié, motorista ana), a perna do meio do [4] | 4 itinerários, na ordem da tela 5. **Um** recebe `Reserva … confirmada`; o **outro** recebe `Assento esgotado no trecho Feira de Santana → Jequié.` No servidor: um `OK` e um `ERRO SEM_ASSENTO` |
| D4 | **7, 8** | MB · joao | Detalhar carona `car-1` | Salvador → Feira de Santana: `2 assentos livres`, **só o vencedor** listado. Feira de Santana → Jequié: `3 assentos livres`. O perdedor não ficou com assento na `car-1`, que tinha vaga: **nada foi reservado pela metade** |
| D5 | **8** | vencedor do D3 | `Minhas reservas`, depois `Cancelar reserva`, confirmando com `s` | A reserva aparece na lista; depois `Reserva … cancelada. Os assentos voltaram para os trechos.` |
| D6 | **9** | PB · lucia, depois PA · pedro | lucia busca de novo e para em “Reservar qual?”, vendo o [4], que usa o último assento da `car-4`. **Ctrl+C** no PB. pedro busca e reserva o **[4]** | A reserva do pedro é **confirmada**, com o assento que a lucia via. No servidor, a conexão da lucia termina sem nenhum `RESERVAR` |
| D7 | **7, 8** | MB · joao, depois ana | joao cancela `car-1`, confirmando com `s`. `Sair`, reabrir o motorista, entrar como `ana` e detalhar `car-4` | `Carona car-1 cancelada. 1 reserva de passageiro cancelada em cascata.` Depois, `car-4` com `1 assento livre` e nenhum passageiro: o assento disputado voltou numa carona de **outro** motorista |

O item 10 (Testes) é coberto pela tela do HTML. O item 1 (Arquitetura) aparece
em toda a demonstração.

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

---

## Se algo der errado

| Sintoma | O que fazer |
|---|---|
| O cliente não conecta | Confira `IP_A` e se `docker ps` em A mostra `0.0.0.0:9000->9000/tcp`. Libere a porta 9000/tcp no firewall |
| O cliente fecha logo ao abrir | Faltou `-it` |
| Busca com menos de 4 itinerários | O servidor não estava limpo: reinicie, reabra os clientes e recomece do D1 |
| Ctrl+C não encerra o cliente | Feche a janela do terminal PB |
| A rede do laboratório falha | Rode todos os clientes na máquina A. A disputa do D3 perde o efeito de duas máquinas, mas continua válida |
| Endereço no registro do servidor aparece como `172.17.0.1` | É o Docker encaminhando a porta. Identifique o cliente pelo usuário da linha |
