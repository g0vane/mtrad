package translate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

// ErrDailyLimit: a cota do dia acabou; o motor para e a execução pode
// continuar amanhã (ou com outro motor).
var ErrDailyLimit = errors.New("limite diário de pedidos atingido")

// Limiter espaça os pedidos (rpm) e conta os do dia (rpd). O contador do dia
// fica num arquivo, para não estourar a cota ao rodar de novo.
type Limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
	rpd      int
	day      string
	used     int
	state    string
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error
}

func NewLimiter(rpm, rpd int, statePath string) *Limiter {
	l := &Limiter{rpd: rpd, state: statePath, now: time.Now, sleep: sleepCtx}
	if rpm > 0 {
		l.interval = time.Minute / time.Duration(rpm)
	}
	l.load()
	return l
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type limState struct {
	Day  string `json:"day"`
	Used int    `json:"used"`
}

func (l *Limiter) load() {
	if l.state == "" {
		return
	}
	b, err := os.ReadFile(l.state)
	if err != nil {
		return
	}
	var s limState
	if json.Unmarshal(b, &s) == nil {
		l.day, l.used = s.Day, s.Used
	}
}

func (l *Limiter) save() {
	if l.state == "" {
		return
	}
	b, _ := json.Marshal(limState{l.day, l.used})
	_ = os.WriteFile(l.state, b, 0o644)
}

// Wait bloqueia até poder fazer um pedido.
func (l *Limiter) Wait(ctx context.Context) error {
	l.mu.Lock()
	now := l.now()
	today := now.Format("2006-01-02")
	if l.day != today {
		l.day, l.used = today, 0
	}
	if l.rpd > 0 && l.used >= l.rpd {
		l.mu.Unlock()
		return ErrDailyLimit
	}
	l.used++
	l.save()
	var wait time.Duration
	if l.interval > 0 {
		if l.next.After(now) {
			wait = l.next.Sub(now)
			l.next = l.next.Add(l.interval)
		} else {
			l.next = now.Add(l.interval)
		}
	}
	l.mu.Unlock()
	return l.sleep(ctx, wait)
}
