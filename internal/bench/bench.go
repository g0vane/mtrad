// Package bench mede a velocidade de um motor de tradução com frases de
// exemplo: palavras por segundo, latência por pedido e quanto tempo levaria
// um modpack inteiro. Só biblioteca padrão.
//
// O que NÃO mede: o tempo até o primeiro token (o mtrad pede a resposta
// inteira, sem streaming) e o pico de memória da placa de vídeo (não há leitura
// da placa durante o teste).
package bench

import (
	"context"
	"fmt"
	"sort"
	"time"

	"mtrad/internal/translate"
)

// Samples são frases curtas no estilo de interface de mods, já com as marcas
// ⟦n⟧ que o mtrad usa no lugar de %s, cores e afins. São genéricas e escritas
// para o teste: nenhum texto de mod de verdade sai do computador.
var Samples = []string{
	"Right-click to open the machine's inventory.",
	"Hold ⟦1⟧Shift⟦2⟧ to see more details.",
	"This block can only be placed on solid ground.",
	"⟦1⟧ items were moved to your backpack.",
	"The ancient chest is locked. Find the matching key.",
	"Crafting speed is increased by ⟦1⟧ when powered.",
	"Collect ten pieces of iron ore from the nearby cave.",
	"Warning: this action cannot be undone.",
	"Fuel remaining: ⟦1⟧ seconds",
	"Place the pipe next to the tank to start transferring fluids.",
	"Deals extra damage to undead creatures.",
	"Your quest book has been updated.",
	"Requires a nether star to activate.",
	"The generator produces ⟦1⟧ units of energy per tick.",
	"Combine two rods in the workbench to build a stronger tool.",
	"Enable this option to show the item names above the hotbar.",
	"The village elder has a new task for you.",
	"Insert a battery to restore the lost power.",
	"Use a wrench to rotate the block without breaking it.",
	"Stored: ⟦1⟧ / ⟦2⟧ buckets",
	"A mysterious glow comes from beneath the surface.",
	"Smelting this ore gives a rare shiny ingot.",
	"Press ⟦1⟧ to toggle the minimap on and off.",
	"The spell fails if the target is too far away.",
	"Rename the item at an anvil to give it a custom label.",
	"Only one of these can be active at a time.",
	"Right-click the crop with bone meal to grow it faster.",
	"Redstone signal strength required: ⟦1⟧",
	"Throw the potion at your enemies to slow them down.",
	"This structure generates only in cold biomes.",
	"The machine needs a stable water source nearby.",
	"Congratulations! You have unlocked a new recipe.",
}

// Result é o resultado de um motor.
type Result struct {
	Name, Model string
	// Cold é a primeira resposta, que costuma incluir carregar o modelo na
	// memória (ou subir o processo do motor). Fica fora das médias.
	Cold time.Duration
	// Calls e Texts contam só o que veio depois da primeira resposta.
	Calls, Texts, Failed int
	Words                int
	Elapsed              time.Duration // soma do tempo das chamadas medidas
	Latencies            []time.Duration
	// Err é o erro que parou o teste (motor fora do ar, chave recusada...).
	Err error
}

// OK diz se houve ao menos uma medida válida.
func (r Result) OK() bool { return r.Err == nil && r.Calls > 0 && r.Elapsed > 0 }

func (r Result) WordsPerSec() float64 {
	if r.Elapsed <= 0 {
		return 0
	}
	return float64(r.Words) / r.Elapsed.Seconds()
}

func (r Result) TextsPerSec() float64 {
	if r.Elapsed <= 0 {
		return 0
	}
	return float64(r.Texts) / r.Elapsed.Seconds()
}

// Percentile devolve a latência no percentil p (0 a 100) pelo método do
// vizinho mais próximo; 0 se não há medidas.
func (r Result) Percentile(p float64) time.Duration {
	if len(r.Latencies) == 0 {
		return 0
	}
	l := append([]time.Duration(nil), r.Latencies...)
	sort.Slice(l, func(i, j int) bool { return l[i] < l[j] })
	idx := int(p/100*float64(len(l))+0.5) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(l) {
		idx = len(l) - 1
	}
	return l[idx]
}

// Estimate diz quanto tempo levaria traduzir totalWords palavras na velocidade
// medida. Falso quando não há medida (não inventa número).
func (r Result) Estimate(totalWords int64) (time.Duration, bool) {
	w := r.WordsPerSec()
	if !r.OK() || w <= 0 || totalWords <= 0 {
		return 0, false
	}
	return time.Duration(float64(totalWords) / w * float64(time.Second)), true
}

// Options controla um teste.
type Options struct {
	Samples []string         // frases a traduzir (a 1ª vai sozinha, como aquecimento)
	Now     func() time.Time // relógio (nil = time.Now); os testes trocam
	// Wait é chamado antes de cada pedido (limite de pedidos por minuto da
	// nuvem). O tempo gasto nele NÃO entra na medida do motor.
	Wait func(ctx context.Context) error
}

// Run roda as frases no motor e mede. Nunca repete um pedido que falhou: o
// teste mostra o que o motor faz, não esconde falhas com tentativas.
func Run(ctx context.Context, eng translate.Engine, o Options) Result {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	res := Result{Name: eng.Name(), Model: eng.Model()}
	if len(o.Samples) == 0 {
		res.Err = fmt.Errorf("sem frases para testar")
		return res
	}
	call := func(texts []string, base int) ([]translate.Result, time.Duration, error) {
		items := make([]translate.Item, len(texts))
		for i, t := range texts {
			items[i] = translate.Item{ID: fmt.Sprintf("bench%03d", base+i), Text: t}
		}
		if o.Wait != nil {
			if err := o.Wait(ctx); err != nil {
				return nil, 0, err
			}
		}
		start := now()
		out, err := eng.Translate(ctx, items)
		d := now().Sub(start)
		if err == nil && len(out) != len(items) {
			err = fmt.Errorf("o motor devolveu %d respostas para %d textos", len(out), len(items))
		}
		return out, d, err
	}

	// aquecimento: a primeira frase vai sozinha; só mede o tempo dela
	out, d, err := call(o.Samples[:1], 0)
	if err != nil {
		res.Err = err
		return res
	}
	res.Cold = d
	if out[0].Err != nil {
		res.Err = fmt.Errorf("a primeira frase falhou: %w", out[0].Err)
		return res
	}

	bs := eng.BatchSize()
	if bs < 1 {
		bs = 1
	}
	rest := o.Samples[1:]
	for i := 0; i < len(rest); i += bs {
		if ctx.Err() != nil {
			res.Err = ctx.Err()
			return res
		}
		end := i + bs
		if end > len(rest) {
			end = len(rest)
		}
		batch := rest[i:end]
		out, d, err := call(batch, 1+i)
		if err != nil {
			res.Err = err
			return res
		}
		res.Calls++
		res.Elapsed += d
		res.Latencies = append(res.Latencies, d)
		for k, r := range out {
			if r.Err != nil || r.Out == "" {
				res.Failed++
				continue
			}
			res.Texts++
			res.Words += translate.CountWords(batch[k])
		}
	}
	return res
}
