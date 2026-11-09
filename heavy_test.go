package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHeavyGateBusy(t *testing.T) {
	g := newHeavyGate(1)

	release, ok := g.acquire(httptest.NewRecorder())
	if !ok {
		t.Fatal("первый слот должен выдаваться")
	}

	w := httptest.NewRecorder()
	if _, ok := g.acquire(w); ok {
		t.Fatal("второй слот не должен выдаваться при ёмкости 1")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("код %d, ожидался 503", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After=%q", got)
	}

	release()
	if _, ok := g.acquire(httptest.NewRecorder()); !ok {
		t.Fatal("слот не освободился после release")
	}
}

func TestHeavyGateZeroCapacity(t *testing.T) {
	g := newHeavyGate(0)
	if cap(g.slots) != 1 {
		t.Fatalf("ёмкость %d, ожидалась 1", cap(g.slots))
	}
}
