package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"mtrad/internal/schema"
	"mtrad/internal/ui"
)

const uiUsage = `uso: mtrad ui [-c mtrad.json] [-w PASTA_DE_TRABALHO] [--demo]

Abre o menu interativo (setas, Enter, Esc). Rodar "mtrad" sem argumentos num
terminal faz o mesmo. Tudo o que o menu faz tem um comando equivalente, e a tela
mostra qual é.

  -c ARQ     arquivo de configuração (padrão: ./mtrad.json, se existir, senão
             ~/.config/mtrad/mtrad.json; o setup do menu cria o arquivo)
  -w PASTA   pasta de trabalho (padrão: a do mtrad.json, ou ~/mtrad-trabalho;
             cada instância usa uma subpasta, e a pasta que já tem o trabalho de
             uma instância continua sendo dela)
  --demo     dados falsos: nada é executado nem gravado (para conhecer o menu)

Precisa de um terminal interativo e do comando stty (Linux e macOS).
`

// interactive diz se a entrada e a saída são um terminal.
func interactive() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		fi, err := f.Stat()
		if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
}

func stageUI(args []string) error {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("c", "", "arquivo de configuração")
	work := fs.String("w", "", "pasta de trabalho")
	demo := fs.Bool("demo", false, "dados falsos")
	if err := fs.Parse(args); err != nil {
		io.WriteString(os.Stderr, uiUsage)
		return exitErr{schema.ExitUsage, ""}
	}
	if !interactive() {
		return exitErr{schema.ExitUsage, "o menu precisa de um terminal interativo (entrada e saída ligadas a um terminal); sem terminal, use os comandos, por exemplo: mtrad run -i INSTÂNCIA -c mtrad.json"}
	}
	home, _ := os.UserHomeDir()
	var be ui.Backend
	if *demo {
		be = &ui.Demo{Delay: 80 * time.Millisecond}
	} else {
		c := *cfgPath
		if c == "" {
			c = defaultConfigPath(home)
		}
		abs, err := absPath(c)
		if err != nil {
			return err
		}
		be = newUIBackend(toolVersion, abs, *work, home)
	}
	term, err := ui.OpenTerminal()
	if err != nil {
		return err
	}
	defer term.Close()
	// se o programa for encerrado de fora, devolve o terminal ao normal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGHUP)
	go func() { <-sig; term.Close(); os.Exit(143) }()
	u := ui.New(term, os.Stdout, be, toolVersion, true, os.Getenv("NO_COLOR") == "", term.Size)
	if err := u.Run(); err != nil {
		return fmt.Errorf("%w", err)
	}
	return nil
}

func absPath(p string) (string, error) {
	home, _ := os.UserHomeDir()
	return filepath.Abs(expandHome(p, home))
}
