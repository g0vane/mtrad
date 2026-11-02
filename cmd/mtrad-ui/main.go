// mtrad-ui é o protótipo separado do menu interativo do mtrad: roda com dados
// falsos (nada é traduzido, instalado ou apagado) para a interface ser refinada
// antes de entrar no mtrad de verdade. Teclas: setas, Enter, Espaço, Esc.
//
//	mtrad-ui                 começa como primeira vez (só "Configurar")
//	mtrad-ui --configurado   começa como se o setup já tivesse sido feito
//	mtrad-ui --sem-ollama    simula um computador sem Ollama
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mtrad/internal/ui"
)

const version = "0.10 (protótipo)"

func main() {
	demo := &ui.Demo{Delay: 80 * time.Millisecond}
	for _, a := range os.Args[1:] {
		switch a {
		case "--configurado", "-c":
			demo.Done = true
		case "--sem-ollama":
			demo.Broken = true
		case "-h", "--help":
			fmt.Println("uso: mtrad-ui [--configurado] [--sem-ollama]\nTeclas: ↑↓ mover, Enter escolher, Espaço marcar, Esc voltar, i estilo, ? ajuda")
			return
		default:
			fmt.Fprintln(os.Stderr, "opção desconhecida:", a)
			os.Exit(2)
		}
	}
	term, err := ui.OpenTerminal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
	defer term.Close()
	// se o programa for encerrado de fora, devolve o terminal ao normal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGHUP)
	go func() { <-sig; term.Close(); os.Exit(143) }()
	color := os.Getenv("NO_COLOR") == ""
	u := ui.New(term, os.Stdout, demo, version, true, color, term.Size)
	if err := u.Run(); err != nil {
		term.Close()
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}
