# VAIJUNTO — Documento de projeto

Problema 1 de TEC502 (Concorrência e Conectividade). Sistema de caronas
compartilhadas de média e longa distância, com servidor central e clientes
conectados por sockets TCP.

Este documento consolida os requisitos, as decisões de projeto e os algoritmos.
A especificação do protocolo de aplicação está em `PROTOCOL.md`.

---

## 1. Escopo

Uma startup mantém um servidor central onde motoristas publicam caronas e
passageiros consultam e reservam assentos. O motorista define a rota; a
plataforma apenas descobre e coordena os encontros possíveis entre uma oferta
existente e uma demanda dispersa.

Três propriedades definem a dificuldade técnica:

1. A disponibilidade de assentos é controlada **por trecho**, não pela carona
   inteira. Um assento ocupado entre a primeira e a segunda cidade permanece
   livre nos trechos seguintes.
2. Um itinerário pode combinar trechos de caronas de **motoristas diferentes**.
3. A confirmação de um itinerário é **atômica**: ou todos os trechos são
   reservados, ou nenhum.

---

## 2. Requisitos

### 2.1 Funcionais

| ID | Requisito |
|---|---|
| RF01 | Autenticar usuário, distinguindo perfil motorista e passageiro |
| RF02 | Motorista publica carona: rota, data/hora de partida, assentos, preço por trecho |
| RF03 | Motorista consulta as caronas que publicou |
| RF04 | Motorista consulta os passageiros confirmados em cada trecho |
| RF05 | Motorista cancela uma carona |
| RF06 | Passageiro busca itinerários por origem, destino e data |
| RF07 | Um itinerário pode combinar trechos de caronas de motoristas diferentes |
| RF08 | Passageiro confirma a reserva de um itinerário de forma atômica |
| RF09 | Passageiro consulta suas reservas |
| RF10 | Passageiro cancela uma reserva, devolvendo os assentos |
| RF11 | Disponibilidade controlada por trecho, não pela carona inteira |

### 2.2 Não funcionais

| ID | Requisito |
|---|---|
| RNF01 | Comunicação via socket TCP nativo, sem framework de RPC ou mensageria |
| RNF02 | Representação intermediária bem definida (JSON), com validação e descarte de mensagens malformadas |
| RNF03 | Servidor atende múltiplos clientes simultâneos |
| RNF04 | Queda abrupta de cliente não interrompe o serviço nem corrompe o estado |
| RNF05 | Nunca confirmar o mesmo assento do mesmo trecho para dois passageiros |
| RNF06 | Confirmação de itinerário é tudo-ou-nada |
| RNF07 | Nenhum assento fica permanentemente bloqueado por reserva não concluída |
| RNF08 | Controle de concorrência na solução, sem SGBD ou coordenador externo |
| RNF09 | Backend empacotado e executado em contêiner Docker |
| RNF10 | Servidor central único, sem réplicas |
| RNF11 | Servidor e clientes em computadores distintos |

---

## 3. Decisões de projeto

Cada decisão traz a justificativa, porque elas são o conteúdo central do
relatório SBC.

### D01 — Linguagem: Go

Biblioteca padrão com socket TCP nativo (`net`), serialização JSON
(`encoding/json`), primitivas de concorrência (`sync`) e detector de corrida
(`go test -race`). Compila para binário estático, o que torna a imagem Docker
mínima. Nenhuma dependência externa é necessária, o que atende diretamente à
restrição de não usar framework de mensageria ou RPC.

### D02 — Estado apenas em memória

Sem banco de dados. O enunciado proíbe delegar o controle de concorrência a um
SGBD, e persistir em disco só acrescentaria uma fonte de complexidade sem
relação com o problema estudado. O estado inicial é carregado de arquivos JSON
no boot.

### D03 — Uma goroutine por conexão

Modelo *thread-per-connection*. É o mapeamento mais direto do modelo mental de
"um cliente, uma sessão" e o mais simples de raciocinar. Goroutines custam
poucos KB, então centenas de clientes simultâneos não são problema.

### D04 — Um mutex global sobre o estado

`sync.Mutex` único protegendo todo o estado de domínio. É a solução correta mais
simples para RNF05 e RNF06. Granularidade fina (lock por carona) traria risco de
*deadlock* na reserva multi-carona, sem ganho mensurável nesta escala.

Evolução prevista: trocar por `sync.RWMutex` após o sistema funcionar, com
leitura compartilhada na busca e escrita exclusiva na reserva, e comparar as duas
versões com os números do teste de carga.

### D05 — Só a camada de estado conhece o lock

As funções de domínio recebem o estado já travado e nunca adquirem o mutex por
conta própria. Isso elimina por construção o risco de reentrância no mesmo mutex
por caminhos diferentes.

### D06 — JSON delimitado por quebra de linha

Uma mensagem JSON por linha, terminada por `\n`. Atende RNF02, é depurável com
`nc` ou `telnet`, e é seguro porque `encoding/json` escapa quebras de linha
dentro de strings, tornando impossível um delimitador falso vindo do conteúdo.

### D07 — Sem reserva em duas fases

**Não existe estado intermediário de "assento em espera".** A busca é puramente
informativa: lê a disponibilidade para podar resultados inviáveis, mas não
bloqueia nada e não promete nada. Toda a validação e a escrita acontecem juntas,
na confirmação, dentro de uma única seção crítica.

Consequência: RNF07 é satisfeito por construção. Não existe reserva pendente que
possa vazar, portanto não é preciso *timeout*, varredura periódica ou compensação.
Esta é a decisão mais importante do projeto.

### D08 — Identidade ligada à conexão, sem token de sessão

Após o `LOGIN`, o usuário autenticado fica em uma variável local da goroutine que
atende a conexão. Nenhuma requisição posterior carrega credencial ou
identificador de usuário.

Justificativa: token de sessão existe para resolver a ausência de estado no HTTP,
onde cada requisição pode chegar por uma conexão diferente. Com conexão TCP
persistente, a associação conexão ↔ usuário já é natural, e um token apenas
reimplementaria por cima o que o transporte oferece. Quando a conexão cai, a
goroutine termina e a sessão desaparece sozinha, sem tabela de sessões, expiração
ou coleta de tokens vencidos.

### D09 — Corredor fixo de cidades

O universo do sistema é uma sequência fixa de 4 cidades com durações de trajeto
constantes entre vizinhas. O motorista informa origem, destino e instante de
partida; o servidor deriva a rota e todos os horários.

| Índice | Cidade | Duração até a próxima |
|---|---|---|
| 0 | Salvador | 2h00 |
| 1 | Feira de Santana | 3h00 |
| 2 | Jequié | 2h30 |
| 3 | Vitória da Conquista | — |

Caronas percorrem o corredor nos dois sentidos, com durações simétricas.

Internamente a rota continua sendo uma lista explícita de cidades e os horários
uma lista de instantes: o corredor é usado apenas como fonte dos horários e como
validação de entrada. Generalizar para grafo livre depois é trocar uma função, e
não reescrever o domínio.

Um efeito colateral útil: com 4 cidades em linha e sem revisitar cidade, um
itinerário tem no máximo 3 trechos. O teto emerge da topologia, e não de um
parâmetro arbitrário escolhido para conter explosão combinatória.

### D10 — Contadores por trecho, sem numeração de assentos

`Livres[t]` é um inteiro por trecho. O enunciado fala em "quantidade de assentos
livres", e a garantia exigida é não vender além da capacidade. Numerar assentos
individualmente aumentaria o estado sem alterar a propriedade de correção.

### D11 — Dinheiro em centavos, instantes em RFC 3339

Inteiro em centavos elimina erro de arredondamento na soma do preço total do
itinerário. RFC 3339 é o formato nativo de `time.Time` em Go.

### D12 — Usuários de arquivo fixo, perfis imutáveis

`usuarios.json` é carregado no boot. Não há operação de cadastro; o enunciado
pede apenas autenticação. Um usuário tem exatamente um perfil: quem quiser ser
motorista e passageiro precisa de duas contas.

Senhas ficam em texto claro. O escopo do protótipo é coordenação distribuída, e
não segurança. Guardar hash SHA-256 é uma alteração de poucas linhas caso se
queira mencionar no relatório.

### D13 — Regras temporais

| Constante | Valor | Onde se aplica |
|---|---|---|
| `MARGEM_BALDEACAO` | 30 min | Folga mínima entre chegada de um trecho e partida do seguinte |
| `ANTECEDENCIA_CANCELAMENTO` | 1 h | Prazo do passageiro para cancelar, contado da partida do primeiro trecho |
| Cancelamento de carona | até a partida | Prazo do motorista |

A margem de baldeação deve ser a **mesma constante** na busca e na reserva. Se
divergirem, a busca oferece itinerários que a reserva recusa.

Assimetria deliberada: quando o motorista cancela a carona, o cancelamento em
cascata das reservas **ignora** o prazo de uma hora do passageiro. O prazo
protege o motorista contra desistência de última hora, não o contrário.

### D14 — Reservas do mesmo passageiro não se sobrepõem

Comparação feita sobre o intervalo do itinerário inteiro, da primeira partida à
última chegada, e não trecho a trecho: o tempo de espera numa baldeação também é
tempo em que o passageiro não pode estar em outra viagem.

### D15 — Clientes com menu interativo e conexão única por sessão

Os clientes são interfaces de terminal navegadas por menu numérico, e não CLIs de
subcomandos. Uma execução do cliente abre **uma** conexão TCP, faz `LOGIN`,
executa quantas operações o usuário quiser e encerra com `LOGOUT`.

A justificativa é de coerência com D08. Um CLI de subcomandos criaria um processo
novo por operação, abrindo e fechando conexão a cada comando, o que obrigaria a
autenticar a cada operação e esvaziaria o argumento de que a conexão persistente
torna o token de sessão desnecessário. O menu é o cenário para o qual o protocolo
foi desenhado.

O usuário nunca digita identificador. Ao escolher um itinerário retornado pela
busca, o cliente devolve ao servidor os campos `carona_id`, `de` e `ate` que ele
mesmo recebeu, sem que nada disso apareça na tela.

Entrada inválida no menu não encerra o processo nem fecha a conexão: o cliente
revalida e repete a pergunta. Isso importa na apresentação, que tem 20 minutos e
arguição no meio.

**Exceção deliberada:** o teste de carga não usa o menu. Ele fala o protocolo
diretamente por socket, usando `internal/protocolo`, porque precisa controlar
temporização e disparar requisições simultâneas, o que uma interface interativa
não permite.

---

## 4. Modelo de domínio

```go
type Usuario struct {
    Usuario string
    Senha   string
    Nome    string
    Perfil  string // "MOTORISTA" ou "PASSAGEIRO"
}

type Carona struct {
    ID          string
    MotoristaID string
    Rota        []string    // ["Salvador", "Feira de Santana", "Jequié"]
    Horarios    []time.Time // len == len(Rota); derivados do corredor
    Assentos    int         // capacidade total do veículo
    PrecoTrecho []int       // centavos; len == len(Rota)-1
    Livres      []int       // len == len(Rota)-1; inicia com Assentos
    Cancelada   bool
}

type ItemReserva struct {
    CaronaID string
    De, Ate  int // índices de cidade na Rota; consome trechos [De, Ate)
}

type Reserva struct {
    ID           string
    PassageiroID string
    Itens        []ItemReserva
    Ativa        bool
    CriadaEm     time.Time
}

type Estado struct {
    mu       sync.Mutex
    usuarios map[string]*Usuario
    caronas  map[string]*Carona
    reservas map[string]*Reserva
}
```

Uma rota com N cidades tem N−1 trechos, indexados de 0 a N−2. Uma reserva de A
até C sobre a rota `[A, B, C, D]` consome os índices 0 e 1.

---

## 5. Arquitetura

### 5.1 Camadas do servidor

| # | Camada | Responsabilidade | Implementação |
|---|---|---|---|
| 1 | Conexão | Aceitar conexões e isolar cada cliente | `net.Listener`, goroutine por `net.Conn` |
| 2 | Enquadramento | Delimitar mensagens e codificar/decodificar | `bufio.Reader.ReadBytes('\n')`, `encoding/json` |
| 3 | Sessão | Autenticar e lembrar quem é o cliente | variável local da goroutine |
| 4 | Roteamento | Despachar por tipo de mensagem | `switch req.Tipo` |
| 5 | Domínio | Regras de negócio | funções sobre o estado já travado |
| 6 | Estado | Guardar caronas, reservas e usuários | `sync.Mutex` |

### 5.2 Estrutura de pacotes

```
vaijunto/
├── cmd/
│   ├── servidor/main.go
│   ├── motorista/main.go
│   └── passageiro/main.go
├── internal/
│   ├── protocolo/    # envelope, structs de requisição/resposta, códigos de erro, framing
│   ├── dominio/      # Corredor, Carona, Reserva, Usuario, Estado, regras, mutex
│   ├── servidor/     # listener, sessão, roteador
│   └── cliente/      # conexão reaproveitada pelos dois CLIs
├── testes/           # teste de concorrência e carga
├── dados/            # usuarios.json, caronas.json
├── docs/             # figuras do relatório
├── Dockerfile.servidor
├── Dockerfile.cliente
├── docker-compose.yml
├── PROTOCOL.md
├── PROJETO.md
└── README.md
```

O pacote `protocolo`, compartilhado entre servidor e clientes, garante que os
dois lados nunca divirjam. Isso não fere a interoperabilidade: o contrato real é
o JSON documentado em `PROTOCOL.md`, e um cliente escrito em outra linguagem
continua funcionando.

---

## 6. Algoritmo de busca de itinerários

Entrada: cidade de origem, cidade de destino, data.

**Passo 1 — normalizar.** Converter origem e destino em índices `i` e `j` do
corredor. Se `i == j`, erro. Sentido `d = +1` se `j > i`, senão `d = -1`.

**Passo 2 — gerar pernas candidatas.** Uma perna é um segmento contíguo de uma
única carona.

```
pernas = []
para cada carona c não cancelada:
    se sentido(c) != d: continua
    para cada par (a, b) de posições da rota de c, com a < b:
        se cidade(c,a) ou cidade(c,b) estiverem fora do intervalo [i..j]: continua
        se algum trecho t em [a, b) tem livres[t] == 0: continua
        pernas.append({carona: c, de: a, ate: b,
                       partida: c.Horarios[a], chegada: c.Horarios[b],
                       preco: soma(c.PrecoTrecho[a..b-1])})
```

Com 4 cidades, cada carona gera no máximo 6 pernas.

**Passo 3 — busca em profundidade.** Estado: cidade atual e instante em que o
passageiro fica livre.

```
função expandir(cidadeAtual, livreEm, acumulado, resultados):
    se cidadeAtual == destino:
        resultados.append(montarItinerario(acumulado)); retorna
    para cada perna p com origem == cidadeAtual:
        se acumulado está vazio:
            se data(p.partida) != dataPedida: continua
        senão:
            se p.partida < livreEm + MARGEM_BALDEACAO: continua
        se perna usa carona já presente em acumulado: continua
        expandir(p.destino, p.chegada, acumulado + [p], resultados)
```

O filtro de data se aplica apenas à primeira perna, o que permite baldeação
atravessando a meia-noite. A checagem de carona repetida evita itinerários que
embarcam duas vezes no mesmo veículo.

**Passo 4 — ordenar e limitar.** Preço total crescente; empate por chegada mais
cedo; depois por menos baldeações. Máximo de 20 itinerários.

A busca lê `Livres` apenas para podar pernas inviáveis. Ela não reserva e não
promete nada; a reserva refaz toda a verificação sob o lock. A duplicação é
deliberada: é o que torna a busca barata e a reserva correta ao mesmo tempo.

---

## 7. Algoritmo de reserva atômica

Entrada: lista ordenada de itens `{carona_id, de, ate}`.

Tudo executa dentro de **uma única seção crítica**:

1. **Formato.** Lista não vazia; `de < ate`; índices dentro da rota de cada
   carona; nenhuma carona repetida.
2. **Encadeamento.** Para cada par consecutivo, a cidade de chegada do anterior é
   a de partida do seguinte, e a partida do seguinte ocorre no mínimo
   `MARGEM_BALDEACAO` após a chegada do anterior.
3. **Disponibilidade.** Para todo trecho `t` em `[de, ate)` de cada carona,
   `Livres[t] >= 1`. Caronas canceladas reprovam.
4. **Sobreposição.** Para cada reserva ativa do mesmo passageiro, o intervalo
   `[partida, chegada]` do novo itinerário não pode intersectar o intervalo da
   reserva existente.
5. **Falha.** Se qualquer passo anterior falhar, responder erro **sem alterar
   nada**.
6. **Commit.** Decrementar todos os `Livres` envolvidos, criar a reserva,
   responder `OK`.

A separação entre os passos 1–4 (que apenas verificam) e o passo 6 (que apenas
escreve) é o que garante RNF06. Nenhuma escrita ocorre antes de toda a validação
passar, então não existe estado parcial a desfazer e nenhum mecanismo de
compensação é necessário.

O cancelamento de carona segue o mesmo princípio: marca a carona, e para cada
reserva ativa que use qualquer trecho dela, marca a reserva como cancelada e
devolve os assentos de **todos** os seus trechos, inclusive os de outras caronas,
tudo na mesma seção crítica.

---

## 8. Invariantes e plano de teste

### 8.1 Invariantes

| ID | Propriedade |
|---|---|
| I1 | Para toda carona `c` e trecho `t`: `Livres[t] + (reservas ativas cobrindo (c,t)) == Assentos` |
| I2 | `0 <= Livres[t] <= Assentos`, sempre |
| I3 | Toda reserva ativa é um caminho contíguo no espaço e monotônico no tempo |
| I4 | Nenhuma reserva ativa referencia carona cancelada |
| I5 | Duas reservas ativas do mesmo passageiro não se sobrepõem no tempo |

I1 é a invariante mestra. Ela é simultaneamente a prova de que nenhum assento foi
vendido duas vezes (o lado esquerdo nunca excede `Assentos`) e de que nenhum
assento ficou permanentemente bloqueado (nunca fica abaixo). Um único predicado
cobre RNF05 e RNF07.

O verificador não precisa de operação administrativa no protocolo: autentica-se
como cada motorista e chama `DETALHAR_CARONA`, autentica-se como cada passageiro
e chama `LISTAR_MINHAS_RESERVAS`, e reconstrói a soma do lado do cliente. O teste
valida o sistema pela mesma interface que os usuários reais usam.

### 8.2 Cenários

| ID | Cenário | Verificação |
|---|---|---|
| T1 | 1 carona, 1 assento, 1 trecho, 50 clientes reservam simultaneamente | Exatamente 1 `OK`, 49 `SEM_ASSENTO`, `Livres == 0` |
| T2 | Itinerário A+B, A com 10 assentos e B com 1, 50 clientes simultâneos | 1 sucesso; `Livres` de A deve ser 9. Valor menor prova reserva parcial |
| T3 | Reservas simultâneas em trechos disjuntos da mesma carona | Todas bem-sucedidas; contadores por trecho corretos |
| T4 | N clientes em laço de reservar e cancelar por 30 s | Ao final, `Livres` volta ao valor inicial |
| T5 | Motorista cancela carona enquanto passageiros reservam nela | I1 e I4 valem; nenhum assento órfão em caronas vizinhas |
| T6 | Cliente encerra a conexão no meio de uma linha JSON | Servidor continua atendendo os demais; I1 vale |
| T7 | Rajada de mensagens malformadas e tipos desconhecidos | Servidor responde erro e permanece disponível |
| T8 | Mesmo passageiro tenta duas reservas sobrepostas em paralelo | Exatamente uma passa; a outra recebe `CONFLITO_HORARIO` |

T2 é o teste central da atomicidade. T8 existe por causa de D14.

Todos os testes de unidade e integração devem rodar com `go test -race`. O
detector de corrida do Go encontra acesso concorrente não sincronizado ao estado
mesmo quando o teste passa por sorte de escalonamento.

### 8.3 Medição de desempenho

Usando o campo `id` do envelope, o teste registra o instante de envio e o de
recebimento de cada requisição e calcula latência média, p50, p95 e p99, além de
vazão em requisições por segundo. Executar com N = 1, 10, 50 e 100 clientes
concorrentes para gerar a curva do relatório e a base de comparação entre
`Mutex` e `RWMutex`.

Medir duas vezes: com o teste na mesma máquina do servidor (sem latência de rede)
e na segunda máquina do laboratório (com rede real).

---

## 9. Dados de carga inicial

### 9.1 Usuários

| Usuário | Senha | Perfil |
|---|---|---|
| joao, carlos, ana | 1234 | MOTORISTA |
| maria, pedro, lucia | abcd | PASSAGEIRO |
| teste01 … teste50 | teste | PASSAGEIRO |

Os 50 usuários genéricos existem para o teste de carga; sem eles, T1 e T8 não têm
como autenticar 50 conexões distintas.

### 9.2 Caronas

Todas em 15/09/2026, salvo indicação.

| ID | Motorista | Rota | Partida | Chegada | Assentos | Papel no cenário |
|---|---|---|---|---|---|---|
| car-1 | joao | Salvador → Jequié | 06:00 | 11:00 | 3 | Primeira perna da baldeação |
| car-2 | carlos | Jequié → Vitória da Conquista | 12:30 | 15:00 | 2 | Segunda perna, folga de 90 min |
| car-3 | ana | Feira de Santana → Vitória da Conquista | 09:00 | 14:30 | 1 | Itinerário alternativo, assento escasso |
| car-4 | joao | Jequié → Vitória da Conquista | 11:15 | 13:45 | 4 | **Deve ser rejeitada** como conexão de car-1 |
| car-5 | carlos | Vitória da Conquista → Salvador | 08:00 | 15:30 | 3 | Sentido oposto; não pode aparecer na busca |
| car-6 | ana | Salvador → Vitória da Conquista (**16/09**) | 07:00 | 14:30 | 4 | Direta, mas fora da data pedida |
| car-7 | joao | Feira de Santana → Jequié | 08:30 | 11:30 | 1 | Trecho de assento único para T1 |

A consulta **Salvador → Vitória da Conquista em 15/09** exercita tudo:

- Não existe carona direta na data, então a baldeação é obrigatória. É
  exatamente o exemplo do enunciado.
- `car-1 + car-2` é válido: chegada em Jequié às 11:00, partida às 12:30, folga
  de 90 min.
- `car-1 + car-4` é **inválido**: partida às 11:15, folga de 15 min, abaixo da
  margem. Se aparecer no resultado, a validação de margem está quebrada.
- `car-1 + car-3` é válido: chegada em Feira às 08:00, partida às 09:00.
- `car-1 + car-7 + car-2` é um itinerário de três pernas, e o assento único de
  `car-7` é o gargalo do teste de concorrência.
- `car-5` e `car-6` devem estar sempre ausentes. São os controles negativos de
  sentido e de data.

Escrever um teste que fixe o resultado esperado dessa consulta. Ele serve de
regressão para o algoritmo de busca e de evidência de corretude no relatório.

---

## 10. Containerização e execução

### 10.1 Detalhe crítico de fuso horário

O binário precisa do banco de fusos para interpretar `-03:00` e
`America/Bahia`, e a imagem `alpine` não o inclui. Solução, no `main.go` do
servidor:

```go
import _ "time/tzdata"
```

Sem isso, tudo funciona na máquina de desenvolvimento e quebra dentro do
contêiner, com horários deslocando três horas.

### 10.2 Execução no laboratório

Máquina A, servidor:

```bash
docker run --rm -p 9000:9000 \
  -v $(pwd)/dados:/dados \
  vaijunto-servidor --usuarios /dados/usuarios.json --caronas /dados/caronas.json
```

Máquina B, cliente:

```bash
docker run --rm -it \
  -e VAIJUNTO_SERVIDOR=192.168.0.10:9000 \
  vaijunto-cliente /bin/passageiro
```

Três pontos que costumam consumir tempo em laboratório:

- O servidor escuta em `0.0.0.0:9000`, nunca em `127.0.0.1`. Ouvindo em loopback,
  o mapeamento de porta não recebe nada de fora do contêiner.
- Os CLIs precisam de `-it`, porque leem do terminal. Sem isso, o cliente morre
  imediatamente ao ler EOF do stdin.
- O firewall das máquinas pode bloquear a porta 9000. Testar a conectividade com
  `PING` antes da apresentação e ter o IP anotado, não descoberto na hora.

---

## 11. Roteiro de implementação

| Fase | Entregável | Critério de pronto |
|---|---|---|
| 0 | Requisitos | Este documento, seção 2 |
| 1 | Modelo de domínio | Este documento, seção 4 |
| 2 | Arquitetura | Este documento, seção 5 |
| 3 | Protocolo | `PROTOCOL.md` |
| 4 | Servidor: esqueleto de rede | `PING` responde por `nc`; cliente derrubado não afeta os demais |
| 5 | Operações básicas | Publicar, listar, detalhar, cancelar, listar reservas funcionam |
| 6 | Busca de itinerários | Teste de regressão da seção 9.2 passa |
| 7 | Reserva atômica | T1, T2 e T8 passam com `-race` |
| 8 | Clientes CLI com menu interativo | Motorista e passageiro completos; conexão única por sessão |
| 9 | Teste de carga | T1 a T8 e as curvas de latência |
| 10 | Docker e execução distribuída | Servidor e cliente em máquinas distintas |
| 11 | Relatório SBC e README | 8 páginas, formato SBC, referenciado |

---

## 12. Trabalho futuro

Itens deliberadamente fora do escopo, úteis para a seção final do relatório:

- Cadastro de usuários e hash de senha.
- Grafo de cidades arbitrário no lugar do corredor fixo.
- `RWMutex` ou granularidade fina de lock, com medição comparativa.
- Persistência do estado e recuperação após reinício.
- Réplicas do servidor, o que traria o problema de consenso distribuído.
- Notificação ativa do passageiro quando o motorista cancela a carona, o que
  exigiria abandonar o modelo estritamente requisição/resposta.