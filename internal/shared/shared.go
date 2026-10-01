// Пакет shared — общий код для разделённых веб-сервисов.
//
// Выделен из монолита astro-api (main.go) без изменения логики, чтобы
// cmd/site, cmd/astro, cmd/llmgw и cmd/erp использовали одни и те же
// хелперы вместо копипаста: CORS, кэш в памяти, пути к бинарю/эфемеридам,
// запись JSON, health-проверка.
package shared

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Длительность кэша астро-ответов (как CacheDuration в main.go).
const CacheDuration = 24 * time.Hour

// CacheEntry — закэшированный ответ с дедлайном.
type CacheEntry struct {
	Response   []byte
	Expiration time.Time
}

// MemoryCache — потокобезопасный кэш в памяти с часовой чисткой.
type MemoryCache struct {
	mu    sync.RWMutex
	items map[string]CacheEntry
}

// NewMemoryCache создаёт кэш и запускает фоновую чистку просрочки.
func NewMemoryCache() *MemoryCache {
	cache := &MemoryCache{
		items: make(map[string]CacheEntry),
	}
	go cache.startCleanupTimer()
	return cache
}

// Get возвращает ответ, если ключ есть и не просрочен.
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

// Set кладёт ответ в кэш на duration.
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

// GenerateCacheKey — ключ кэша по префиксу и query-параметрам.
func GenerateCacheKey(prefix string, params url.Values) string {
	hash := sha256.Sum256([]byte(prefix + "?" + params.Encode()))
	return fmt.Sprintf("%x", hash)
}

// CORSMiddleware — открытый CORS для публичных GET-ручек.
func CORSMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}

// CORSAuthMiddleware — CORS для авторизованных ручек (нужны Bearer,
// биллингу — POST/DELETE).
func CORSAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

// WriteJSON пишет v как JSON с utf-8 (ошибка — только в лог).
func WriteJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("shared: ошибка записи JSON: %v", err)
	}
}

// Truncate обрезает строку для логов/ошибок.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// GetExecutableDir — папка с бинарём (рядом лежат keys.json/tasks.json).
func GetExecutableDir() string {
	exePath, err := os.Executable()
	if err != nil {
		log.Printf("shared: ошибка получения пути процесса: %v, используем текущую директорию", err)
		return "."
	}
	return filepath.Dir(exePath)
}

// GetEphePath — путь к эфемеридам: EPHE_PATH → рядом с бинарём → ./ephe.
func GetEphePath() string {
	if envPath := os.Getenv("EPHE_PATH"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath
		}
	}

	if exePath, err := os.Executable(); err == nil {
		path := filepath.Join(filepath.Dir(exePath), "ephe")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	if _, err := os.Stat("ephe"); err == nil {
		return "ephe"
	}

	return "ephe"
}

// HandleHealth — проверка здоровья (единый формат всех сервисов).
func HandleHealth(service string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"healthy","service":%q}`, service)
	}
}
