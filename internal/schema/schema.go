// Package schema guarda os tipos dos arquivos que os módulos trocam entre si
// e a leitura/gravação deles. É a única coisa que os módulos têm em comum.
package schema

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Nomes de esquema (nome/versão). Mudança que quebra o formato sobe o número.
const (
	VInstance   = "instance/1"
	VInventory  = "inventory/1"
	VStrings    = "strings/1"
	VTodo       = "todo/1"
	VReused     = "reused/1"
	VProtected  = "protected/1"
	VRouted     = "routed/1"
	VTranslated = "translated/1"
	VOK         = "ok/1"
	VFailed     = "failed/1"
	VErrors     = "errors/1"
	VGlossary   = "glossary/1"
	VDone       = "done/1"
	VDocs       = "docs/1"
)

// Códigos de saída.
const (
	ExitOK      = 0
	ExitError   = 1
	ExitUsage   = 2
	ExitPartial = 3
)

// ---- tipos dos arquivos ----

type PackFormat struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

type Instance struct {
	Schema        string     `json:"schema"`
	InstanceDir   string     `json:"instance_dir"`
	GameDir       string     `json:"game_dir"`
	McVersion     string     `json:"mc_version"`
	Loader        string     `json:"loader"`
	LoaderVersion string     `json:"loader_version"`
	PackFormat    PackFormat `json:"pack_format"`
	How           string     `json:"how"`
	Warnings      []string   `json:"warnings,omitempty"`
}

type LangRef struct {
	NS     string `json:"ns"`
	Locale string `json:"locale"`
	Path   string `json:"path"`
}

type JarInfo struct {
	Chain   []string  `json:"chain"` // [caminho relativo ao game_dir, jar aninhado, ...]
	SHA256  string    `json:"sha256"`
	Size    int64     `json:"size"`
	ModID   string    `json:"mod_id"`
	Version string    `json:"version"`
	Name    string    `json:"name,omitempty"`
	Loader  string    `json:"loader,omitempty"` // fabric, forge, neoforge, forge-legacy
	Langs   []LangRef `json:"langs"`
	Notes   []string  `json:"notes,omitempty"`
}

type Inventory struct {
	Schema  string    `json:"schema"`
	GameDir string    `json:"game_dir"`
	Jars    []JarInfo `json:"jars"`
}

// Tipos de texto que não são arquivo de idioma (campo Kind dos registros).
const (
	KindPatchouli = "patchouli"
	KindFTBQ      = "ftbq"  // lang do FTB Quests (1.21 em diante)
	KindFTBQInl   = "ftbqi" // texto dentro dos capítulos (1.20.1 e antes)
	// KindFTBQLang: texto de missões que mora num arquivo de idioma comum (por
	// exemplo o kubejs/assets/ftbquestlocalizer/lang/en_us.json que o FTB Quest
	// Localizer gera). Usa as marcas do FTB Quests, mas o pack o trata como idioma.
	KindFTBQLang = "ftbql"
	// KindFTBQRich: texto dentro de uma linha de JSON de texto rico de um
	// capítulo (1.20.1 e antes), por exemplo {"text":"Olá","color":"gold"}. O
	// texto volta para o JSON no lugar (não passa pelo lang do pack); o Loc.Ptr
	// é "<caminho da linha>#<caminho dentro do JSON>".
	KindFTBQRich = "ftbqr"
)

// IsFTBQ diz se o tipo é de missões do FTB Quests (qualquer era): usa as
// marcas "&a", "&#RRGGBB", "{image:...}" e "{@pagebreak}".
func IsFTBQ(kind string) bool {
	return kind == KindFTBQ || kind == KindFTBQInl || kind == KindFTBQLang || kind == KindFTBQRich
}

// Loc diz onde um texto mora dentro de um documento (Patchouli, FTB Quests).
// Doc é o caminho de saída do documento; Ptr é o caminho até o texto, no estilo
// JSON Pointer ("/pages/0/text"). Textos de arquivos de idioma não têm Loc.
type Loc struct {
	Doc string `json:"doc"`
	Ptr string `json:"ptr"`
}

// DocRec descreve um documento que o pack ou o install precisa reescrever:
// Orig é a cópia do original em work/docs, Out é onde o resultado vai (dentro do
// zip para kind patchouli; relativo a work/out para ftbq).
type DocRec struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Mod    string `json:"mod"`
	Orig   string `json:"orig"`
	Out    string `json:"out"`
	Format string `json:"format"` // json | snbt | json5
	Src    string `json:"src"`
}

type StringRec struct {
	ID    string  `json:"id"`
	Mod   string  `json:"mod"`
	NS    string  `json:"ns"`
	Key   string  `json:"key"`
	En    string  `json:"en"`
	EnSha string  `json:"en_sha"`
	Pt    *string `json:"pt"`
	Src   string  `json:"src"`
	Kind  string  `json:"kind,omitempty"` // vazio = arquivo de idioma
	Loc   *Loc    `json:"loc,omitempty"`
}

type TodoRec struct {
	ID     string `json:"id"`
	Mod    string `json:"mod"`
	NS     string `json:"ns"`
	Key    string `json:"key"`
	Reason string `json:"reason"` // missing | identical
	En     string `json:"en"`
	EnSha  string `json:"en_sha"`
	Kind   string `json:"kind,omitempty"` // vazio = arquivo de idioma
	Loc    *Loc   `json:"loc,omitempty"`
}

type ReusedRec struct {
	ID   string `json:"id"`
	Mod  string `json:"mod"`
	NS   string `json:"ns"`
	Key  string `json:"key"`
	Pt   string `json:"pt"`
	Kind string `json:"kind,omitempty"` // vazio = arquivo de idioma
	Loc  *Loc   `json:"loc,omitempty"`
}

type Token struct {
	T   string `json:"t"`   // marca no texto protegido
	Raw string `json:"raw"` // trecho original
	// Class diz que tipo de marca é (placeholder, style, reset, tag-open...;
	// ver o pacote protect). Vazio em arquivos feitos antes de existir o campo.
	// Nada no pipeline decide com base nela ainda: é só informação.
	Class string `json:"class,omitempty"`
}

type ProtectedRec struct {
	ID     string  `json:"id"`
	Masked string  `json:"masked"`
	Tokens []Token `json:"tokens"`
	Style  string  `json:"style"`
}

type RoutedRec struct {
	ID     string `json:"id"`
	Engine string `json:"engine"`
}

type TranslatedRec struct {
	ID      string `json:"id"`
	Engine  string `json:"engine"`
	Model   string `json:"model"`
	Out     string `json:"out"`
	Attempt int    `json:"attempt"`
	Ms      int64  `json:"ms"`
	// MSha é o SHA-256 do texto protegido que gerou Out; vazio nos arquivos
	// antigos (aceitos como válidos).
	MSha string `json:"msha,omitempty"`
}

type OKRec struct {
	ID     string `json:"id"`
	Mod    string `json:"mod"`
	NS     string `json:"ns"`
	Key    string `json:"key"`
	Pt     string `json:"pt"`
	Engine string `json:"engine"`
	Kind   string `json:"kind,omitempty"` // vazio = arquivo de idioma
	Loc    *Loc   `json:"loc,omitempty"`
}

type FailedRec struct {
	ID     string   `json:"id"`
	NS     string   `json:"ns"`
	Key    string   `json:"key"`
	Engine string   `json:"engine"`
	Out    string   `json:"out"`
	Reason []string `json:"reason"`
	Kind   string   `json:"kind,omitempty"` // vazio = arquivo de idioma
	Loc    *Loc     `json:"loc,omitempty"`
}

type ErrRec struct {
	Stage string `json:"stage"`
	Src   string `json:"src,omitempty"`
	ID    string `json:"id,omitempty"`
	Msg   string `json:"msg"`
	Time  string `json:"time"`
}

type Done struct {
	Schema  string            `json:"schema"`
	Tool    string            `json:"tool"`
	Stage   string            `json:"stage"`
	Inputs  map[string]string `json:"inputs"`
	Outputs map[string]string `json:"outputs"`
	Counts  map[string]int    `json:"counts"`
	Time    string            `json:"time"`
}

// ---- identidade ----

// ID: 16 primeiros caracteres hexadecimais do SHA-256 de mod, ns e key
// separados por um byte zero.
func ID(mod, ns, key string) string {
	h := sha256.Sum256([]byte(mod + "\x00" + ns + "\x00" + key))
	return hex.EncodeToString(h[:8])
}

// Sha: hash curto de um texto (usado como en_sha).
func Sha(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:8])
}

// FileSHA256 devolve o SHA-256 completo de um arquivo.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ---- JSON simples (arquivo único) ----

func WriteJSON(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return atomicWrite(path, buf.Bytes())
}

// ReadJSON lê um JSON e confere o campo "schema" (se o tipo tiver um).
func ReadJSON(path string, v any, want string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var probe struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if want != "" {
		if err := CheckVersion(want, probe.Schema); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := WriteFileSync(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WriteFileSync grava o arquivo e o manda para o disco (fsync) antes de fechar.
// Usado para o arquivo temporário que depois é renomeado: sem o fsync, uma queda
// de energia pode deixar o rename feito e o conteúdo vazio.
func WriteFileSync(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// CheckVersion recusa um esquema de outro nome ou de versão maior que a conhecida.
func CheckVersion(want, got string) error {
	wn, wv := splitSchema(want)
	gn, gv := splitSchema(got)
	if gn == "" {
		return errors.New("arquivo sem campo de esquema")
	}
	if gn != wn {
		return fmt.Errorf("esquema %q, esperado %q", got, want)
	}
	if gv > wv {
		return fmt.Errorf("esquema %q é mais novo que o conhecido (%q)", got, want)
	}
	return nil
}

func splitSchema(s string) (string, int) {
	i := strings.LastIndex(s, "/")
	if i < 0 {
		return "", 0
	}
	n, _ := strconv.Atoi(s[i+1:])
	return s[:i], n
}

// ---- JSONL ----

type header struct {
	Schema string `json:"_schema"`
}

// Writer grava um JSONL em arquivo .tmp e renomeia no Close.
type Writer struct {
	f    *os.File
	bw   *bufio.Writer
	enc  *json.Encoder
	path string
	tmp  string
	N    int
}

func Create(path, schema string) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	w := &Writer{f: f, bw: bufio.NewWriterSize(f, 1<<20), path: path, tmp: tmp}
	w.enc = json.NewEncoder(w.bw)
	w.enc.SetEscapeHTML(false)
	if err := w.enc.Encode(header{schema}); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

func (w *Writer) Write(v any) error {
	if err := w.enc.Encode(v); err != nil {
		return err
	}
	w.N++
	return nil
}

func (w *Writer) Close() error {
	if err := w.bw.Flush(); err != nil {
		w.f.Close()
		return err
	}
	if err := w.f.Sync(); err != nil {
		w.f.Close()
		return err
	}
	if err := w.f.Close(); err != nil {
		return err
	}
	return os.Rename(w.tmp, w.path)
}

// Abort descarta o arquivo temporário.
func (w *Writer) Abort() {
	w.f.Close()
	os.Remove(w.tmp)
}

// Appender só acrescenta linhas (translated.jsonl, errors.jsonl). Seguro para
// uso por várias goroutines.
type Appender struct {
	mu    sync.Mutex
	f     *os.File
	buf   bytes.Buffer
	enc   *json.Encoder             // codifica em buf; cada registro vai ao arquivo numa só gravação
	write func([]byte) (int, error) // f.Write (os testes trocam)
	N     int
}

// OnRepair é chamado quando OpenAppend corta o resto de uma linha gravada pela
// metade (dropped = bytes removidos). O padrão avisa no stderr.
var OnRepair = func(path string, dropped int64) {
	fmt.Fprintf(os.Stderr, "aviso: %s terminava numa linha incompleta (execução interrompida); cortei %d bytes e segui\n", path, dropped)
}

// repairTail corta o final de um arquivo que não termina em quebra de linha
// (gravação interrompida): sem isso, o próximo registro seria colado nessa
// linha e estragaria o arquivo de vez. Devolve quantos bytes cortou.
func repairTail(path string) (int64, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return 0, err
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, st.Size()-1); err != nil {
		return 0, err
	}
	if last[0] == '\n' {
		return 0, nil
	}
	// procura a última quebra de linha, de trás para frente
	const chunk = 64 << 10
	end := st.Size()
	keep := int64(0)
	buf := make([]byte, chunk)
	for end > 0 {
		start := end - chunk
		if start < 0 {
			start = 0
		}
		b := buf[:end-start]
		if _, err := f.ReadAt(b, start); err != nil {
			return 0, err
		}
		if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
			keep = start + int64(i) + 1
			break
		}
		end = start
	}
	// se o resto é um JSON completo, só faltava a quebra de linha: mantém
	tail := make([]byte, st.Size()-keep)
	if _, err := f.ReadAt(tail, keep); err != nil {
		return 0, err
	}
	if json.Valid(tail) {
		_, err := f.WriteAt([]byte("\n"), st.Size())
		return 0, err
	}
	if err := f.Truncate(keep); err != nil {
		return 0, err
	}
	return st.Size() - keep, nil
}

func OpenAppend(path, schema string) (*Appender, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	_, statErr := os.Stat(path)
	isNew := os.IsNotExist(statErr)
	if !isNew {
		dropped, err := repairTail(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if dropped > 0 && OnRepair != nil {
			OnRepair(path, dropped)
		}
		// confere o cabeçalho do arquivo existente
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		line, err := bufio.NewReader(f).ReadBytes('\n')
		f.Close()
		if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
			if errors.Is(err, io.EOF) {
				isNew = true // arquivo vazio (ou só tinha um cabeçalho cortado)
			} else {
				return nil, err
			}
		}
		if !isNew {
			var h header
			if err := json.Unmarshal(line, &h); err != nil {
				return nil, fmt.Errorf("%s: cabeçalho inválido: %w", path, err)
			}
			if err := CheckVersion(schema, h.Schema); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	a := &Appender{f: f, write: f.Write}
	a.enc = json.NewEncoder(&a.buf)
	a.enc.SetEscapeHTML(false)
	if isNew {
		if err := a.put(header{schema}); err != nil {
			f.Close()
			return nil, err
		}
	}
	return a, nil
}

func (a *Appender) Write(v any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.put(v); err != nil {
		return err
	}
	a.N++
	return nil
}

// put codifica v e grava a linha inteira de uma vez. Se a gravação falhar no
// meio (disco cheio), tira do arquivo o pedaço que entrou: senão o próximo
// registro ficaria colado numa linha quebrada no meio do arquivo, o que é erro
// de leitura (só uma linha quebrada no fim é tolerada).
func (a *Appender) put(v any) error {
	a.buf.Reset()
	if err := a.enc.Encode(v); err != nil {
		return err
	}
	n, err := a.write(a.buf.Bytes())
	if err != nil {
		if n > 0 {
			if st, e := a.f.Stat(); e == nil && st.Size() >= int64(n) {
				_ = a.f.Truncate(st.Size() - int64(n))
			}
		}
		return err
	}
	return nil
}

func (a *Appender) Close() error { return a.f.Close() }

// Each lê um JSONL, confere o cabeçalho e chama fn por registro.
// Se o arquivo não existir devolve um erro que satisfaz os.IsNotExist.
//
// Uma última linha sem quebra de linha e que não é um JSON válido é o rastro de
// uma gravação interrompida (queda de energia, kill -9): ela é ignorada, não
// derruba a leitura. Uma linha estragada no meio do arquivo continua sendo erro.
func Each[T any](path, schema string, fn func(T) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<20)
	line, err := br.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return fmt.Errorf("%s: arquivo vazio", path)
	}
	var h header
	if err := json.Unmarshal(line, &h); err != nil {
		if !bytes.HasSuffix(line, []byte("\n")) {
			return nil // cabeçalho cortado no meio: o arquivo ainda não tem registros
		}
		return fmt.Errorf("%s: cabeçalho inválido: %w", path, err)
	}
	if err := CheckVersion(schema, h.Schema); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	n := 1
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			n++
			var v T
			if jerr := json.Unmarshal(line, &v); jerr != nil {
				if err != nil && !bytes.HasSuffix(line, []byte("\n")) {
					return nil // última linha cortada
				}
				return fmt.Errorf("%s:%d: %w", path, n, jerr)
			}
			if ferr := fn(v); ferr != nil {
				return ferr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func ReadAll[T any](path, schema string) ([]T, error) {
	var out []T
	err := Each(path, schema, func(v T) error { out = append(out, v); return nil })
	return out, err
}

// ---- erros, retomada e trava ----

// ErrLog grava errors.jsonl de uma pasta de trabalho.
type ErrLog struct {
	a         *Appender
	stage     string
	mu        sync.Mutex
	writeErrs int
}

func OpenErrLog(dir, stage string) (*ErrLog, error) {
	a, err := OpenAppend(filepath.Join(dir, "errors.jsonl"), VErrors)
	if err != nil {
		return nil, err
	}
	return &ErrLog{a: a, stage: stage}, nil
}

func (e *ErrLog) Add(src, id, msg string) {
	if err := e.a.Write(ErrRec{Stage: e.stage, Src: src, ID: id, Msg: msg, Time: time.Now().Format(time.RFC3339)}); err != nil {
		e.mu.Lock()
		e.writeErrs++
		first := e.writeErrs == 1
		e.mu.Unlock()
		if first {
			fmt.Fprintf(os.Stderr, "mtrad: não consegui gravar errors.jsonl (%v); os avisos desta etapa podem estar incompletos\n", err)
		}
	}
}

// WriteErrors diz quantas vezes a gravação do registro de erros falhou.
func (e *ErrLog) WriteErrors() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.writeErrs
}

func (e *ErrLog) Count() int { return e.a.N }
func (e *ErrLog) Close()     { e.a.Close() }

// WriteDone grava <etapa>.done.json com os SHA-256 das entradas e saídas.
func WriteDone(dir, stage string, inputs, outputs []string, counts map[string]int) error {
	d := Done{Schema: VDone, Tool: "mtrad", Stage: stage, Inputs: map[string]string{}, Outputs: map[string]string{}, Counts: counts, Time: time.Now().Format(time.RFC3339)}
	for _, n := range inputs {
		h, err := FileSHA256(filepath.Join(dir, n))
		if err != nil {
			return err
		}
		d.Inputs[n] = h
	}
	for _, n := range outputs {
		h, err := FileSHA256(filepath.Join(dir, n))
		if err != nil {
			return err
		}
		d.Outputs[n] = h
	}
	return WriteJSON(filepath.Join(dir, stage+".done.json"), d)
}

// RequireFresh confere se o arquivo de entrada ainda é o que a etapa anterior
// deixou. Se a etapa anterior nunca rodou (sem .done.json) devolve nil: só
// avisa quem chama que não há como conferir.
func RequireFresh(dir, upstream, file string) (checked bool, err error) {
	if SkipFreshness {
		return false, nil
	}
	var d Done
	p := filepath.Join(dir, upstream+".done.json")
	if _, serr := os.Stat(p); serr != nil {
		return false, nil
	}
	if err := ReadJSON(p, &d, VDone); err != nil {
		return false, err
	}
	want, ok := d.Outputs[file]
	if !ok {
		return false, nil
	}
	got, err := FileSHA256(filepath.Join(dir, file))
	if err != nil {
		return false, err
	}
	if got != want {
		return true, fmt.Errorf("%s mudou depois de a etapa %s terminar; rode a etapa de novo ou use --force", file, upstream)
	}
	return true, nil
}

// SkipFreshness desliga a conferência de RequireFresh (opção --force).
var SkipFreshness bool

// Lock cria work/.lock com o pid de quem o criou; devolve a função que o
// remove. Se a trava existe mas o processo dono dela não existe mais (a
// execução caiu), ela é removida e a trava é tomada. Limite conhecido: se o
// sistema reaproveitar o pid para outro programa, a trava parece viva; nesse
// caso use `mtrad unlock`.
func Lock(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, ".lock")
	for try := 0; ; try++ {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			mine := fmt.Sprintf("%d\n", os.Getpid())
			f.WriteString(mine)
			f.Close()
			// só remove a trava se ela ainda é a nossa (depois de `mtrad unlock`
			// ou de uma tomada, o arquivo pode ser de outra execução)
			return func() {
				if b, err := os.ReadFile(p); err == nil && string(b) == mine {
					os.Remove(p)
				}
			}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		b, _ := os.ReadFile(p)
		txt := strings.TrimSpace(string(b))
		if try == 0 && lockIsStale(p, txt) && takeOverStale(p, txt) {
			fmt.Fprintf(os.Stderr, "aviso: a trava %s era de uma execução que caiu (pid %s); removida\n", p, txt)
			continue
		}
		return nil, fmt.Errorf("outra execução usa %s (pid %s); se não houver, rode `mtrad unlock`", dir, txt)
	}
}

// takeOverStale tira do caminho a trava velha com o conteúdo txt. Tirar é um
// rename (atômico) para um nome só nosso, e só depois se confere o que foi
// tirado: se, entre a leitura e o rename, outra execução já tinha trocado a
// trava velha por uma nova, a nova é devolvida (link sem sobrescrever) e a
// tomada é desfeita.
func takeOverStale(p, txt string) bool {
	mine := fmt.Sprintf("%s.stale-%d", p, os.Getpid())
	if err := os.Rename(p, mine); err != nil {
		return false
	}
	defer os.Remove(mine)
	b, _ := os.ReadFile(mine)
	if strings.TrimSpace(string(b)) == txt {
		return true
	}
	_ = os.Link(mine, p) // falha se alguém já criou outra: então fica a dele
	return false
}

// LockInfo diz se há trava em dir e, havendo, de qual processo e se ele ainda
// existe (fora do Unix não há como saber: alive é sempre verdadeiro).
func LockInfo(dir string) (exists bool, pid int, alive bool) {
	b, err := os.ReadFile(filepath.Join(dir, ".lock"))
	if err != nil {
		return false, 0, false
	}
	pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return true, 0, false
	}
	return true, pid, pid == os.Getpid() || pidAlive(pid)
}

// lockIsStale diz se a trava é de um processo que já não existe. Uma trava sem
// pid legível só vale como velha se tiver mais de 10 s (o dono pode estar
// escrevendo o pid agora).
func lockIsStale(path, content string) bool {
	pid, err := strconv.Atoi(content)
	if err != nil || pid <= 0 {
		st, serr := os.Stat(path)
		return serr == nil && time.Since(st.ModTime()) > 10*time.Second
	}
	if pid == os.Getpid() {
		return false
	}
	return !pidAlive(pid)
}

// SwapDir troca a pasta final pela pasta nova (tmp) já pronta: a antiga só
// sai do caminho depois de a nova existir, e volta se a troca falhar. Assim uma
// falha no meio da geração nunca deixa a saída anterior destruída.
func SwapDir(tmp, final string) error {
	old := final + ".old"
	if err := os.RemoveAll(old); err != nil {
		return err
	}
	hadOld := true
	if err := os.Rename(final, old); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		hadOld = false
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		if hadOld {
			os.Rename(old, final)
		}
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		if hadOld {
			os.Rename(old, final)
		}
		return err
	}
	return os.RemoveAll(old)
}
