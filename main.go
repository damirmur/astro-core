// Сервис astro-core (:8084) — чистый CGO-расчётник без авторизации.
// Код расчётов — astro.go (вырезка main.go:1-1277 монолита 1:1), daily — daily_file.go.
package main

import (
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"
	"time"

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
	mux.HandleFunc("/api/v1/geo", corsMiddleware(handleGeo))

	// Фоновый daily_calendar из tasks.json (пишет html/data/daily.json).
	go startTaskRunner()

	addr := Port
	if v := os.Getenv("ASTRO_PORT"); v != "" {
		addr = v
	}
	// Таймауты обязательны: без них медленный клиент держит соединение и
	// горутину бесконечно, а под нагрузкой сервер копит запросы вместо отсечки.
	// WriteTimeout с запасом: самый тяжёлый расчёт (calendar/period) — секунды.
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       75 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	// pprof — только по явному env-флагу и только loopback:
	// без него горутины и блокировки под нагрузкой не видны.
	if pprofAddr := strings.TrimSpace(os.Getenv("PPROF_ADDR")); pprofAddr != "" {
		go func() {
			log.Printf("pprof слушает %s", pprofAddr)
			if err := http.ListenAndServe(pprofAddr, nil); err != nil {
				log.Printf("pprof: %v", err)
			}
		}()
	}
	log.Printf("astro-core слушает %s", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("astro: ошибка запуска: %v", err)
	}
}
