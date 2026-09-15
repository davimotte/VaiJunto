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
| RF02 | Motorista publica carona: sequência de paradas com o horário de cada uma, assentos, preço por trecho |
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

### D09 — Paradas informadas pelo motorista, sobre cidades fixas

O servidor conhece um conjunto fixo de cidades atendidas: Salvador, Feira de
Santana, Jequié e Vitória da Conquista. O conjunto **não tem ordem nem
durações**. O motorista informa a sequência de paradas da carona e o horário em
que passa por cada uma; o servidor valida e guarda, sem derivar nada.

Validações de uma carona:

- toda cidade pertence ao conjunto atendido;
- ao menos duas paradas;
- nenhuma cidade repetida na rota;
- horários estritamente crescentes (horário igual ao anterior também é recusado);
- um preço por trecho entre paradas consecutivas;
- a primeira parada no futuro.

Há **um horário por parada**: o instante em que o carro chega a uma cidade é o
mesmo em que parte dela. Espera no local não é modelada.

Justificativa: o motorista define a rota (seção 1), e um corredor com durações
fixas impunha a todos a mesma velocidade e só admitia rotas em linha. Manter o
conjunto de cidades fixo preserva o que ele tinha de útil: comparação por grafia
canônica e menu enumerado no cliente, sem cidade digitada.

Consequência que precisa ser defendida: **horários crescentes deixam de ser
garantidos por construção.** Com o corredor, a derivação somava durações
positivas; agora quem garante é a validação. A busca (janela de baldeação), a
reserva (encadeamento e sobreposição) e a invariante I3 dependem disso, então as
mesmas validações se aplicam nas duas portas de entrada do estado: a publicação e
a carga de `dados/caronas.json` no boot. A única exceção na carga é a regra da
partida no futuro: os dados de demonstração têm data fixa, e aplicá-la impediria
o servidor de subir depois dessa data.

Sem corredor não existe "sentido" de viagem, então a busca não pode mais podar
por sentido (seção 6). O que impede um itinerário de ir e voltar é a regra de
**não revisitar cidade**, aplicada igualmente na busca e na reserva e contando
as cidades intermediárias por onde o passageiro passa dentro do veículo. Dela
decorre o teto do itinerário: no máximo `|cidades| − 1` pernas, 3 com as quatro
cidades atuais. O teto continua emergindo de uma regra do domínio, e não de um
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
| `MARGEM_BALDEACAO` | 30 min | Folga **mínima** entre chegada de um trecho e partida do seguinte |
| `ESPERA_MAXIMA_BALDEACAO` | 12 h | Folga **máxima** da mesma conexão |
| `ANTECEDENCIA_CANCELAMENTO` | 1 h | Prazo do passageiro para cancelar, contado da partida do primeiro trecho |
| Cancelamento de carona | até a partida | Prazo do motorista |

A margem de baldeação deve ser a **mesma constante** na busca e na reserva. Se
divergirem, a busca oferece itinerários que a reserva recusa.

As duas margens delimitam uma janela, e não um piso solto. O teto existe porque o
filtro de data da busca vale só para a primeira perna (seção 6): sem ele, uma
carona de outro dia entra como perna intermediária e produz um "itinerário" com
espera de 24 h, formalmente válido no espaço e no tempo. Doze horas é o que
separa a conexão noturna legítima — que atravessa a meia-noite e é justamente o
que o filtro de data por perna única existe para permitir — da espera de um dia
inteiro, que nenhum passageiro chamaria de baldeação.

Como `MARGEM_BALDEACAO`, o teto é a **mesma constante** na busca e na reserva.

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

### D16 — Busca ordenada por menos trocas, com limite de 10 itinerários

A lista de itinerários devolvida ao passageiro segue esta ordem:

1. menos baldeações (trocas de veículo, `pernas − 1`);
2. menor preço total;
3. chegada mais cedo;
4. desempate determinístico sobre a sequência de trechos (seção 6, Passo 4).

"Parada", para a ordenação, é troca de veículo, e não cidade atravessada dentro
do carro: uma carona direta que passa por duas cidades vem antes de qualquer
itinerário com baldeação. A prioridade reflete o que o passageiro valoriza: cada
baldeação é um ponto em que ele depende de dois motoristas cumprirem o horário,
e o preço só desempata entre roteiros com o mesmo número de trocas.

A resposta traz **no máximo 10 itinerários**. O limite existe porque a resposta
é uma única linha do protocolo, sujeita ao teto de 64 KB (seção 5.1), e **o
cliente aplica o mesmo teto** ao ler. Um itinerário de duas pernas ocupa cerca de
1 KB; sem limite, uma busca com dezenas de combinações derrubaria a conexão do
passageiro. Dez cabem com folga mesmo com itinerários de três pernas e ainda são
legíveis num menu de terminal.

Como a ordenação acontece **antes** do corte, o limite nunca descarta um
itinerário com menos trocas enquanto mantém um com mais. Limitação conhecida,
registrada como tal: numa busca com mais de 10 possibilidades, o passageiro não
vê todas.

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
    Horarios    []time.Time // len == len(Rota); informados pelo motorista, estritamente crescentes
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
| 2 | Enquadramento | Delimitar mensagens e codificar/decodificar | `bufio.Reader.ReadSlice('\n')`, `encoding/json` |
| 3 | Sessão | Autenticar e lembrar quem é o cliente | variável local da goroutine |
| 4 | Roteamento | Despachar por tipo de mensagem | `switch req.Tipo` |
| 5 | Domínio | Regras de negócio | funções sobre o estado já travado |
| 6 | Estado | Guardar caronas, reservas e usuários | `sync.Mutex` |

Duas notas sobre a camada 2. O uso de `ReadSlice` em vez de `ReadBytes` é
deliberado: `ReadBytes` acumula num slice que cresce sem limite até encontrar o
delimitador, o que torna impossível aplicar o corte de 64 KB antes de já ter lido
a linha inteira, exatamente o que o limite existe para evitar. Com `ReadSlice`
sobre um buffer de 64 KB + 1, o estouro é detectado por `bufio.ErrBufferFull`
sem que a memória seja consumida.

A contrapartida é que `ReadSlice` devolve uma fatia apontando para o buffer
interno do `bufio.Reader`, sobrescrita na leitura seguinte. **A fatia precisa ser
copiada antes de sair da função de leitura.** Sem a cópia, mensagens enviadas em
sequência rápida corrompem umas às outras, e a falha só aparece sob carga.

A camada 4 também é responsável por traduzir erros de domínio em códigos do
protocolo, conforme a seção 5.3.

### 5.2 Estrutura de pacotes

```
vaijunto/
├── cmd/
│   ├── servidor/main.go
│   ├── motorista/main.go
│   └── passageiro/main.go
├── internal/
│   ├── protocolo/    # envelope, structs de requisição/resposta, códigos de erro, framing
│   ├── dominio/      # cidades atendidas, Carona, Reserva, Usuario, Estado, regras, mutex
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

### 5.3 Tradução de erros de domínio

`internal/dominio` não importa `internal/protocolo`, então ele não pode conhecer
os códigos de erro do protocolo. Ao mesmo tempo, é o domínio que sabe **qual**
erro ocorreu. A conciliação:

1. O domínio define erros sentinela tipados, sem string de protocolo:
   `dominio.ErrCidadeDesconhecida`, `dominio.ErrSemAssento`, e assim por diante.
2. `internal/servidor` mantém **uma única** tabela que mapeia cada erro sentinela
   ao seu código do `PROTOCOL.md` e à mensagem legível.
3. A camada 4 consulta a tabela ao montar a resposta.

Nenhum literal de código de erro pode existir em `internal/dominio`. Um erro
sentinela sem entrada na tabela cai em `ERRO_INTERNO`, e um teste percorre todos
os sentinelas exportados verificando que cada um tem entrada, o que impede a
divergência silenciosa entre as duas pontas.

Isso preserva a propriedade que sustenta toda a seção 5.1: o domínio não conhece
a rede. Trocar o protocolo por outro formato exigiria mexer apenas nas camadas 2
e 4.

---

## 6. Algoritmo de busca de itinerários

Entrada: cidade de origem, cidade de destino, data.

**Passo 1 — validar.** Origem e destino pertencem ao conjunto de cidades
atendidas (D09). Se forem a mesma cidade, erro.

**Passo 2 — gerar pernas candidatas.** Uma perna é um segmento contíguo de uma
única carona, indexado pela cidade onde o passageiro embarca.

```
pernas = mapa cidade → lista de pernas
para cada carona c não cancelada:
    para cada par (a, b) de posições da rota de c, com a < b:
        se algum trecho t em [a, b) tem livres[t] == 0: continua
        pernas[c.Rota[a]].append({carona: c, de: a, ate: b,
                                  cidades: c.Rota[a..b],
                                  partida: c.Horarios[a], chegada: c.Horarios[b],
                                  preco: soma(c.PrecoTrecho[a..b-1])})
```

Uma carona com `k` paradas gera no máximo `k(k−1)/2` pernas.

Não há poda geométrica. Na versão com corredor fixo, pernas no sentido oposto ao
da busca e cidades fora do intervalo entre origem e destino eram descartadas
aqui. Com paradas livres (D09) essas podas perdem o sentido e passam a errar:
uma carona `[Feira, Salvador, Conquista]` seria classificada pelo sentido das
duas primeiras paradas e descartada, embora sirva à busca Salvador → Conquista.

**Passo 3 — busca em profundidade.** Estado: cidade atual, instante em que o
passageiro fica livre e cidades já visitadas. A busca começa com
`visitadas = {origem}`.

```
função expandir(cidadeAtual, livreEm, acumulado, visitadas, resultados):
    se cidadeAtual == destino:
        resultados.append(montarItinerario(acumulado)); retorna
    para cada perna p em pernas[cidadeAtual]:
        se acumulado está vazio:
            se data(p.partida) != dataPedida: continua
        senão:
            se p.partida < livreEm + MARGEM_BALDEACAO: continua
            se p.partida > livreEm + ESPERA_MAXIMA_BALDEACAO: continua
        se perna usa carona já presente em acumulado: continua
        se alguma cidade de p.cidades, exceto a de embarque, está em visitadas: continua
        marcar p.cidades em visitadas
        expandir(p.destino, p.chegada, acumulado + [p], visitadas, resultados)
        desmarcar p.cidades
```

O filtro de data se aplica apenas à primeira perna, o que permite baldeação
atravessando a meia-noite. É esse afrouxamento que obriga a existir o teto
`ESPERA_MAXIMA_BALDEACAO` (D13): sem ele, uma carona de outro dia encadeia
legalmente como perna intermediária, e a busca devolve esperas de 24 h como se
fossem conexões. A checagem de carona repetida evita itinerários que embarcam
duas vezes no mesmo veículo.

O conjunto de visitadas é o que substitui a antiga poda por sentido, e ele não é
redundante. O encadeamento no espaço e no tempo garante contiguidade, horários
avançando e carona não repetida, mas **não** impede voltar a uma cidade. Com
`A: Salvador 06:00 → Feira 08:00`, `B: Feira 08:30 → Salvador 10:30` e
`C: Salvador 11:00 → Conquista 18:30`, a sequência `A|B|C` é contígua, respeita a
janela de baldeação e usa três caronas distintas — e volta à origem. As cidades
intermediárias da perna também contam, porque o passageiro passou por elas dentro
do veículo; isso inclui o destino, de modo que uma perna que atravessa o destino
sem desembarcar nele não pode ser completada depois voltando até lá.

A regra também garante o término com teto explícito: cada perna acrescenta ao
menos uma cidade nova, então um itinerário tem no máximo `|cidades| − 1` pernas.

**Passo 4 — ordenar e limitar (D16).** Menos baldeações; empate por menor preço
total; depois por chegada mais cedo. Máximo de 10 itinerários, cortados **depois**
da ordenação.

Os três critérios não formam ordem total — há pares de itinerários que empatam
nos três —, e a iteração de mapa em Go é aleatória. A implementação
acrescenta um quarto desempate, determinístico, sobre a sequência de trechos do
itinerário. Não é regra de negócio: existe para que a mesma consulta devolva
sempre a mesma lista, na demonstração e no teste de regressão.

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
   `MARGEM_BALDEACAO` e no máximo `ESPERA_MAXIMA_BALDEACAO` após a chegada do
   anterior. Nenhuma cidade se repete no itinerário, contando as cidades
   intermediárias de cada item: é a mesma regra do Passo 3 da busca, pelo mesmo
   motivo de as margens serem constantes únicas.
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
| I3 | Toda reserva ativa é um caminho contíguo no espaço, sem cidade repetida, e monotônico no tempo |
| I4 | Nenhuma reserva ativa referencia carona cancelada |
| I5 | Duas reservas ativas do mesmo passageiro não se sobrepõem no tempo |
| I6 | Toda carona tem ao menos duas paradas, nenhuma cidade repetida e horários estritamente crescentes |

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

Todas em **01/10/2026**, salvo indicação. A data fica com folga em relação ao
dia da apresentação: os prazos de cancelamento são aplicados com o relógio real,
e a demonstração cancela reserva e carona deste cenário.

| ID | Motorista | Rota e horários | Assentos | Preço por trecho (R$) | Papel no cenário |
|---|---|---|---|---|---|
| car-1 | joao | Salvador 06:00 → Feira de Santana 07:45 → Jequié 10:45 | 3 | 25,00 · 35,00 | Primeira perna das baldeações |
| car-2 | carlos | Jequié 12:15 → Vitória da Conquista 14:30 | 2 | 40,00 | Segunda perna da baldeação do enunciado (folga de 90 min) |
| car-3 | ana | Feira de Santana 09:30 → Vitória da Conquista 16:45 | 2 | 55,00 | Opção com uma troca mais barata e que chega mais tarde |
| car-4 | ana | Feira de Santana 08:15 → Jequié 11:15 | **1** | 30,00 | Perna do meio do itinerário de três pernas; assento escasso (T1 e disputa) |
| car-5 | joao | Jequié 05:30 → Salvador 10:30 → Vitória da Conquista 17:30 | 3 | 60,00 · 110,00 | Direta mais cara; rota impossível num corredor fixo (D09); embarque no meio |
| car-6 | carlos | Jequié 11:00 → Vitória da Conquista 13:15 | 4 | 40,00 | **Controle negativo:** margem de baldeação |
| car-7 | ana | **02/10** Salvador 06:00 → Feira de Santana 07:45 → Jequié 10:45 → Vitória da Conquista 13:00 | 4 | 25,00 · 35,00 · 40,00 | **Controle negativo:** data da primeira perna e teto de espera |
| car-8 | carlos | Feira de Santana 08:15 → Salvador 10:00 | 2 | 25,00 | **Controle negativo:** fecha um ciclo com car-1 |

#### Resultado esperado

A consulta **Salvador → Vitória da Conquista em 01/10/2026** devolve exatamente
estes quatro itinerários, nesta ordem:

| # | Itinerário | Trechos | Baldeações | Preço | Partida → chegada |
|---|---|---|---|---|---|
| 1 | car-5 | `car-5:1-2` | 0 | R$ 110,00 | 10:30 → 17:30 |
| 2 | car-1 até Feira + car-3 | `car-1:0-1 \| car-3:0-1` | 1 | R$ 80,00 | 06:00 → 16:45 |
| 3 | car-1 até Jequié + car-2 | `car-1:0-2 \| car-2:0-1` | 1 | R$ 100,00 | 06:00 → 14:30 |
| 4 | car-1 até Feira + car-4 + car-2 | `car-1:0-1 \| car-4:0-1 \| car-2:0-1` | 2 | R$ 95,00 | 06:00 → 14:30 |

Derivação, pelas regras da seção 6 e das decisões D13 e D16:

- A primeira perna precisa sair de Salvador em 01/10. Servem car-1 até Feira,
  car-1 até Jequié e car-5 a partir de Salvador; car-7 é de 02/10.
- De Feira, com chegada às 07:45, são aceitas partidas entre 08:15 e 19:45.
  car-3 (09:30) chega ao destino: **#2**. car-4 (08:15, folga exata de 30 min)
  leva a Jequié às 11:15, e de lá car-2 (12:15) chega ao destino: **#4**. car-8
  volta a Salvador, cidade já visitada.
- De Jequié, com chegada às 10:45 por car-1, car-2 (12:15) chega ao destino:
  **#3**. car-6 (11:00) fica a 15 min da chegada.
- car-5 vai de Salvador direto ao destino: **#1**.

A ordem é a da D16, e cada par prova um critério:

- **#1 antes de todos** mesmo sendo a mais cara: menos baldeações vem antes de
  preço.
- **#2 antes de #3** mesmo chegando mais tarde: dentro do mesmo número de trocas,
  preço vem antes de chegada.
- **#4 por último** mesmo mais barato que #1 e #3: tem duas trocas.

#### O que cada caso exercita

| Caso | Onde |
|---|---|
| Baldeação combinando dois motoristas, o exemplo do enunciado | #3 (joao e carlos) |
| Direta mais cara que uma baldeação | #1 (R$ 110,00) antes de #2 (R$ 80,00) |
| Mesmo número de pernas, preços diferentes | #2 e #3 |
| Itinerário de três pernas | #4, com três motoristas diferentes |
| Embarque no meio de uma carona (`de > 0`) | #1: car-5 embarcando em Salvador (`de = 1`) |
| Rota que um corredor fixo não permitiria (D09) | car-5: Jequié → Salvador → Vitória da Conquista |
| Assento escasso | car-4, no #4, no T1 e na disputa da demonstração |

#### Controles negativos

Cada controle cumpre todas as regras da busca exceto uma. Se um deles aparecer
no resultado, a regra que ele isola está quebrada.

| Itinerário que nunca pode aparecer | Regra que o recusa |
|---|---|
| car-1 até Jequié (10:45) + car-6 (11:00) | `MARGEM_BALDEACAO`: folga de 15 min |
| car-1 até Feira (01/10 07:45) + car-7 a partir de Feira (02/10 07:45) | `ESPERA_MAXIMA_BALDEACAO`: espera de 24 h |
| car-7 inteira, como direta | Filtro de data da primeira perna |
| car-1 até Feira (07:45) + car-8 (08:15 → 10:00) + car-5 a partir de Salvador (10:30) | Cidade repetida: volta a Salvador (D09) |

O ciclo é o contraexemplo da seção 6 em forma de dados: as duas folgas são de 30
min exatos, nenhuma espera passa de 12 h e as três caronas são distintas. Só o
conjunto de cidades visitadas o recusa.

As regras de trecho que atravessa cidade visitada, de trecho sem assento e de
carona cancelada não têm caso próprio neste cenário, para não inflá-lo: ficam
cobertas pelos testes unitários do domínio. O limite de 10 itinerários (D16)
também não aparece aqui, porque exigiria mais de 10 combinações na mesma
consulta; ele tem um teste próprio, com itinerários montados em código.

Um teste fixa essa lista completa e ordenada, e não só a presença dos
itinerários válidos: um bug de validação costuma acrescentar um itinerário
errado sem remover nenhum correto. Ele serve de regressão para o algoritmo de
busca e de evidência de corretude no relatório.

#### Uso nos testes e na demonstração

As caronas do arquivo servem só a operações **sem prazo**: busca, reserva,
listagem, detalhamento e o T1. Todo teste que cancela reserva ou carona publica
as próprias caronas pelo protocolo, com partida relativa ao relógio, para não
expirar quando a data do cenário passar.

Roteiro dos 20 minutos, sem nenhuma carona além das oito:

1. **Publicar.** ana publica uma carona ao vivo, com data de 25/09 ou posterior
   a 02/10, para que ela não altere a busca do passo seguinte.
2. **Buscar com baldeação.** maria busca Salvador → Vitória da Conquista em
   01/10 e recebe os quatro itinerários acima.
3. **Reservar.** maria reserva o #3. Livres: car-1 [2, 2], car-2 [1].
4. **Disputar o último assento.** pedro e lucia reservam o #4 ao mesmo tempo.
   Um confirma; o outro recebe `SEM_ASSENTO` no trecho 0 de car-4. Livres: car-4
   [0], car-2 [0].
5. **Cancelar em cascata.** joao cancela car-1. As duas reservas caem, e os
   assentos voltam nas caronas dos outros motoristas: car-2 volta a [2] e car-4
   a [1].

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

O import resolve só metade do problema: ele embute o banco de fusos, mas não
escolhe o fuso. `time.Local` continua vindo da variável `TZ`, que no contêiner
não está definida e vale UTC. Por isso **nenhum código do sistema usa
`time.Local`**. O fuso das cidades atendidas é uma propriedade do domínio
(D09), exposta por `dominio.FusoDasCidades()` (`America/Bahia`, com
deslocamento fixo de −03:00 como rede de segurança), e é a mesma função nas duas
pontas:

- o servidor lê nela a `data` de `BUSCAR_ITINERARIOS`, que é um dia civil e só
  existe dentro de um fuso;
- o cliente monta nela os horários que o motorista digita.

Com a data lida em UTC, uma carona que sai às 22:00 de Salvador (01:00 UTC do
dia seguinte) sumiria da busca do próprio dia e apareceria na do dia seguinte. O
teste `TestBuscarDataNoFusoDasCidades` cobre esse caso, e o pacote `testes` roda
com `time.Local` forçado para UTC, reproduzindo o contêiner na máquina de
desenvolvimento.

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
- Cadastro dinâmico de cidades atendidas e verificação de plausibilidade dos
  horários informados pelo motorista, a partir de distâncias reais.
- Paginação da busca, para mostrar mais de 10 itinerários sem estourar o limite
  de linha do protocolo (D16).
- `RWMutex` ou granularidade fina de lock, com medição comparativa.
- Persistência do estado e recuperação após reinício.
- Réplicas do servidor, o que traria o problema de consenso distribuído.
- Notificação ativa do passageiro quando o motorista cancela a carona, o que
  exigiria abandonar o modelo estritamente requisição/resposta.