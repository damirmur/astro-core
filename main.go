// Сервис astro-core (:8084) — чистый CGO-расчётник без авторизации.
//
// Сюда переезжают из монолита:
//
//	main.go (астро-хендлеры): /api/v1/natal|synastry|period|natal-period|returns|calendar,
//	  /api/daily, /api/translate;
//	tasks.go (часть daily_calendar): расчёт календаря + запись html/data/daily.json
//	  (ИИ-интерпретация — через llm-1c :8086, не прямой keys.json).
//
// Единственный сервис с CGO (swisseph, EPHE_PATH). PB/Supabase/keys.json — нет.
// Пока каркас: регистрирует /healthz, перенос хендлеров — фазой 2.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/damirmur/astro-core/internal/shared"
	"github.com/joho/godotenv"
)

// Port — локальный порт astro-core (nginx: точные /api/v1/natal… сюда).
const Port = ":8084"

func main() {
	if err := godotenv.Load(".env.astro"); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Println("astro: .env.astro/.env не найден, используются системные переменные")
		}
	}
	log.Printf("astro: эфемериды %s", shared.GetEphePath())

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", shared.HandleHealth("astro-core"))

	// TODO(фаза 2): перенести из main.go:1305-1312 астро-хендлеры
	// (handleNatal/handleSynastry/handlePeriod/handleNatalPeriod/handleReturns/
	// handleCalendar/handleDailyFile/handleTranslate) + кэш shared.NewMemoryCache().

	addr := Port
	if v := os.Getenv("ASTRO_PORT"); v != "" {
		addr = v
	}
	log.Printf("astro-core слушает %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("astro: ошибка запуска: %v", err)
	}
}
