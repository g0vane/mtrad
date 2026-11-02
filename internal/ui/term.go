package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Key é uma tecla já interpretada.
type Key int

const (
	KNone Key = iota
	KRune
	KUp
	KDown
	KLeft
	KRight
	KEnter
	KEsc
	KBackspace
	KDelete
	KHome
	KEnd
	KPgUp
	KPgDn
	KCtrlC
	KEOF
)

// KeyEvent é uma tecla; R só vale quando K == KRune.
type KeyEvent struct {
	K Key
	R rune
}

// Input entrega as teclas à interface: o terminal de verdade ou, nos testes, uma lista.
type Input interface {
	// Next espera uma tecla.
	Next() KeyEvent
	// Poll espera uma tecla por pouco tempo (usado durante a tradução, em que a
	// tela se atualiza sozinha).
	Poll(d time.Duration) (KeyEvent, bool)
}

// Terminal lê teclas do terminal pondo-o em modo "uma tecla por vez" com o
// comando stty (Linux e macOS). Só a biblioteca padrão; sem o stty, não abre.
// O modo não desliga o processamento de saída, então "\n" continua voltando
// ao início da linha.
type Terminal struct {
	in     *os.File
	saved  string
	closed bool
	rows   int
	cols   int
	at     time.Time
	back   []byte // bytes lidos a mais depois de um Esc, devolvidos antes de ler de novo
}

// OpenTerminal prepara o terminal. Quem chama deve chamar Close (com defer).
func OpenTerminal() (*Terminal, error) {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return nil, fmt.Errorf("abra o programa num terminal interativo")
	}
	if _, err := exec.LookPath("stty"); err != nil {
		return nil, fmt.Errorf("não achei o comando stty, necessário para ler as setas")
	}
	t := &Terminal{in: os.Stdin}
	saved, err := t.stty("-g")
	if err != nil {
		return nil, fmt.Errorf("não consegui configurar o terminal: %w", err)
	}
	t.saved = strings.TrimSpace(saved)
	// sem eco, sem linha inteira, sem Ctrl+C/Ctrl+S como sinais (viram teclas);
	// espera até 0,1 s por tecla
	if _, err := t.stty("-icanon", "-echo", "-isig", "-ixon", "min", "0", "time", "1"); err != nil {
		return nil, fmt.Errorf("não consegui configurar o terminal: %w", err)
	}
	fmt.Fprint(os.Stdout, "\x1b[?1049h\x1b[?25l") // tela alternativa, cursor escondido
	return t, nil
}

func (t *Terminal) stty(args ...string) (string, error) {
	c := exec.Command("stty", args...)
	c.Stdin = t.in
	b, err := c.Output()
	return string(b), err
}

// Close devolve o terminal ao que era.
func (t *Terminal) Close() {
	if t == nil || t.closed {
		return
	}
	t.closed = true
	fmt.Fprint(os.Stdout, "\x1b[0m\x1b[?25h\x1b[?1049l")
	t.stty(t.saved)
}

// Size devolve linhas e colunas (guarda o valor por meio segundo).
func (t *Terminal) Size() (int, int) {
	if time.Since(t.at) < 500*time.Millisecond && t.rows > 0 {
		return t.rows, t.cols
	}
	t.rows, t.cols = 24, 80
	if out, err := t.stty("size"); err == nil {
		var r, c int
		if n, _ := fmt.Sscan(out, &r, &c); n == 2 && r > 0 && c > 0 {
			t.rows, t.cols = r, c
		}
	}
	t.at = time.Now()
	return t.rows, t.cols
}

func (t *Terminal) readByte() (byte, bool) {
	if n := len(t.back); n > 0 {
		b := t.back[0]
		t.back = t.back[1:]
		return b, true
	}
	var b [1]byte
	n, _ := t.in.Read(b[:])
	if n == 0 {
		return 0, false
	}
	return b[0], true
}

func (t *Terminal) Poll(time.Duration) (KeyEvent, bool) {
	b, ok := t.readByte()
	if !ok {
		return KeyEvent{}, false
	}
	return t.decode(b), true
}

func (t *Terminal) Next() KeyEvent {
	for {
		if ev, ok := t.Poll(0); ok && ev.K != KNone {
			return ev
		}
	}
}

func (t *Terminal) decode(b byte) KeyEvent {
	switch {
	case b == 3:
		return KeyEvent{K: KCtrlC}
	case b == 4:
		return KeyEvent{K: KEOF}
	case b == 13 || b == 10:
		return KeyEvent{K: KEnter}
	case b == 127 || b == 8:
		return KeyEvent{K: KBackspace}
	case b == 27:
		return t.escape()
	case b >= 32 && b < 127:
		return KeyEvent{K: KRune, R: rune(b)}
	case b >= 0xC0:
		n := 1
		switch {
		case b >= 0xF0:
			n = 3
		case b >= 0xE0:
			n = 2
		}
		buf := []byte{b}
		for i := 0; i < n; i++ {
			c, ok := t.readByte()
			if !ok {
				break
			}
			buf = append(buf, c)
		}
		if r, _ := utf8.DecodeRune(buf); r != utf8.RuneError {
			return KeyEvent{K: KRune, R: r}
		}
	}
	return KeyEvent{}
}

// escape lê o resto de uma sequência que começou com ESC. Sem mais bytes, é a tecla Esc.
func (t *Terminal) escape() KeyEvent {
	b, ok := t.readByte()
	if !ok {
		return KeyEvent{K: KEsc}
	}
	if b != '[' && b != 'O' {
		// o Esc veio sozinho e a próxima tecla chegou logo depois (menos de
		// 0,1 s): devolve-a para ser lida na vez seguinte, em vez de perdê-la
		t.back = append(t.back, b)
		return KeyEvent{K: KEsc}
	}
	var num []byte
	for {
		c, ok := t.readByte()
		if !ok {
			return KeyEvent{K: KEsc}
		}
		if c >= '0' && c <= '9' || c == ';' {
			num = append(num, c)
			continue
		}
		switch c {
		case 'A':
			return KeyEvent{K: KUp}
		case 'B':
			return KeyEvent{K: KDown}
		case 'C':
			return KeyEvent{K: KRight}
		case 'D':
			return KeyEvent{K: KLeft}
		case 'H':
			return KeyEvent{K: KHome}
		case 'F':
			return KeyEvent{K: KEnd}
		case '~':
			n, _ := strconv.Atoi(strings.Split(string(num), ";")[0])
			switch n {
			case 1, 7:
				return KeyEvent{K: KHome}
			case 4, 8:
				return KeyEvent{K: KEnd}
			case 3:
				return KeyEvent{K: KDelete}
			case 5:
				return KeyEvent{K: KPgUp}
			case 6:
				return KeyEvent{K: KPgDn}
			}
		}
		return KeyEvent{}
	}
}

// Scripted é um Input para testes: as teclas vêm de listas.
type Scripted struct {
	Keys     []KeyEvent // para Next
	PollKeys []KeyEvent // para Poll (durante a tradução)
}

func (s *Scripted) Next() KeyEvent {
	if len(s.Keys) == 0 {
		return KeyEvent{K: KEOF}
	}
	k := s.Keys[0]
	s.Keys = s.Keys[1:]
	return k
}

func (s *Scripted) Poll(d time.Duration) (KeyEvent, bool) {
	time.Sleep(time.Millisecond)
	if len(s.PollKeys) == 0 {
		return KeyEvent{}, false
	}
	k := s.PollKeys[0]
	s.PollKeys = s.PollKeys[1:]
	return k, true
}

// Script transforma "down down enter t:Olá esc q" em teclas. Palavras: up, down,
// left, right, enter, esc, bs, del, home, end, pgup, pgdn, ctrlc, eof, space;
// "t:texto" digita o texto; um único caractere é essa tecla.
func Script(spec string) []KeyEvent {
	var out []KeyEvent
	for _, w := range strings.Fields(spec) {
		switch w {
		case "up":
			out = append(out, KeyEvent{K: KUp})
		case "down":
			out = append(out, KeyEvent{K: KDown})
		case "left":
			out = append(out, KeyEvent{K: KLeft})
		case "right":
			out = append(out, KeyEvent{K: KRight})
		case "enter":
			out = append(out, KeyEvent{K: KEnter})
		case "esc":
			out = append(out, KeyEvent{K: KEsc})
		case "bs":
			out = append(out, KeyEvent{K: KBackspace})
		case "del":
			out = append(out, KeyEvent{K: KDelete})
		case "home":
			out = append(out, KeyEvent{K: KHome})
		case "end":
			out = append(out, KeyEvent{K: KEnd})
		case "pgup":
			out = append(out, KeyEvent{K: KPgUp})
		case "pgdn":
			out = append(out, KeyEvent{K: KPgDn})
		case "ctrlc":
			out = append(out, KeyEvent{K: KCtrlC})
		case "eof":
			out = append(out, KeyEvent{K: KEOF})
		case "space":
			out = append(out, KeyEvent{K: KRune, R: ' '})
		default:
			if strings.HasPrefix(w, "t:") {
				for _, r := range w[2:] {
					if r == '_' {
						r = ' '
					}
					out = append(out, KeyEvent{K: KRune, R: r})
				}
			} else if utf8.RuneCountInString(w) == 1 {
				r, _ := utf8.DecodeRuneInString(w)
				out = append(out, KeyEvent{K: KRune, R: r})
			} else {
				panic("tecla desconhecida no roteiro: " + w)
			}
		}
	}
	return out
}
