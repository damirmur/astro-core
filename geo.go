package main

// geo.go — GET /api/v1/geo?city= : часовой пояс и координаты города.
// Нужен site-cloud для digest-рассылок (местное время подписки).
// Геокодер — interactive.GetGeoData (CGO), поэтому живёт здесь, а не в site.

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/damirmur/swisseph_build/pkg/interactive"
)

// GET /api/v1/geo?city=<город> → {ok, city, timezone, lat, lon}.
// Без города — 400; город не распознан — 404.
func handleGeo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	city := strings.TrimSpace(r.URL.Query().Get("city"))
	if city == "" {
		http.Error(w, "Нет города (query city)", http.StatusBadRequest)
		return
	}
	gd, err := interactive.GetGeoData(&interactive.UserInput{City: city})
	if err != nil || gd == nil {
		log.Printf("astro: geo %q не распознан: %v", city, err)
		http.Error(w, "Город не распознан", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok": true, "city": gd.City, "timezone": gd.Timezone,
		"lat": gd.Latitude, "lon": gd.Longitude,
	})
}
