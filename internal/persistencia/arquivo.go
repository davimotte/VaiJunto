// Package persistencia é o log em arquivo de cada empresa (D22): append-only,
// um registro JSON por linha, com fsync a cada registro.
//
// O pacote só grava e lê linhas. Não conhece o significado dos registros
// (isso é do domínio, seção 4.8) e não importa nenhum pacote interno
// (PROJETO_P2.md, seção 5.3). Também não usa banco de dados: o enunciado exige
// que a recuperação seja código nosso (RD08).
package persistencia

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrLogCorrompido indica linha completa e inválida no log. Diferente da
// última linha incompleta, que é consequência normal de uma queda, isto não
// tem explicação benigna (seção 9.2): o servidor recusa subir.
var ErrLogCorrompido = errors.New("persistencia: log corrompido")

// Arquivo é o log aberto para anexar registros.
//
// Não tem mutex próprio, de propósito: o único chamador de Anexar é a camada
// de estado, que já está dentro do mutex da empresa (D05, regra 3). Dois
// Anexar simultâneos seriam um bug de quem chama, não algo a esconder aqui.
type Arquivo struct {
	f *os.File
}

// Abrir cria o log se ele não existir, lê os registros existentes e devolve o
// arquivo pronto para anexar, junto com as linhas válidas em ordem (sem o
// '\n' final).
//
// D22/seção 9.2: se a ÚLTIMA linha não termina em '\n', ela foi interrompida
// pela queda. Como nenhuma resposta sai antes do fsync do registro inteiro,
// ninguém foi avisado dela; descartamos e aparamos o arquivo até o fim da
// última linha completa. Sem aparar, o próximo registro seria colado na sobra
// e criaria uma linha inválida no meio, que o boot seguinte recusaria.
//
// Linha terminada em '\n' e inválida, em qualquer posição, é corrupção: erro
// ErrLogCorrompido, com caminho e número da linha, e o arquivo não é tocado
// para que o operador possa inspecioná-lo.
func Abrir(caminho string) (*Arquivo, [][]byte, error) {
	criado := false
	if _, err := os.Stat(caminho); errors.Is(err, os.ErrNotExist) {
		criado = true
	}

	f, err := os.OpenFile(caminho, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("persistencia: abrir %s: %w", caminho, err)
	}
	if criado {
		// O arquivo novo só existe de verdade depois que a entrada do diretório
		// chega ao disco; sem isso uma queda logo após o primeiro registro
		// poderia perder o próprio arquivo.
		if err := sincronizarDiretorio(filepath.Dir(caminho)); err != nil {
			f.Close()
			return nil, nil, err
		}
	}

	linhas, fimValido, err := lerLinhas(f, caminho)
	if err != nil {
		f.Close()
		return nil, nil, err
	}

	// Aparar só depois de saber que o log inteiro é aceitável.
	tamanho, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("persistencia: posicionar %s: %w", caminho, err)
	}
	if tamanho > fimValido {
		if err := f.Truncate(fimValido); err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("persistencia: aparar a última linha incompleta de %s: %w", caminho, err)
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("persistencia: sincronizar %s: %w", caminho, err)
		}
	}
	if _, err := f.Seek(fimValido, io.SeekStart); err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("persistencia: posicionar %s: %w", caminho, err)
	}
	return &Arquivo{f: f}, linhas, nil
}

// lerLinhas devolve as linhas completas e válidas e o deslocamento logo após
// a última delas.
func lerLinhas(f *os.File, caminho string) ([][]byte, int64, error) {
	var (
		linhas [][]byte
		fim    int64
		numero int
		leitor = bufio.NewReader(f)
	)
	for {
		// ReadBytes devolve uma cópia nova a cada chamada, então guardar a
		// fatia é seguro (ao contrário do ReadSlice usado no TCP).
		bruta, err := leitor.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			// O que sobrou, se sobrou, não tem '\n': linha incompleta.
			return linhas, fim, nil
		}
		if err != nil {
			return nil, 0, fmt.Errorf("persistencia: ler %s: %w", caminho, err)
		}
		numero++
		fim += int64(len(bruta))
		linha := bytes.TrimSuffix(bruta, []byte("\n"))
		if !json.Valid(linha) {
			return nil, 0, fmt.Errorf("%w: %s, linha %d não é JSON válido; o servidor não sobe com estado duvidoso (restaure o arquivo ou apague o volume para recomeçar a empresa)", ErrLogCorrompido, caminho, numero)
		}
		linhas = append(linhas, linha)
	}
}

// Anexar grava o registro e só retorna depois do fsync. É este retorno que
// autoriza o chamador a responder a um cliente ou a um par (regra 3).
//
// Uma única chamada Write com a linha inteira: se a queda a interromper, o que
// sobra no disco é um prefixo sem '\n', exatamente o caso que Abrir descarta.
func (a *Arquivo) Anexar(registro []byte) error {
	linha := make([]byte, 0, len(registro)+1)
	linha = append(linha, registro...)
	linha = append(linha, '\n')
	if _, err := a.f.Write(linha); err != nil {
		return fmt.Errorf("persistencia: gravar: %w", err)
	}
	if err := a.f.Sync(); err != nil {
		return fmt.Errorf("persistencia: fsync: %w", err)
	}
	return nil
}

// Fechar libera o arquivo. O servidor não tem rotina de desligamento (seção
// 9.1); isto serve aos testes.
func (a *Arquivo) Fechar() error {
	return a.f.Close()
}

func sincronizarDiretorio(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("persistencia: abrir diretório %s: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("persistencia: fsync do diretório %s: %w", dir, err)
	}
	return nil
}
