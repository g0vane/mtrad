package translate

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// statusError é um erro de HTTP do motor. RetryAfter vem do cabeçalho
// Retry-After (zero se o servidor não mandou).
type statusError struct {
	Code       int
	Msg        string
	RetryAfter time.Duration
}

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Code, e.Msg) }

// errCount: o motor devolveu um número de resultados diferente do pedido.
var errCount = errors.New("o motor devolveu quantidade diferente de resultados")

func httpError(resp *http.Response, body []byte) error {
	return &statusError{Code: resp.StatusCode, Msg: snippet(body), RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
}

// parseRetryAfter lê o cabeçalho Retry-After: segundos inteiros ou uma data
// HTTP. Valor ausente, inválido ou no passado dá zero.
func parseRetryAfter(h string, now time.Time) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if n, err := strconv.Atoi(h); err == nil {
		if n < 0 {
			return 0
		}
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// retryAfterOf devolve o tempo que o servidor pediu para esperar, se pediu.
func retryAfterOf(err error) time.Duration {
	var se *statusError
	if errors.As(err, &se) {
		return se.RetryAfter
	}
	return 0
}

// splittable diz se vale dividir o lote ao meio e tentar de novo: o servidor
// respondeu, mas recusou o pedido (4xx que não é 429, ou 5xx) ou devolveu a
// conta errada. Erro de rede (servidor fora do ar) não: dividir só multiplicaria
// as tentativas inúteis.
func splittable(err error) bool {
	if errors.Is(err, errCount) {
		return true
	}
	var se *statusError
	if errors.As(err, &se) {
		return se.Code != http.StatusTooManyRequests
	}
	return false
}
