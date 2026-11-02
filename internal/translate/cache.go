package translate

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"mtrad/internal/schema"
)

const vCache = "cache/1"

type cacheRec struct {
	K   string `json:"k"`
	Out string `json:"out"`
}

// Cache guarda traduções já feitas. A chave junta o texto protegido, o motor,
// o modelo, a versão do prompt, a versão do glossário e o estilo das marcas,
// então mudar qualquer um deles invalida só o que precisa.
type Cache struct {
	mu sync.Mutex
	m  map[string]string
	a  *schema.Appender
}

func OpenCache(path string) (*Cache, error) {
	c := &Cache{m: map[string]string{}}
	err := schema.Each(path, vCache, func(r cacheRec) error { c.m[r.K] = r.Out; return nil })
	if err != nil && !isNotExist(err) {
		return nil, err
	}
	a, err := schema.OpenAppend(path, vCache)
	if err != nil {
		return nil, err
	}
	c.a = a
	return c, nil
}

// CacheKey calcula a chave de um texto. promptTag identifica a versão do
// prompt do motor; vazia (o prompt original) não entra no cálculo, para o cache
// feito antes de existir esse campo continuar valendo.
func CacheKey(engine, model, promptTag, glossVer, style, masked string) string {
	h := sha256.New()
	parts := []string{engine, model}
	if promptTag != "" {
		parts = append(parts, "prompt:"+promptTag)
	}
	parts = append(parts, glossVer, style, masked)
	for _, s := range parts {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func (c *Cache) Get(k string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	return v, ok
}

func (c *Cache) Put(k, out string) error {
	c.mu.Lock()
	c.m[k] = out
	c.mu.Unlock()
	return c.a.Write(cacheRec{k, out})
}

func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

func (c *Cache) Close() error { return c.a.Close() }
