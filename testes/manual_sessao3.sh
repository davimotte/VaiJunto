#!/usr/bin/env bash
# Testes manuais da Sessão 3 — camada de rede do VAIJUNTO.
# Usa /dev/tcp do bash: não depende de nc, telnet ou python.
# Uso: ./testes/manual_sessao3.sh [host] [porta]

HOST="${1:-localhost}"
PORTA="${2:-9000}"

abrir3()  { exec 3<>/dev/tcp/"$HOST"/"$PORTA" 2>/dev/null; }
abrir4()  { exec 4<>/dev/tcp/"$HOST"/"$PORTA" 2>/dev/null; }
abrir5()  { exec 5<>/dev/tcp/"$HOST"/"$PORTA" 2>/dev/null; }
fechar3() { exec 3<&- 2>/dev/null; exec 3>&- 2>/dev/null; }
fechar4() { exec 4<&- 2>/dev/null; exec 4>&- 2>/dev/null; }
fechar5() { exec 5<&- 2>/dev/null; exec 5>&- 2>/dev/null; }

env3() { printf '%s\n' "$1" >&3; }
env4() { printf '%s\n' "$1" >&4; }
env5() { printf '%s\n' "$1" >&5; }

ler3() { local l; if IFS= read -r -t 2 l <&3; then printf '%s\n' "$l"; else echo "(SEM RESPOSTA em 2s ou conexao fechada pelo servidor)"; fi; }
ler4() { local l; if IFS= read -r -t 2 l <&4; then printf '%s\n' "$l"; else echo "(SEM RESPOSTA em 2s ou conexao fechada pelo servidor)"; fi; }
ler5() { local l; if IFS= read -r -t 2 l <&5; then printf '%s\n' "$l"; else echo "(SEM RESPOSTA em 2s ou conexao fechada pelo servidor)"; fi; }

ping_json() { printf '{"id":"%s","tipo":"PING","dados":{}}' "$1"; }

titulo() { echo; echo "=================================================="; echo " $1"; echo "=================================================="; }

# Sanidade: o servidor esta no ar?
if ! abrir3; then
  echo "ERRO: nao consegui conectar em $HOST:$PORTA."
  echo "Suba o servidor antes:"
  echo "  go run ./cmd/servidor --endereco 0.0.0.0:9000 \\"
  echo "    --usuarios dados/usuarios.json --caronas dados/caronas.json"
  exit 1
fi
fechar3
echo "Servidor respondendo em $HOST:$PORTA"

# ---------------------------------------------------------------- A
titulo "TESTE A — PING basico (conexao nova)"
abrir3
env3 "$(ping_json A1)"
echo "-> $(ping_json A1)"
echo "<- $(ler3)"
fechar3
echo "Conferir: id ecoado como A1, campo servidor_em presente, fuso -03:00."

# ---------------------------------------------------------------- B
titulo "TESTE B — conexao persistente (duas mensagens, uma conexao)"
abrir3
env3 "$(ping_json B1)"; echo "-> B1"; echo "<- $(ler3)"
env3 "$(ping_json B2)"; echo "-> B2"; echo "<- $(ler3)"
env3 "$(ping_json B3)"; echo "-> B3"; echo "<- $(ler3)"
fechar3
echo "Conferir: TRES respostas na MESMA conexao, ids B1, B2 e B3."

# ---------------------------------------------------------------- C
titulo "TESTE C — JSON invalido nao derruba a conexao"
abrir3
env3 'isso nao e json'
echo "-> isso nao e json"
echo "<- $(ler3)"
env3 "$(ping_json C2)"
echo "-> C2 (PING logo depois do lixo)"
echo "<- $(ler3)"
fechar3
echo "Conferir: JSON_INVALIDO com id vazio, e o PING seguinte AINDA responde."

# ---------------------------------------------------------------- D
titulo "TESTE D — variacoes de envelope (mesma conexao)"
abrir3
env3 '{"id":"D1","tipo":"NAOEXISTE","dados":{}}'
echo "-> D1 tipo inexistente"
echo "<- $(ler3)"

env3 '{"id":"D2","dados":{}}'
echo "-> D2 sem campo tipo"
echo "<- $(ler3)"

env3 '["isso","e","um","array"]'
echo "-> D3 JSON valido mas envelope errado"
echo "<- $(ler3)"

env3 ''
echo "-> D4 linha vazia (nao deve haver resposta)"
echo "<- $(ler3)"

env3 "$(ping_json D5)"
echo "-> D5 PING final"
echo "<- $(ler3)"
fechar3
echo "Conferir: D1=TIPO_DESCONHECIDO, D2=ENVELOPE_INVALIDO,"
echo "          D3=ENVELOPE_INVALIDO (nao JSON_INVALIDO),"
echo "          D4=SEM RESPOSTA, D5 responde normalmente."

# ---------------------------------------------------------------- E
titulo "TESTE E — desconexao abrupta no meio de uma mensagem"
abrir3
printf '{"id":"E1","tipo":"PI' >&3
echo "-> enviada meia mensagem, sem quebra de linha"
sleep 1
fechar3
echo "Conexao fechada abruptamente."
sleep 1
echo "Verificando se o servidor sobreviveu:"
abrir3 && { env3 "$(ping_json E2)"; echo "<- $(ler3)"; fechar3; } || echo "!! SERVIDOR CAIU"
echo "Conferir: o PING E2 responde. Nenhum panic no terminal do servidor."

# ---------------------------------------------------------------- F
titulo "TESTE F — tres clientes simultaneos, um morre"
abrir3; abrir4; abrir5
env3 "$(ping_json F_c1)"; echo "cliente 1 <- $(ler3)"
env4 "$(ping_json F_c2)"; echo "cliente 2 <- $(ler4)"
env5 "$(ping_json F_c3)"; echo "cliente 3 <- $(ler5)"
echo "Matando o cliente 2..."
fechar4
sleep 1
env3 "$(ping_json F_c1_pos)"; echo "cliente 1 <- $(ler3)"
env5 "$(ping_json F_c3_pos)"; echo "cliente 3 <- $(ler5)"
fechar3; fechar5
echo "Conferir: clientes 1 e 3 continuam respondendo apos a morte do 2."

# ---------------------------------------------------------------- G
titulo "TESTE G — linha acima do limite de 64 KB"
GRANDE=$(printf 'a%.0s' $(seq 1 70000))
abrir3
printf '%s\n' "$GRANDE" >&3 2>/dev/null
echo "-> enviados 70000 bytes numa unica linha"
echo "<- $(ler3)"
fechar3
sleep 1
echo "Verificando se o servidor sobreviveu:"
abrir3 && { env3 "$(ping_json G2)"; echo "<- $(ler3)"; fechar3; } || echo "!! SERVIDOR CAIU"
echo "Conferir: a conexao grande foi fechada (com ou sem erro antes),"
echo "          e o servidor continua atendendo novas conexoes."

titulo "FIM"
echo "Compare cada bloco com a tabela de saidas esperadas."
