package fmtx

import (
	"testing"
	"time"
)

func TestDuration(t *testing.T) {
	for in, want := range map[time.Duration]string{
		-5 * time.Second:                        "0 s",
		0:                                       "0 s",
		499 * time.Millisecond:                  "0 s",
		500 * time.Millisecond:                  "1 s",
		45 * time.Second:                        "45 s",
		59*time.Second + 600*time.Millisecond:   "1 min 00 s",
		12*time.Minute + 4*time.Second:          "12 min 04 s",
		59*time.Minute + 59*time.Second:         "59 min 59 s",
		time.Hour:                               "1 h 00 min",
		time.Hour + 5*time.Minute + time.Second: "1 h 05 min",
		26*time.Hour + 30*time.Minute:           "26 h 30 min",
	} {
		if got := Duration(in); got != want {
			t.Errorf("Duration(%v) = %q, queria %q", in, got, want)
		}
	}
}

func TestDecimalEMs(t *testing.T) {
	if got := Decimal1(7.94); got != "7,9" {
		t.Fatalf("got %q", got)
	}
	if got := Ms(850 * time.Millisecond); got != "850 ms" {
		t.Fatalf("got %q", got)
	}
	if got := Ms(1450 * time.Millisecond); got != "1,4 s" && got != "1,5 s" {
		t.Fatalf("got %q", got)
	}
}
