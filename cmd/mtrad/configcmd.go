package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"mtrad/internal/schema"
)

const configUsage = `uso: mtrad config [-c ARQ] <ação> ...

  show                       mostra o arquivo de configuração
  get   CHAVE                mostra um valor (CHAVE com pontos: translate.engines.llm.model)
  set   CHAVE VALOR          troca um valor (VALOR é JSON; texto simples vira string)
  add   CHAVE VALOR...       acrescenta itens a uma lista (sem repetir): skip_mods, only_mods, sources
  remove CHAVE VALOR...      tira itens de uma lista
  unset CHAVE                apaga a chave

Exemplos:
  mtrad config add skip_mods chipped rechiseled
  mtrad config set translate.engines.llm.model translategemma:4b
  mtrad config set translate.retry_style xml
  mtrad config get skip_mods

O arquivo é conferido antes de gravar (a gravação é recusada se ficar inválido)
e o anterior fica em ARQ.bak. O padrão de -c é mtrad.json.
`

// splitKey separa "a.b.c" em partes.
func splitKey(k string) ([]string, error) {
	if strings.TrimSpace(k) == "" {
		return nil, errors.New("chave vazia")
	}
	parts := strings.Split(k, ".")
	for _, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("chave inválida %q", k)
		}
	}
	return parts, nil
}

// lookup anda pelo mapa até o pai do último nome.
func lookup(m map[string]any, parts []string, create bool) (map[string]any, error) {
	cur := m
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p]
		if !ok || next == nil {
			if !create {
				return nil, fmt.Errorf("a chave %q não existe", strings.Join(parts, "."))
			}
			nm := map[string]any{}
			cur[p] = nm
			cur = nm
			continue
		}
		nm, ok := next.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%q não é um objeto", p)
		}
		cur = nm
	}
	return cur, nil
}

// parseValue lê VALOR como JSON; se não for JSON, é um texto.
func parseValue(s string) any {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err == nil && !dec.More() {
		return v
	}
	return s
}

func setKey(m map[string]any, key string, v any) error {
	parts, err := splitKey(key)
	if err != nil {
		return err
	}
	parent, err := lookup(m, parts, true)
	if err != nil {
		return err
	}
	parent[parts[len(parts)-1]] = v
	return nil
}

func getKey(m map[string]any, key string) (any, error) {
	parts, err := splitKey(key)
	if err != nil {
		return nil, err
	}
	parent, err := lookup(m, parts, false)
	if err != nil {
		return nil, err
	}
	v, ok := parent[parts[len(parts)-1]]
	if !ok {
		return nil, fmt.Errorf("a chave %q não existe", key)
	}
	return v, nil
}

func unsetKey(m map[string]any, key string) error {
	parts, err := splitKey(key)
	if err != nil {
		return err
	}
	parent, err := lookup(m, parts, false)
	if err != nil {
		return err
	}
	delete(parent, parts[len(parts)-1])
	return nil
}

// editList acrescenta ou tira textos de uma lista de strings.
func editList(m map[string]any, key string, add bool, items []string) error {
	parts, err := splitKey(key)
	if err != nil {
		return err
	}
	parent, err := lookup(m, parts, add)
	if err != nil {
		return err
	}
	name := parts[len(parts)-1]
	var cur []string
	if raw, ok := parent[name]; ok && raw != nil {
		list, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("%q não é uma lista", key)
		}
		for _, x := range list {
			s, ok := x.(string)
			if !ok {
				return fmt.Errorf("%q tem um item que não é texto", key)
			}
			cur = append(cur, s)
		}
	}
	has := map[string]bool{}
	for _, c := range cur {
		has[c] = true
	}
	if add {
		for _, it := range items {
			if !has[it] {
				cur = append(cur, it)
				has[it] = true
			}
		}
	} else {
		drop := map[string]bool{}
		for _, it := range items {
			drop[it] = true
		}
		var keep []string
		for _, c := range cur {
			if !drop[c] {
				keep = append(keep, c)
			}
		}
		cur = keep
	}
	if len(cur) == 0 {
		delete(parent, name) // lista vazia = chave ausente (os campos têm omitempty)
		return nil
	}
	out := make([]any, len(cur))
	for i, c := range cur {
		out[i] = c
	}
	parent[name] = out
	return nil
}

// editConfigFile aplica fn ao arquivo, confere o resultado e só então grava
// (guardando o anterior em .bak).
func editConfigFile(path string, fn func(m map[string]any) error) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	m := map[string]any{}
	if err := dec.Decode(&m); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := fn(m); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return err
	}
	if _, err := parseConfig(buf.Bytes(), path); err != nil {
		return fmt.Errorf("a alteração deixaria a configuração inválida, nada foi gravado: %w", err)
	}
	if bytes.Equal(bytes.TrimSpace(b), bytes.TrimSpace(buf.Bytes())) {
		return nil
	}
	if err := os.WriteFile(path+".bak", b, 0o644); err != nil {
		return err
	}
	return writeFileAtomic(path, buf.Bytes())
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func stageConfig(w io.Writer, path string, args []string) error {
	if len(args) == 0 {
		io.WriteString(os.Stderr, configUsage)
		return exitErr{schema.ExitUsage, "falta a ação (show, get, set, add, remove, unset)"}
	}
	act, rest := args[0], args[1:]
	need := func(n int, what string) error {
		if len(rest) < n {
			return exitErr{schema.ExitUsage, fmt.Sprintf("config %s: faltam argumentos (%s)", act, what)}
		}
		return nil
	}
	switch act {
	case "show":
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	case "get":
		if err := need(1, "CHAVE"); err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		m := map[string]any{}
		if err := dec.Decode(&m); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		v, err := getKey(m, rest[0])
		if err != nil {
			return err
		}
		out, _ := json.MarshalIndent(v, "", "  ")
		fmt.Fprintln(w, string(out))
		return nil
	case "set":
		if err := need(2, "CHAVE VALOR"); err != nil {
			return err
		}
		return editConfigFile(path, func(m map[string]any) error { return setKey(m, rest[0], parseValue(strings.Join(rest[1:], " "))) })
	case "unset":
		if err := need(1, "CHAVE"); err != nil {
			return err
		}
		return editConfigFile(path, func(m map[string]any) error { return unsetKey(m, rest[0]) })
	case "add", "remove":
		if err := need(2, "CHAVE VALOR..."); err != nil {
			return err
		}
		return editConfigFile(path, func(m map[string]any) error { return editList(m, rest[0], act == "add", rest[1:]) })
	}
	io.WriteString(os.Stderr, configUsage)
	return exitErr{schema.ExitUsage, "config: ação desconhecida: " + act}
}
