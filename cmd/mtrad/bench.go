package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mtrad/internal/bench"
	"mtrad/internal/fmtx"
	"mtrad/internal/schema"
	"mtrad/internal/translate"
)

// isLocalEngine diz se o motor roda neste computador. Só o que fala com o
// endereço de loopback (ou um processo filho) conta como local: qualquer outro
// endereço, mesmo da rede de casa, é tratado como "fora do computador".
func isLocalEngine(ec translate.EngineConfig) bool {
	switch ec.Type {
	case "worker", "echo":
		return true
	}
	u, err := url.Parse(ec.BaseURL)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// pendingWords soma as palavras dos textos que ainda faltam traduzir na pasta
// de trabalho (protected.jsonl menos o que já está em translated.jsonl).
// Falso se não há como saber.
func pendingWords(work string) (int64, bool) {
	done := map[string]bool{}
	_ = schema.Each(filepath.Join(work, "translated.jsonl"), schema.VTranslated, func(r schema.TranslatedRec) error {
		done[r.ID] = true
		return nil
	})
	var total int64
	n := 0
	err := schema.Each(filepath.Join(work, "protected.jsonl"), schema.VProtected, func(r schema.ProtectedRec) error {
		n++
		if !done[r.ID] {
			total += int64(translate.CountWords(r.Masked))
		}
		return nil
	})
	if err != nil || n == 0 {
		return 0, false
	}
	return total, true
}

// benchReport escreve o resultado de um motor. words > 0 acrescenta quanto
// tempo levaria traduzir essa quantidade de palavras.
func benchReport(w io.Writer, r bench.Result, words int64, rpm int) {
	label := r.Name
	if r.Model != "" {
		label += " (" + r.Model + ")"
	}
	fmt.Fprintf(w, "\nmotor %s\n", label)
	if r.Err != nil {
		fmt.Fprintf(w, "  não consegui medir: %v\n", r.Err)
		return
	}
	fmt.Fprintf(w, "  primeira resposta  %s   (inclui carregar o modelo; fora das médias)\n", fmtx.Ms(r.Cold))
	if !r.OK() {
		fmt.Fprintln(w, "  poucas frases para medir: use -n maior")
		return
	}
	fmt.Fprintf(w, "  medido             %d textos, %d palavras, em %s\n", r.Texts, r.Words, fmtx.Duration(r.Elapsed))
	fmt.Fprintf(w, "  velocidade         %s palavras/s · %s textos/s\n", fmtx.Decimal1(r.WordsPerSec()), fmtx.Decimal1(r.TextsPerSec()))
	fmt.Fprintf(w, "  tempo por pedido   mediana %s · p95 %s\n", fmtx.Ms(r.Percentile(50)), fmtx.Ms(r.Percentile(95)))
	if r.Failed > 0 {
		fmt.Fprintf(w, "  falhas             %d de %d textos\n", r.Failed, r.Failed+r.Texts)
	}
	if rpm > 0 {
		fmt.Fprintf(w, "  limite             %d pedidos por minuto na configuração: o ritmo real fica abaixo do medido\n", rpm)
	}
	if d, ok := r.Estimate(words); ok {
		fmt.Fprintf(w, "  estimativa         %d palavras levariam cerca de %s\n", words, fmtx.Duration(d))
	}
}

func stageBench(ctx context.Context, w io.Writer, configPath, only string, n int, words int64, wordsSet, allowCloud bool, work string) error {
	if configPath == "" {
		return exitErr{schema.ExitUsage, "bench: informe o arquivo de configuração com -c"}
	}
	if n < 2 || n > len(bench.Samples) {
		return exitErr{schema.ExitUsage, fmt.Sprintf("bench: -n deve ficar entre 2 e %d", len(bench.Samples))}
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(cfg.Translate.Engines))
	for name := range cfg.Translate.Engines {
		names = append(names, name)
	}
	sort.Strings(names)
	if only != "" {
		if _, ok := cfg.Translate.Engines[only]; !ok {
			return exitErr{schema.ExitUsage, fmt.Sprintf("bench: o motor %q não existe na configuração (há: %s)", only, strings.Join(names, ", "))}
		}
		names = []string{only}
	}
	if !wordsSet {
		if pw, ok := pendingWords(work); ok {
			words = pw
			fmt.Fprintf(w, "bench: faltam traduzir %d palavras em %s; a estimativa usa esse número\n", pw, work)
		}
	}
	samples := bench.Samples[:n]
	measured := 0
	for _, name := range names {
		ec := cfg.Translate.Engines[name]
		switch {
		case ec.Type == "echo":
			fmt.Fprintf(w, "\nmotor %s: pulei (motor de teste, só copia o texto)\n", name)
			continue
		case !isLocalEngine(ec) && !allowCloud:
			fmt.Fprintf(w, "\nmotor %s: pulei porque é um motor fora do seu computador (%s). O teste envia %d frases de exemplo a ele e o uso pode ser cobrado; para medir mesmo assim, use --allow-cloud\n", name, hostOf(ec), n)
			continue
		}
		eng, err := translate.NewEngine(name, ec, os.Getenv)
		if err != nil {
			benchReport(w, bench.Result{Name: name, Model: ec.Model, Err: err}, 0, 0)
			continue
		}
		opts := bench.Options{Samples: samples}
		if ec.RPM > 0 {
			opts.Wait = translate.NewLimiter(ec.RPM, 0, "").Wait
		}
		r := bench.Run(ctx, eng, opts)
		if c, ok := eng.(interface{ Close() }); ok {
			c.Close()
		}
		benchReport(w, r, words, ec.RPM)
		if r.OK() {
			measured++
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	fmt.Fprintln(w, "\nnão medido: o tempo até a primeira palavra (o mtrad pede a resposta inteira) e o pico de memória da placa de vídeo")
	if measured == 0 {
		return exitErr{schema.ExitError, "bench: nenhum motor foi medido"}
	}
	return nil
}

func hostOf(ec translate.EngineConfig) string {
	if u, err := url.Parse(ec.BaseURL); err == nil && u.Host != "" {
		return u.Host
	}
	return "endereço desconhecido"
}
