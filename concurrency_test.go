package main

// Проверка потокобезопасности Swiss Ephemeris под конкуренцией.
//
// На каждый запрос создаётся свой Calculator: LockOSThread + swe_set_ephe_path,
// Close → swe_close. Если глобальное состояние библиотеки не переживает
// параллельные вызовы, одинаковые карты начнут отличаться. Тест считает одну
// и ту же карту из нескольких горутин и сравнивает JSON побайтово.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/damirmur/swisseph_build/pkg/astro"
)

func TestNatalConcurrentConsistent(t *testing.T) {
	if GetEphePath() == "" {
		t.Skip("эфемериды не найдены")
	}
	utc := time.Date(1990, 5, 15, 11, 30, 0, 0, time.UTC)
	const workers = 8
	const iters = 10

	var mu sync.Mutex
	var want string
	var wg sync.WaitGroup
	problems := make(chan string, workers*iters)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				calc := astro.NewCalculator(GetEphePath())
				res, err := calc.ComputeNatal(context.Background(), utc, 55.7558, 37.6173, "P")
				calc.Close()
				if err != nil {
					problems <- "ошибка расчёта: " + err.Error()
					continue
				}
				raw, err := json.Marshal(res)
				if err != nil {
					problems <- "ошибка json: " + err.Error()
					continue
				}
				mu.Lock()
				if want == "" {
					want = string(raw)
				} else if want != string(raw) {
					problems <- "ответы различаются при параллельном расчёте"
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(problems)
	for msg := range problems {
		t.Error(msg)
	}
}
