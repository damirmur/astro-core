package main

// daily_file.go — /api/daily и пути daily.json (из tasks.go монолита).
// Пишет файл по-прежнему планировщик монолита (фаза 5); astro-core только отдаёт.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	tasksFileName   = "tasks.json"
	dailyDefaultOut = "html/data/daily.json"
)

type fileTask struct {
	ID       string         `json:"id"`
	Enabled  bool           `json:"enabled"`
	Type     string         `json:"type"`
	Schedule string         `json:"schedule"` // HH:MM в timezone задачи (по умолчанию UTC)
	Timezone string         `json:"timezone"`
	Params   map[string]any `json:"params"`
}

type taskFile struct {
	PollIntervalSec int        `json:"poll_interval_sec"`
	Tasks           []fileTask `json:"tasks"`
}

func tasksFilePath() string {
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), tasksFileName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return tasksFileName
}

func loadTaskFile() (*taskFile, error) {
	data, err := os.ReadFile(tasksFilePath())
	if err != nil {
		return nil, err
	}
	var tf taskFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return nil, err
	}
	return &tf, nil
}

func dailyStr(p map[string]any, key, def string) string {
	if p == nil {
		return def
	}
	if v, ok := p[key].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func dailyOutPath() string {
	if tf, err := loadTaskFile(); err == nil {
		for _, t := range tf.Tasks {
			if t.Type == "daily_calendar" && t.Enabled {
				if s := dailyStr(t.Params, "out_file", ""); s != "" {
					return s
				}
			}
		}
	}
	return dailyDefaultOut
}

func handleDailyFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := os.ReadFile(dailyOutPath())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"интерпретации дня пока не готовы"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
