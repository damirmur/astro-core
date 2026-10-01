// Сервис astro-core (:8084) — чистый CGO-расчётник без авторизации.
// Код расчётов — astro.go (вырезка main.go:1-1277 монолита 1:1), daily — daily_file.go.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
)

// Port — локальный порт astro-core.
const Port = ":8084"

func main() {
	if err := godotenv.Load(".env.astro"); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Println("astro: .env.astro/.env не найден, используются системные переменные")
		}
	}
	log.Printf("astro: эфемериды %s", GetEphePath())

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/api/v1/natal", corsMiddleware(handleNatal))
	mux.HandleFunc("/api/v1/synastry", corsMiddleware(handleSynastry))
	mux.HandleFunc("/api/v1/period", corsMiddleware(handlePeriod))
	mux.HandleFunc("/api/v1/natal-period", corsMiddleware(handleNatalPeriod))
	mux.HandleFunc("/api/v1/returns", corsMiddleware(handleReturns))
	mux.HandleFunc("/api/v1/calendar", corsMiddleware(handleCalendar))
	mux.HandleFunc("/api/daily", corsMiddleware(handleDailyFile))
	mux.HandleFunc("/api/translate", corsMiddleware(handleTranslate))

	addr := Port
	if v := os.Getenv("ASTRO_PORT"); v != "" {
		addr = v
	}
	log.Printf("astro-core слушает %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("astro: ошибка запуска: %v", err)
	}
}
