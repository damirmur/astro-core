package main

// heavy.go — защита одноядерного расчётника от очереди тяжёлых запросов.
//
// calendar, period, natal-period и returns занимают сотни миллисекунд CPU.
// На одном ядре одновременных расчётов всё равно не будет больше, чем ядер:
// очередь только растёт, и p99 уходит в секунды (нагрузочный прогон:
// 24 запроса calendar → p50 5,2 с). Лишние запросы сразу получают 503
// с Retry-After, чтобы клиент повторил позже, а не ждал в очереди.

import (
	"net/http"
	"os"
	"runtime"
	"strconv"
)

type heavyGate struct {
	slots chan struct{}
}

func newHeavyGate(n int) *heavyGate {
	if n < 1 {
		n = 1
	}
	return &heavyGate{slots: make(chan struct{}, n)}
}

// tryAcquire берёт слот без ожидания и без ответа клиенту: для фоновых задач
// (например, предрасчёт календаря), которые не должны выбивать 503 у запросов.
func (g *heavyGate) tryAcquire() (release func(), ok bool) {
	select {
	case g.slots <- struct{}{}:
		return func() { <-g.slots }, true
	default:
		return nil, false
	}
}

// acquire берёт слот без ожидания. false — слот занят, ответ уже отправлен.
func (g *heavyGate) acquire(w http.ResponseWriter) (release func(), ok bool) {
	select {
	case g.slots <- struct{}{}:
		return func() { <-g.slots }, true
	default:
		w.Header().Set("Retry-After", "1")
		writeJSONError(w, http.StatusServiceUnavailable,
			"Сервер занят расчётом, повторите через секунду")
		return nil, false
	}
}

// heavyConcurrency — сколько тяжёлых расчётов пускаем одновременно.
// По умолчанию 2×GOMAXPROCS (на одном ядре — 2): два запроса сглаживают
// всплеск, третий сразу получает 503. Переопределяется ASTRO_HEAVY_CONCURRENCY.
func heavyConcurrency() int {
	if v := os.Getenv("ASTRO_HEAVY_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	n := runtime.GOMAXPROCS(0) * 2
	if n < 2 {
		n = 2
	}
	return n
}

var heavy = newHeavyGate(heavyConcurrency())
