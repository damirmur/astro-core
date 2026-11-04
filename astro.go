package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/damirmur/swisseph_build/pkg/astro"
	"github.com/damirmur/swisseph_build/pkg/interactive"
)

// Длительность кэша астро-ответов (как в монолите).
const CacheDuration = 24 * time.Hour

// Структура для кэширования ответов в памяти
type CacheEntry struct {
	Response   []byte
	Expiration time.Time
}

type MemoryCache struct {
	mu    sync.RWMutex
	items map[string]CacheEntry
}

func NewMemoryCache() *MemoryCache {
	cache := &MemoryCache{
		items: make(map[string]CacheEntry),
	}
	go cache.startCleanupTimer()
	return cache
}

func (c *MemoryCache) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	item, found := c.items[key]
	if !found {
		return nil, false
	}
	if time.Now().After(item.Expiration) {
		return nil, false
	}
	return item.Response, true
}

func (c *MemoryCache) Set(key string, response []byte, duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = CacheEntry{
		Response:   response,
		Expiration: time.Now().Add(duration),
	}
}

func (c *MemoryCache) startCleanupTimer() {
	ticker := time.NewTicker(1 * time.Hour)
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.items {
			if now.After(v.Expiration) {
				delete(c.items, k)
			}
		}
		c.mu.Unlock()
	}
}

// Глобальный кэш
var cache = NewMemoryCache()

// Вспомогательная функция для генерации ключа кэша
func generateCacheKey(prefix string, params url.Values) string {
	hash := sha256.Sum256([]byte(prefix + "?" + params.Encode()))
	return fmt.Sprintf("%x", hash)
}

// Middleware для CORS
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}

// GetExecutableDir возвращает абсолютный путь к папке, где лежит исполняемый файл
func GetExecutableDir() string {
	exePath, err := os.Executable()
	if err != nil {
		log.Printf("Ошибка получения пути процесса: %v, используем текущую директорию", err)
		return "."
	}
	return filepath.Dir(exePath)
}

// GetEphePath возвращает путь к папке ephe с поддержкой различных сред выполнения
func GetEphePath() string {
	// 1. Проверяем переменную окружения EPHE_PATH
	if envPath := os.Getenv("EPHE_PATH"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath
		}
	}

	// 2. Проверяем путь относительно исполняемого файла (для продакшна)
	if exePath, err := os.Executable(); err == nil {
		path := filepath.Join(filepath.Dir(exePath), "ephe")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	// 3. Проверяем путь относительно текущей рабочей директории (для go run и локальной разработки)
	if _, err := os.Stat("ephe"); err == nil {
		return "ephe"
	}

	// Дефолтный фолбек
	return "ephe"
}

// Парсинг конфигурации фильтров из URL-запроса
// parseLocalTime разбирает момент времени из query-параметра. Для форматов со
// часовым поясом возвращает абсолютное время, приведённое к loc; для форматов без
// зоны трактует показания часов как локальные для loc.
func parseLocalTime(s string, loc *time.Location) (time.Time, error) {
	layouts := []string{
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04Z07:00",
		"2006-01-02T15:04",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			if l == "2006-01-02T15:04" || l == "2006-01-02" {
				return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc), nil
			}
			return t.In(loc), nil
		}
	}
	return time.Time{}, fmt.Errorf("неизвестный формат времени: %s", s)
}

func parseFilterConfig(q url.Values) astro.FilterConfig {
	cfg := astro.FilterConfig{}

	// 1. Фильтр по точности (орбису)
	if maxOrbStr := q.Get("max_orb"); maxOrbStr != "" {
		if val, err := strconv.ParseFloat(maxOrbStr, 64); err == nil {
			cfg.MaxOrb = &val
		}
	}

	// 2. Фильтр по планетам (Sun,Moon,Mars или 0,1,4)
	if planetsStr := q.Get("planets"); planetsStr != "" {
		cfg.Planets = make(map[string]bool)
		for _, p := range strings.Split(planetsStr, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				cfg.Planets[p] = true
			}
		}
	}

	// 3. Фильтр по типам аспектов (Conjunction,Square,etc.)
	if aspectsStr := q.Get("aspects"); aspectsStr != "" {
		cfg.AspectTypes = make(map[string]bool)
		for _, a := range strings.Split(aspectsStr, ",") {
			a = strings.TrimSpace(a)
			if a != "" {
				cfg.AspectTypes[a] = true
			}
		}
	}

	// 4. Фильтр по типам событий (lunD,noC,chS,exA)
	if eventTypesStr := q.Get("event_types"); eventTypesStr != "" {
		cfg.EventTypes = make(map[string]bool)
		for _, e := range strings.Split(eventTypesStr, ",") {
			e = strings.TrimSpace(e)
			if e != "" {
				cfg.EventTypes[e] = true
			}
		}
	}

	return cfg
}

// Вспомогательная структура для возврата метаданных
type ResponseMeta struct {
	City               string  `json:"city,omitempty"`
	Latitude           float64 `json:"lat"`
	Longitude          float64 `json:"lon"`
	Timezone           string  `json:"timezone,omitempty"`
	UTCOffset          float64 `json:"utc_offset"`
	CalculationTimeUTC string  `json:"calculation_time_utc"`
	City2              string  `json:"city2,omitempty"`
	Latitude2          float64 `json:"lat2,omitempty"`
	Longitude2         float64 `json:"lon2,omitempty"`
	Timezone2          string  `json:"timezone2,omitempty"`
	UTCOffset2         float64 `json:"utc_offset2,omitempty"`
	CalculationTimeB   string  `json:"calculation_time_b,omitempty"`
}

type APIResponse struct {
	Meta    ResponseMeta          `json:"meta"`
	Planets []astro.Position      `json:"planets,omitempty"`
	Houses  []astro.House         `json:"houses,omitempty"`
	Aspects []astro.Aspect        `json:"aspects,omitempty"`
	Events  []astro.CalendarEvent `json:"events,omitempty"`
	Slices  []astro.TimeSlice     `json:"slices,omitempty"`
}

// CalendarAPIResponse — самоописываемая огибающая /api/v1/calendar.
// meta содержит словари и описание структуры (источник типов), data — готовые события.
type CalendarAPIResponse struct {
	Schema  string               `json:"schema"`
	Meta    astro.Meta           `json:"meta"`
	Geo     ResponseMeta         `json:"geo,omitempty"`
	AscSign *int                 `json:"asc_sign,omitempty"`
	Data    []astro.CompactEvent `json:"data"`
}

// Функция для получения геоданных и автоматического перевода локального времени в UTC
func resolveGeoAndTime(city string, dateStr string, timeStr string) (*interactive.GeoData, time.Time, error) {
	// Если город не передан, то считаем по дефолту UTC+0 и координаты Москвы
	if city == "" {
		t, err := time.Parse("2006-01-02 15:04", dateStr+" "+timeStr)
		if err != nil {
			return nil, time.Time{}, err
		}
		return &interactive.GeoData{
			City:      "UTC Mode",
			Latitude:  55.7558,
			Longitude: 37.6173,
			Timezone:  "UTC",
			UTCOffset: 0,
		}, t, nil
	}

	// Получаем геоданные
	gd, err := interactive.GetGeoData(&interactive.UserInput{
		City: city,
		Date: dateStr,
		Time: timeStr,
	})
	if err != nil {
		return nil, time.Time{}, err
	}

	// Парсим локальное время
	locTime, err := time.Parse("2006-01-02 15:04", dateStr+" "+timeStr)
	if err != nil {
		return nil, time.Time{}, err
	}

	// Переводим в UTC
	utcTime := locTime.Add(time.Duration(-gd.UTCOffset) * time.Hour)
	return gd, utcTime, nil
}

// --- Ответ без домов (космограмма, ?houses=none) ---------------------------
//
// Космограмма — карта с неизвестным временем рождения: дома, Asc и MC
// не рассчитываются и не отдаются вовсе (никаких "cusps": null и номеров
// домов у транзитов). Планеты и аспекты не меняются: система домов на них
// не влияет.

type natalDataNoHouses struct {
	Planets []astro.PlanetPoint `json:"planets"`
	Aspects []astro.Aspect      `json:"aspects,omitempty"`
	Balance astro.Balance       `json:"balance"`
}

type synastryDataNoHouses struct {
	Chart1  natalDataNoHouses `json:"chart1"`
	Chart2  natalDataNoHouses `json:"chart2"`
	Aspects []astro.Aspect    `json:"aspects"`
}

type transitPointNoHouses struct {
	Longitude float64
	Speed     float64
}

// MarshalJSON — компактная форма [longitude, speed] без номера дома.
func (p transitPointNoHouses) MarshalJSON() ([]byte, error) {
	return json.Marshal([]float64{p.Longitude, p.Speed})
}

type transitSeriesNoHouses struct {
	Date    []string                          `json:"date"`
	Planets map[string][]transitPointNoHouses `json:"planets"`
}

type natalPeriodDataNoHouses struct {
	Natal    natalDataNoHouses          `json:"natal"`
	Transits transitSeriesNoHouses      `json:"transits"`
	Aspects  []astro.TransitAspectEvent `json:"aspects"`
}

// parseHousesMode — режим ответа по домам: пусто/full — обычный, none —
// космограмма. Неизвестное значение — ошибка (а не тихая подмена).
func parseHousesMode(q url.Values) (bool, error) {
	switch v := strings.TrimSpace(q.Get("houses")); v {
	case "", "full":
		return false, nil
	case "none":
		return true, nil
	default:
		return false, fmt.Errorf("parameter 'houses' accepts only 'none' (got %q)", v)
	}
}

// natalPayload — data натала: обычная карта либо без домов.
func natalPayload(res *astro.AstroResult, housesNone bool) any {
	d := astro.ToNatalData(res)
	if housesNone {
		return natalDataNoHouses{Planets: d.Planets, Aspects: d.Aspects, Balance: d.Balance}
	}
	return d
}

// synastryPayload — data синастрии: обычные карты либо без домов.
func synastryPayload(res *astro.AstroResult, housesNone bool) any {
	d := astro.ToSynastryData(res)
	if !housesNone {
		return d
	}
	return synastryDataNoHouses{
		Chart1:  natalDataNoHouses{Planets: d.Chart1.Planets, Aspects: d.Chart1.Aspects, Balance: d.Chart1.Balance},
		Chart2:  natalDataNoHouses{Planets: d.Chart2.Planets, Aspects: d.Chart2.Aspects, Balance: d.Chart2.Balance},
		Aspects: d.Aspects,
	}
}

// natalPeriodWithoutHouses — тот же ответ natal-period, но без куспидов
// натала и без номера дома в рядах транзитов.
func natalPeriodWithoutHouses(res *astro.NatalPeriodData) natalPeriodDataNoHouses {
	out := natalPeriodDataNoHouses{
		Natal:   natalDataNoHouses{Planets: res.Natal.Planets, Aspects: res.Natal.Aspects, Balance: res.Natal.Balance},
		Aspects: res.Aspects,
	}
	out.Transits.Date = res.Transits.Date
	out.Transits.Planets = make(map[string][]transitPointNoHouses, len(res.Transits.Planets))
	for id, pts := range res.Transits.Planets {
		list := make([]transitPointNoHouses, 0, len(pts))
		for _, p := range pts {
			list = append(list, transitPointNoHouses{Longitude: p.Longitude, Speed: p.Speed})
		}
		out.Transits.Planets[id] = list
	}
	return out
}

// 1. Обработчик /api/v1/natal
func handleNatal(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	city := q.Get("city")
	date := q.Get("date")
	timeVal := q.Get("time")
	hsys := q.Get("hsys")

	// Фолбек на старый формат параметров, если новые не переданы
	if date == "" {
		date = q.Get("date") // Ожидает YYYY-MM-DD HH:MM
		if strings.Contains(date, " ") {
			parts := strings.Split(date, " ")
			date = parts[0]
			timeVal = parts[1]
		}
	}

	if date == "" {
		writeJSONError(w, http.StatusBadRequest, "Parameter 'date' (YYYY-MM-DD) is required")
		return
	}
	if timeVal == "" {
		timeVal = "12:00"
	}
	if hsys == "" {
		hsys = "P"
	}
	housesNone, hErr := parseHousesMode(q)
	if hErr != nil {
		writeJSONError(w, http.StatusBadRequest, hErr.Error())
		return
	}

	// Кэширование
	cacheKey := generateCacheKey("natal", q)
	if cachedData, found := cache.Get(cacheKey); found {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		w.WriteHeader(http.StatusOK)
		w.Write(cachedData)
		return
	}

	// Поиск геокоординат и расчет UTC времени
	latVal := q.Get("lat")
	lonVal := q.Get("lon")

	var gd *interactive.GeoData
	var utcTime time.Time
	var err error

	if latVal != "" && lonVal != "" {
		// Старый формат: переданы точные координаты напрямую
		latF, _ := strconv.ParseFloat(latVal, 64)
		lonF, _ := strconv.ParseFloat(lonVal, 64)
		utcTime, err = time.Parse("2006-01-02 15:04", date+" "+timeVal)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid date/time format. Expected YYYY-MM-DD HH:MM")
			return
		}
		gd = &interactive.GeoData{
			City:      "Coordinates Mode",
			Latitude:  latF,
			Longitude: lonF,
			Timezone:  "UTC",
			UTCOffset: 0,
		}
	} else {
		// Новый формат: по названию города
		gd, utcTime, err = resolveGeoAndTime(city, date, timeVal)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to resolve geo/time: %v", err))
			return
		}
	}

	// Расчет натальной карты
	ephePath := GetEphePath()
	calc := astro.NewCalculator(ephePath)
	defer calc.Close()

	res, err := calc.ComputeNatal(r.Context(), utcTime, gd.Latitude, gd.Longitude, hsys)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Calculation error: %v", err))
		return
	}

	// Применяем фильтрацию
	filter := parseFilterConfig(q)
	res.ApplyFilter(filter)

	// Формируем самоописываемый ответ
	geo := astro.GeoInfo{
		City:      gd.City,
		Latitude:  gd.Latitude,
		Longitude: gd.Longitude,
		Timezone:  gd.Timezone,
		UTCOffset: gd.UTCOffset,
	}
	envelope := astro.NewEnvelope(astro.NatalSchema, astro.NewNatalMeta(), geo, natalPayload(res, housesNone))

	jsonBytes, _ := json.Marshal(envelope)
	cache.Set(cacheKey, jsonBytes, CacheDuration)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	w.WriteHeader(http.StatusOK)
	w.Write(jsonBytes)
}

// 2. Обработчик /api/v1/synastry
func handleSynastry(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	city1 := q.Get("city1")
	date1 := q.Get("date1")
	time1 := q.Get("time1")

	city2 := q.Get("city2")
	date2 := q.Get("date2")
	time2 := q.Get("time2")

	hsys := q.Get("hsys")

	// Фолбек на старые параметры
	if date1 == "" {
		date1 = q.Get("date1")
		if strings.Contains(date1, " ") {
			parts := strings.Split(date1, " ")
			date1 = parts[0]
			time1 = parts[1]
		}
	}
	if date2 == "" {
		date2 = q.Get("date2")
		if strings.Contains(date2, " ") {
			parts := strings.Split(date2, " ")
			date2 = parts[0]
			time2 = parts[1]
		}
	}

	if date1 == "" || date2 == "" {
		writeJSONError(w, http.StatusBadRequest, "Parameters 'date1' and 'date2' are required")
		return
	}
	if time1 == "" {
		time1 = "12:00"
	}
	if time2 == "" {
		time2 = "12:00"
	}
	if hsys == "" {
		hsys = "P"
	}
	housesNone, hErr := parseHousesMode(q)
	if hErr != nil {
		writeJSONError(w, http.StatusBadRequest, hErr.Error())
		return
	}

	cacheKey := generateCacheKey("synastry", q)
	if cachedData, found := cache.Get(cacheKey); found {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		w.WriteHeader(http.StatusOK)
		w.Write(cachedData)
		return
	}

	latVal := q.Get("lat")
	lonVal := q.Get("lon")

	var gd1, gd2 *interactive.GeoData
	var utcTime1, utcTime2 time.Time
	var err error

	if latVal != "" && lonVal != "" {
		latF, _ := strconv.ParseFloat(latVal, 64)
		lonF, _ := strconv.ParseFloat(lonVal, 64)
		utcTime1, err = time.Parse("2006-01-02 15:04", date1+" "+time1)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Неверная дата/время первой карты (ожидается YYYY-MM-DD HH:MM): "+date1+" "+time1)
			return
		}
		utcTime2, err = time.Parse("2006-01-02 15:04", date2+" "+time2)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Неверная дата/время второй карты (ожидается YYYY-MM-DD HH:MM): "+date2+" "+time2)
			return
		}
		gd1 = &interactive.GeoData{
			City:      "Coordinates Mode",
			Latitude:  latF,
			Longitude: lonF,
			Timezone:  "UTC",
			UTCOffset: 0,
		}
		gd2 = gd1
	} else {
		gd1, utcTime1, err = resolveGeoAndTime(city1, date1, time1)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to resolve city1: %v", err))
			return
		}
		gd2, utcTime2, err = resolveGeoAndTime(city2, date2, time2)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to resolve city2: %v", err))
			return
		}
	}

	ephePath := GetEphePath()
	calc := astro.NewCalculator(ephePath)
	defer calc.Close()

	res, err := calc.ComputeSynastry(r.Context(), utcTime1, utcTime2, gd1.Latitude, gd1.Longitude, hsys)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Calculation error: %v", err))
		return
	}

	filter := parseFilterConfig(q)
	res.ApplyFilter(filter)

	// Формируем самоописываемый ответ
	geo := astro.GeoInfoPair{
		Chart1: astro.GeoInfo{
			City:      gd1.City,
			Latitude:  gd1.Latitude,
			Longitude: gd1.Longitude,
			Timezone:  gd1.Timezone,
			UTCOffset: gd1.UTCOffset,
		},
		Chart2: astro.GeoInfo{
			City:      gd2.City,
			Latitude:  gd2.Latitude,
			Longitude: gd2.Longitude,
			Timezone:  gd2.Timezone,
			UTCOffset: gd2.UTCOffset,
		},
	}
	envelope := astro.NewEnvelope(astro.SynastrySchema, astro.NewSynastryMeta(), geo, synastryPayload(res, housesNone))

	jsonBytes, _ := json.Marshal(envelope)
	cache.Set(cacheKey, jsonBytes, CacheDuration)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	w.WriteHeader(http.StatusOK)
	w.Write(jsonBytes)
}

// 3. Обработчик /api/v1/period
func handlePeriod(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	start := q.Get("start")
	end := q.Get("end")
	step := q.Get("step")
	city := q.Get("city")

	if start == "" || end == "" {
		writeJSONError(w, http.StatusBadRequest, "Parameters 'start' and 'end' (YYYY-MM-DD HH:MM) are required")
		return
	}

	cacheKey := generateCacheKey("period", q)
	if cachedData, found := cache.Get(cacheKey); found {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		w.WriteHeader(http.StatusOK)
		w.Write(cachedData)
		return
	}

	// Обработка локального времени, если указан город
	var tStart, tEnd time.Time
	var gd *interactive.GeoData
	var err error

	if city != "" {
		var partsS []string
		if strings.Contains(start, " ") {
			partsS = strings.Split(start, " ")
		} else {
			partsS = []string{start, "00:00"}
		}
		gd, tStart, err = resolveGeoAndTime(city, partsS[0], partsS[1])
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to resolve city start time: %v", err))
			return
		}

		var partsE []string
		if strings.Contains(end, " ") {
			partsE = strings.Split(end, " ")
		} else {
			partsE = []string{end, "23:59"}
		}
		_, tEnd, err = resolveGeoAndTime(city, partsE[0], partsE[1])
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to resolve city end time: %v", err))
			return
		}
	} else {
		tStart, err = time.Parse("2006-01-02 15:04", start)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Неверный параметр 'start' (ожидается YYYY-MM-DD HH:MM): %q", start))
			return
		}
		tEnd, err = time.Parse("2006-01-02 15:04", end)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Неверный параметр 'end' (ожидается YYYY-MM-DD HH:MM): %q", end))
			return
		}
		if !tStart.Before(tEnd) {
			writeJSONError(w, http.StatusBadRequest, "'start' должен быть раньше 'end'")
			return
		}
		gd = &interactive.GeoData{
			City:      "UTC Mode",
			Latitude:  55.7558,
			Longitude: 37.6173,
			Timezone:  "UTC",
			UTCOffset: 0,
		}
	}

	stepHours := 24
	if step != "" {
		if val, err := strconv.Atoi(step); err == nil && val > 0 {
			stepHours = val
		}
	}

	ephePath := GetEphePath()
	calc := astro.NewCalculator(ephePath)
	defer calc.Close()

	res, err := calc.ComputePeriod(r.Context(), tStart, tEnd, stepHours)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Calculation error: %v", err))
		return
	}

	// Поддержка вывода меток времени в часовом поясе пользователя
	if city != "" && gd != nil {
		loc, err := time.LoadLocation(gd.Timezone)
		if err == nil {
			for idx := range res.Slices {
				res.Slices[idx].Timestamp = res.Slices[idx].Timestamp.In(loc)
			}
		}
	}

	filter := parseFilterConfig(q)
	res.ApplyFilter(filter)

	// Формируем самоописываемый ответ
	geo := astro.GeoInfo{
		City:      gd.City,
		Latitude:  gd.Latitude,
		Longitude: gd.Longitude,
		Timezone:  gd.Timezone,
		UTCOffset: gd.UTCOffset,
	}
	envelope := astro.NewEnvelope(astro.PeriodSchema, astro.NewPeriodMeta(), geo, astro.ToPeriodData(res))

	jsonBytes, _ := json.Marshal(envelope)
	cache.Set(cacheKey, jsonBytes, CacheDuration)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	w.WriteHeader(http.StatusOK)
	w.Write(jsonBytes)
}

// 3.5 Обработчик /api/v1/natal-period — натальная карта + транзиты за период
func handleNatalPeriod(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// --- Натальная карта ---
	date := q.Get("date")
	timeVal := q.Get("time")
	hsys := q.Get("hsys")
	if timeVal == "" {
		timeVal = "12:00"
	}
	if hsys == "" {
		hsys = "P"
	}
	housesNone, hErr := parseHousesMode(q)
	if hErr != nil {
		writeJSONError(w, http.StatusBadRequest, hErr.Error())
		return
	}
	if date == "" {
		date = q.Get("date")
		if strings.Contains(date, " ") {
			parts := strings.Split(date, " ")
			date = parts[0]
			timeVal = parts[1]
		}
	}
	if date == "" {
		writeJSONError(w, http.StatusBadRequest, "Parameter 'date' (YYYY-MM-DD) is required")
		return
	}

	cacheKey := generateCacheKey("natal-period", q)
	if cachedData, found := cache.Get(cacheKey); found {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		w.WriteHeader(http.StatusOK)
		w.Write(cachedData)
		return
	}

	latVal := q.Get("lat")
	lonVal := q.Get("lon")
	var gd *interactive.GeoData
	var utcTime time.Time
	var err error

	if latVal != "" && lonVal != "" {
		latF, _ := strconv.ParseFloat(latVal, 64)
		lonF, _ := strconv.ParseFloat(lonVal, 64)
		utcTime, err = time.Parse("2006-01-02 15:04", date+" "+timeVal)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid date/time format. Expected YYYY-MM-DD HH:MM")
			return
		}
		gd = &interactive.GeoData{
			City:      "Coordinates Mode",
			Latitude:  latF,
			Longitude: lonF,
			Timezone:  "UTC",
			UTCOffset: 0,
		}
	} else {
		gd, utcTime, err = resolveGeoAndTime(q.Get("city"), date, timeVal)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to resolve geo/time: %v", err))
			return
		}
	}

	// --- Период ---
	city := q.Get("city")
	startStr := q.Get("start")
	endStr := q.Get("end")
	step := 24
	if s := q.Get("step"); s != "" {
		if sv, e := strconv.Atoi(s); e == nil && sv > 0 {
			step = sv
		}
	}

	loc := time.UTC
	if gd.Timezone != "" {
		if l, e := time.LoadLocation(gd.Timezone); e == nil {
			loc = l
		}
	}

	startUTC := time.Now().UTC()
	endUTC := startUTC.AddDate(0, 1, 0)
	if startStr != "" {
		ts, te, e := parsePeriodRange(startStr, endStr, city)
		if e != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to parse period: %v", e))
			return
		}
		startUTC = ts
		endUTC = te
		if endStr == "" {
			endUTC = startUTC.AddDate(0, 1, 0)
		}
	}

	// --- Транзитные планеты ---
	transitIDs := astro.DefaultTransitPlanets
	if p := q.Get("planets"); p != "" {
		if parsed := parseIDList(p); len(parsed) > 0 {
			transitIDs = parsed
		}
	}

	// --- Орб и углы аспектов ---
	maxOrb := 1.0
	if mo := q.Get("max_orb"); mo != "" {
		if v, e := strconv.ParseFloat(mo, 64); e == nil {
			maxOrb = v
		}
	}
	angles := astro.MajorAspectAngles
	if a := q.Get("aspects"); a != "" {
		if parsed := parseAngleList(a); len(parsed) > 0 {
			angles = parsed
		}
	}

	// --- Расчёт ---
	ephePath := GetEphePath()
	calc := astro.NewCalculator(ephePath)
	defer calc.Close()

	res, err := calc.ComputeNatalPeriod(r.Context(), utcTime, gd.Latitude, gd.Longitude, hsys,
		startUTC, endUTC, step, transitIDs, maxOrb, angles, loc)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Calculation error: %v", err))
		return
	}

	geo := astro.NatalPeriodGeo{
		Natal: astro.GeoInfo{
			City:      gd.City,
			Latitude:  gd.Latitude,
			Longitude: gd.Longitude,
			Timezone:  gd.Timezone,
			UTCOffset: gd.UTCOffset,
		},
		Period: astro.GeoInfo{
			City:      gd.City,
			Latitude:  gd.Latitude,
			Longitude: gd.Longitude,
			Timezone:  gd.Timezone,
			UTCOffset: gd.UTCOffset,
		},
	}

	payload := any(res)
	if housesNone {
		payload = natalPeriodWithoutHouses(res)
	}
	envelope := astro.NewEnvelope(astro.NatalPeriodSchema, astro.NewNatalPeriodMeta(), geo, payload)

	jsonBytes, _ := json.Marshal(envelope)
	cache.Set(cacheKey, jsonBytes, CacheDuration)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	w.WriteHeader(http.StatusOK)
	w.Write(jsonBytes)
}

// 3a. Обработчик /api/v1/returns — предыдущий/последующий соляры и лунары
// относительно опорной транзитной даты (моменты возврата Солнца/Луны
// на натальные долготы, ISO-8601 с секундами в зоне ref).
func handleReturns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	city := q.Get("city")
	date := q.Get("date")
	timeVal := q.Get("time")
	hsys := q.Get("hsys")
	if timeVal == "" {
		timeVal = "12:00"
	}
	if hsys == "" {
		hsys = "P"
	}
	if date == "" {
		writeJSONError(w, http.StatusBadRequest, "Parameter 'date' (YYYY-MM-DD) is required")
		return
	}
	refDate := q.Get("ref_date")
	if refDate == "" {
		writeJSONError(w, http.StatusBadRequest, "Parameter 'ref_date' (YYYY-MM-DD) is required")
		return
	}
	refTime := q.Get("ref_time")
	if refTime == "" {
		refTime = "12:00"
	}

	cacheKey := generateCacheKey("returns", q)
	if cachedData, found := cache.Get(cacheKey); found {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		w.WriteHeader(http.StatusOK)
		w.Write(cachedData)
		return
	}

	// --- Натал ---
	latVal := q.Get("lat")
	lonVal := q.Get("lon")
	var gd *interactive.GeoData
	var natalUTC time.Time
	var err error
	if latVal != "" && lonVal != "" {
		latF, _ := strconv.ParseFloat(latVal, 64)
		lonF, _ := strconv.ParseFloat(lonVal, 64)
		natalUTC, err = time.Parse("2006-01-02 15:04", date+" "+timeVal)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid date/time format. Expected YYYY-MM-DD HH:MM")
			return
		}
		gd = &interactive.GeoData{
			City:      "Coordinates Mode",
			Latitude:  latF,
			Longitude: lonF,
			Timezone:  "UTC",
			UTCOffset: 0,
		}
	} else {
		gd, natalUTC, err = resolveGeoAndTime(city, date, timeVal)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to resolve geo/time: %v", err))
			return
		}
	}

	// --- Опорная транзитная дата (по умолчанию — в городе натала) ---
	refCity := q.Get("ref_city")
	if refCity == "" {
		refCity = city
	}
	refLatVal := q.Get("ref_lat")
	refLonVal := q.Get("ref_lon")
	var rgd *interactive.GeoData
	var refUTC time.Time
	if refLatVal != "" && refLonVal != "" {
		latF, _ := strconv.ParseFloat(refLatVal, 64)
		lonF, _ := strconv.ParseFloat(refLonVal, 64)
		refUTC, err = time.Parse("2006-01-02 15:04", refDate+" "+refTime)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid ref_date/ref_time format. Expected YYYY-MM-DD HH:MM")
			return
		}
		rgd = &interactive.GeoData{
			City:      "Coordinates Mode",
			Latitude:  latF,
			Longitude: lonF,
			Timezone:  "UTC",
			UTCOffset: 0,
		}
	} else {
		rgd, refUTC, err = resolveGeoAndTime(refCity, refDate, refTime)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to resolve ref geo/time: %v", err))
			return
		}
	}

	loc := time.UTC
	if rgd.Timezone != "" {
		if l, e := time.LoadLocation(rgd.Timezone); e == nil {
			loc = l
		}
	}

	ephePath := GetEphePath()
	calc := astro.NewCalculator(ephePath)
	defer calc.Close()

	res, err := calc.ComputeReturns(natalUTC, refUTC)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Calculation error: %v", err))
		return
	}

	geo := astro.ReturnsGeo{
		Natal: astro.GeoInfo{
			City:      gd.City,
			Latitude:  gd.Latitude,
			Longitude: gd.Longitude,
			Timezone:  gd.Timezone,
			UTCOffset: gd.UTCOffset,
		},
		Ref: astro.GeoInfo{
			City:      rgd.City,
			Latitude:  rgd.Latitude,
			Longitude: rgd.Longitude,
			Timezone:  rgd.Timezone,
			UTCOffset: rgd.UTCOffset,
		},
	}
	envelope := astro.NewEnvelope(astro.ReturnsSchema, astro.NewReturnsMeta(), geo, res.ToReturnsData(loc))

	jsonBytes, _ := json.Marshal(envelope)
	cache.Set(cacheKey, jsonBytes, CacheDuration)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	w.WriteHeader(http.StatusOK)
	w.Write(jsonBytes)
}

// parsePeriodRange разбирает начало/конец периода в UTC.
func parsePeriodRange(startStr, endStr, city string) (time.Time, time.Time, error) {
	var ts, te time.Time
	var err error
	if city != "" {
		var partsS []string
		if strings.Contains(startStr, " ") {
			partsS = strings.Split(startStr, " ")
		} else {
			partsS = []string{startStr, "00:00"}
		}
		_, ts, err = resolveGeoAndTime(city, partsS[0], partsS[1])
		if err != nil {
			return ts, te, err
		}
		var partsE []string
		if endStr != "" {
			if strings.Contains(endStr, " ") {
				partsE = strings.Split(endStr, " ")
			} else {
				partsE = []string{endStr, "23:59"}
			}
		} else {
			partsE = []string{startStr, "23:59"}
		}
		_, te, err = resolveGeoAndTime(city, partsE[0], partsE[1])
		if err != nil {
			return ts, te, err
		}
		return ts, te, nil
	}
	ts, err = time.Parse("2006-01-02 15:04", startStr)
	if err != nil {
		ts, err = time.Parse("2006-01-02", startStr)
	}
	if err != nil {
		return ts, te, err
	}
	ts = ts.UTC()
	if endStr == "" {
		te = ts
	} else {
		te, err = time.Parse("2006-01-02 15:04", endStr)
		if err != nil {
			te, err = time.Parse("2006-01-02", endStr)
		}
		if err != nil {
			return ts, te, err
		}
		te = te.UTC()
	}
	return ts, te, nil
}

func parseIDList(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if v, e := strconv.Atoi(part); e == nil {
			out = append(out, v)
		}
	}
	return out
}

func parseAngleList(s string) []float64 {
	var out []float64
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if v, e := strconv.ParseFloat(part, 64); e == nil {
			out = append(out, v)
		}
	}
	return out
}

// 4. Обработчик /api/v1/calendar
func handleCalendar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	yearStr := q.Get("year")
	monthStr := q.Get("month")
	city := q.Get("city")

	year := time.Now().Year()
	if yearStr != "" {
		y, err := strconv.Atoi(yearStr)
		if err != nil || y < 1 || y > 9999 {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Неверный параметр 'year' (ожидается год числом, например 2026): %q", yearStr))
			return
		}
		year = y
	}

	month := 0
	if monthStr != "" {
		m, err := strconv.Atoi(monthStr)
		if err != nil || m < 0 || m > 12 {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Неверный параметр 'month' (0 — весь год, 1–12 — месяц): %q", monthStr))
			return
		}
		month = m
	}

	signStr := q.Get("sign")
	ascSign := -1
	if signStr != "" {
		if s, err := strconv.Atoi(signStr); err == nil && s >= 0 && s <= 11 {
			ascSign = s
		}
	}

	cacheKey := generateCacheKey("calendar", q)
	if cachedData, found := cache.Get(cacheKey); found {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		w.WriteHeader(http.StatusOK)
		w.Write(cachedData)
		return
	}

	// Определение геоданных и временной зоны (нужно до разбора периода)
	var loc *time.Location = time.UTC
	var gd *interactive.GeoData
	var err error

	if city != "" {
		gd, err = interactive.GetGeoData(&interactive.UserInput{
			City: city,
			Date: time.Now().Format("2006-01-02"),
			Time: "00:00",
		})
		if err != nil {
			// Без геоданных ответ строить не из чего: раньше здесь была паника
			// (nil deref на gd.City), net/http рвал соединение и клиент получал EOF.
			// Отвечаем так же, как остальные расчётные хендлеры.
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to resolve geo/time: %v", err))
			return
		}
		l, lerr := time.LoadLocation(gd.Timezone)
		if lerr == nil {
			loc = l
		} else {
			offsetSeconds := int(gd.UTCOffset * 3600)
			loc = time.FixedZone(gd.Timezone, offsetSeconds)
		}
	} else {
		gd = &interactive.GeoData{
			City:      "UTC Mode",
			Latitude:  55.7558,
			Longitude: 37.6173,
			Timezone:  "UTC",
			UTCOffset: 0,
		}
	}

	// Произвольный период (start/end) либо год/месяц
	var tStart, tEnd time.Time
	startStr := q.Get("start")
	endStr := q.Get("end")
	if startStr != "" && endStr != "" {
		if ps, perr := parseLocalTime(startStr, loc); perr == nil {
			if pe, eerr := parseLocalTime(endStr, loc); eerr == nil {
				tStart, tEnd = ps, pe
			}
		}
	}
	if tStart.IsZero() {
		if month > 0 {
			tStart = time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
			tEnd = tStart.AddDate(0, 1, 0)
		} else {
			tStart = time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
			tEnd = time.Date(year, 12, 31, 23, 59, 0, 0, time.UTC)
		}
	}

	// Расширяем границы для компенсации сдвигов часовых поясов.
	// ComputeCalendar ожидает границы в UTC (dateToJulian читает настенные компоненты),
	// loc используется только для форматирования времени событий.
	calcStart := tStart.Add(-24 * time.Hour).UTC()
	calcEnd := tEnd.Add(24 * time.Hour).UTC()

	ephePath := GetEphePath()
	calc := astro.NewCalculator(ephePath)
	defer calc.Close()

	res, err := calc.ComputeCalendar(r.Context(), calcStart, calcEnd, loc)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Calculation error: %v", err))
		return
	}

	filter := parseFilterConfig(q)
	res.ApplyFilter(filter)

	// Если задан произвольный период — отсекаем события вне [tStart, tEnd]
	if startStr != "" && endStr != "" {
		lo := tStart.Format("2006-01-02T15:04")
		hi := tEnd.Format("2006-01-02T15:04")
		var kept []astro.CalendarEvent
		for _, e := range res.Events {
			if e.Date >= lo && e.Date <= hi {
				kept = append(kept, e)
			}
		}
		res.Events = kept
	}

	var cal astro.CalendarResponse
	if ascSign >= 0 {
		cal = astro.NewCalendarResponseForSign(res.Events, ascSign)
	} else {
		cal = astro.NewCalendarResponse(res.Events)
	}
	response := CalendarAPIResponse{
		Schema: cal.Schema,
		Meta:   cal.Meta,
		Geo: ResponseMeta{
			City:               gd.City,
			Latitude:           gd.Latitude,
			Longitude:          gd.Longitude,
			Timezone:           gd.Timezone,
			UTCOffset:          gd.UTCOffset,
			CalculationTimeUTC: tStart.Format(time.RFC3339),
		},
		AscSign: cal.AscSign,
		Data:    cal.Data,
	}

	jsonBytes, _ := json.Marshal(response)
	cache.Set(cacheKey, jsonBytes, CacheDuration)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	w.WriteHeader(http.StatusOK)
	w.Write(jsonBytes)
}

// Структура ответа /api/translate
type TranslateResponse struct {
	TranslatedText   string `json:"translated_text"`
	DetectedLanguage string `json:"detected_language,omitempty"`
}

// 5. Обработчик /api/translate — бесплатный неофициальный эндпоинт Google Translate
func handleTranslate(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	text := q.Get("q")
	tl := q.Get("tl")
	sl := q.Get("sl")

	if text == "" {
		writeJSONError(w, http.StatusBadRequest, "Parameter 'q' (text) is required")
		return
	}
	if tl == "" {
		tl = "en"
	}
	if sl == "" {
		sl = "auto"
	}

	// Кэширование (ключ включает текст, языки и исходный язык)
	cacheKey := generateCacheKey("translate", q)
	if cachedData, found := cache.Get(cacheKey); found {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		w.WriteHeader(http.StatusOK)
		w.Write(cachedData)
		return
	}

	googleURL := fmt.Sprintf("https://clients5.google.com/translate_a/t?client=dict-chrome-ex&sl=%s&tl=%s&q=%s",
		url.QueryEscape(sl), url.QueryEscape(tl), url.QueryEscape(text))

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(googleURL)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("Translate service error: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("Translate service returned status %d", resp.StatusCode))
		return
	}

	var raw []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		writeJSONError(w, http.StatusBadGateway, "Invalid response from translate service")
		return
	}
	if len(raw) == 0 {
		writeJSONError(w, http.StatusBadGateway, "Empty response from translate service")
		return
	}

	// Формат ответа зависит от sl:
	//   sl=auto -> [["перевод","язык"]]; явный sl -> ["перевод"]
	translated := ""
	detected := ""
	switch v := raw[0].(type) {
	case string:
		translated = v
	case []interface{}:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				translated = s
			}
		}
		if len(v) > 1 {
			if s, ok := v[1].(string); ok {
				detected = s
			}
		}
	}

	response := TranslateResponse{
		TranslatedText:   translated,
		DetectedLanguage: detected,
	}

	jsonBytes, _ := json.Marshal(response)
	cache.Set(cacheKey, jsonBytes, CacheDuration)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	w.WriteHeader(http.StatusOK)
	w.Write(jsonBytes)
}

// Проверка здоровья
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "healthy", "engine": "integrated"}`))
}
