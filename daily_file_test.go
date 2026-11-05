// Тесты daily.json и задач: пути, состояние, окно срабатывания, промпты,
// чтение/запись файла и ручка /api/daily. Сеть не нужна.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/damirmur/swisseph_build/pkg/astro"
)

func TestParseTimeOfDayAndParams(t *testing.T) {
	cases := []struct {
		in   string
		h, m int
		ok   bool
	}{
		{"10:05", 10, 5, true},
		{" 9 : 7 ", 9, 7, true},
		{"00:00", 0, 0, true},
		{"23:59", 23, 59, true},
		{"24:00", 0, 0, false},
		{"10:60", 0, 0, false},
		{"10", 0, 0, false},
		{"a:b", 0, 0, false},
	}
	for _, c := range cases {
		h, m, err := parseTimeOfDay(c.in)
		if (err == nil) != c.ok || (c.ok && (h != c.h || m != c.m)) {
			t.Errorf("parseTimeOfDay(%q)=%d,%d,%v", c.in, h, m, err)
		}
	}

	if dailyStr(nil, "x", "def") != "def" ||
		dailyStr(map[string]any{"x": "  v  "}, "x", "def") != "v" ||
		dailyStr(map[string]any{"x": "  "}, "x", "def") != "def" ||
		dailyStr(map[string]any{"x": 1}, "x", "def") != "def" {
		t.Error("dailyStr")
	}
	if dailyInt(map[string]any{"n": 3.9}, "n", 7) != 3 ||
		dailyInt(map[string]any{"n": 4}, "n", 7) != 4 ||
		dailyInt(map[string]any{"n": "5"}, "n", 7) != 7 ||
		dailyInt(nil, "n", 7) != 7 {
		t.Error("dailyInt")
	}
	if dailyFloat(map[string]any{"n": 3.5}, "n", 7) != 3.5 ||
		dailyFloat(map[string]any{"n": 4}, "n", 7) != 4 ||
		dailyFloat(map[string]any{"n": "5"}, "n", 7) != 7 ||
		dailyFloat(nil, "n", 7) != 7 {
		t.Error("dailyFloat")
	}
}

func TestTaskShouldFire(t *testing.T) {
	t.Chdir(t.TempDir())
	now := time.Date(2026, 11, 5, 10, 5, 0, 0, time.UTC)

	task := fileTask{ID: "daily", Schedule: "10:00"}
	ok, target := taskShouldFire(now, task)
	if !ok || !target.Equal(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("в окне: %v %v", ok, target)
	}
	if ok, _ := taskShouldFire(now.Add(6*time.Minute), task); ok {
		t.Error("сработало после окна 10 минут")
	}
	if ok, _ := taskShouldFire(now.Add(-6*time.Minute), task); ok {
		t.Error("сработало до времени")
	}
	if ok, _ := taskShouldFire(now, fileTask{ID: "bad", Schedule: "25:00"}); ok {
		t.Error("неверное расписание сработало")
	}

	// Повтор не проходит, пока last_run >= target.
	saveTaskState(map[string]string{"daily": "2026-11-05T10:00:00Z"})
	if ok, _ := taskShouldFire(now, task); ok {
		t.Error("повторный запуск в тот же день")
	}
	saveTaskState(map[string]string{"daily": "2026-11-04T10:00:00Z"})
	if ok, _ := taskShouldFire(now, task); !ok {
		t.Error("новый день не сработал")
	}

	// Часовой пояс задачи: 13:05 МСК — это 10:05 UTC.
	msk := fileTask{ID: "msk", Schedule: "13:00", Timezone: "Europe/Moscow"}
	if ok, _ := taskShouldFire(now, msk); !ok {
		t.Error("МСК-задача не сработала")
	}
	// Неизвестный пояс — фолбэк UTC.
	if ok, _ := taskShouldFire(now, fileTask{ID: "tz", Schedule: "10:00", Timezone: "Bad/Zone"}); !ok {
		t.Error("неизвестный пояс не откатился в UTC")
	}
}

func TestTaskFileAndDailyPath(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := loadTaskFile(); err == nil {
		t.Error("отсутствующий tasks.json прочитан")
	}
	tf := taskFile{PollIntervalSec: 30, Tasks: []fileTask{
		{ID: "a", Type: "other", Enabled: true, Params: map[string]any{"out_file": "no.json"}},
		{ID: "b", Type: "daily_calendar", Enabled: true, Params: map[string]any{"out_file": "custom/daily.json"}},
	}}
	data, _ := json.Marshal(tf)
	if err := os.WriteFile(tasksFileName, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadTaskFile()
	if err != nil || got.PollIntervalSec != 30 || len(got.Tasks) != 2 {
		t.Fatalf("loadTaskFile: %+v %v", got, err)
	}
	if p := dailyOutPath(); p != "custom/daily.json" {
		t.Errorf("dailyOutPath=%q", p)
	}
	if err := os.WriteFile(tasksFileName, []byte(`{"tasks":[{"type":"daily_calendar","enabled":false,"params":{"out_file":"x"}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := dailyOutPath(); p != dailyDefaultOut {
		t.Errorf("выключенная задача: %q", p)
	}
}

func TestLoadSaveDailyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "daily.json")
	df := loadDailyFile(path, "Орск")
	if df == nil || df.City != "Орск" || df.TZ != dailyDefaultTZ || len(df.Days) != 0 {
		t.Fatalf("пустой файл: %+v", df)
	}
	df.Days = append(df.Days, dailyDay{Date: "2026-11-05", Text: "день"})
	saveDailyFile(path, df)
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("временный файл остался")
	}
	back := loadDailyFile(path, "Москва")
	if back.City != "Орск" || len(back.Days) != 1 || back.Days[0].Text != "день" {
		t.Errorf("после записи: %+v", back)
	}
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadDailyFile(path, "Москва"); got.City != "Москва" || len(got.Days) != 0 {
		t.Errorf("битый JSON: %+v", got)
	}
}

func TestHandleDailyFile(t *testing.T) {
	t.Chdir(t.TempDir())
	// Нет tasks.json — путь по умолчанию, файла нет.
	w := httptest.NewRecorder()
	handleDailyFile(w, httptest.NewRequest(http.MethodGet, "/api/daily", nil))
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "не готовы") {
		t.Errorf("нет файла: %d %q", w.Code, w.Body.String())
	}

	if err := os.MkdirAll("html/data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dailyDefaultOut, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handleDailyFile(w, httptest.NewRequest(http.MethodGet, "/api/daily", nil))
	if w.Code != http.StatusOK || w.Body.String() != `{"ok":true}` ||
		w.Header().Get("Content-Type") != "application/json" ||
		w.Header().Get("Cache-Control") != "public, max-age=600" {
		t.Errorf("файл: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	w = httptest.NewRecorder()
	handleDailyFile(w, httptest.NewRequest(http.MethodHead, "/api/daily", nil))
	if w.Code != http.StatusOK {
		t.Errorf("HEAD: %d", w.Code)
	}
	w = httptest.NewRecorder()
	handleDailyFile(w, httptest.NewRequest(http.MethodPost, "/api/daily", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", w.Code)
	}
}

func TestDailyPromptsAndLegend(t *testing.T) {
	t.Chdir(t.TempDir())
	events := []astro.CompactEvent{
		{Date: "2026-11-05T10:00", Type: "exA", Planets: []string{"1", "0"}, Aspect: "60"},
	}
	prompt := dailyPrompt(nil, "2026-11-05", events)
	for _, want := range []string{"2026-11-05", "Планеты (p)", "exA", "Типы событий"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("промпт без %q:\n%s", want, prompt)
		}
	}
	if !strings.Contains(dailyPromptEn(nil, "2026-11-05", events), "Conjunction") {
		t.Error("EN-легенда без Conjunction")
	}

	if err := os.MkdirAll("prompts", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("prompts/daily.txt", []byte("Дата {{DATE}}; {{LEGEND}}; {{EVENTS}}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := dailyPrompt(nil, "2026-11-05", events)
	if !strings.HasPrefix(got, "Дата 2026-11-05;") || !strings.Contains(got, "exA") {
		t.Errorf("свой промпт: %q", got)
	}

	if dailyAspectRu("30") == "" || dailyAspectRu("999") != "999" {
		t.Errorf("dailyAspectRu: %q %q", dailyAspectRu("30"), dailyAspectRu("999"))
	}
}
