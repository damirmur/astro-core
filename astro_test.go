// Юнит-тесты astro-core: разбор параметров, JSON ошибок и гейты валидации.
// Расчёты (Swiss Ephemeris) проверяются эталонами в контрактных тестах
// astro3d-e2e, здесь — только то, что не требует CGO.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestWriteJSONError(t *testing.T) {
	cases := []struct {
		name   string
		status int
		msg    string
	}{
		{"обычный текст", http.StatusBadRequest, "Неверный параметр 'year'"},
		{"кавычки внутри", http.StatusBadRequest, `parsing time "1990-13-45 14:30": month out of range`},
		{"переводы строк", http.StatusInternalServerError, "строка 1\nстрока 2"},
		{"юникод и слэши", http.StatusBadGateway, `путь C:\temp\файл и "кавычки"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeJSONError(w, c.status, c.msg)

			if w.Code != c.status {
				t.Errorf("код %d, ожидался %d", w.Code, c.status)
			}
			if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
				t.Errorf("Content-Type=%q", ct)
			}
			var out map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatalf("тело не JSON (%q): %v", w.Body.String(), err)
			}
			if out["error"] != c.msg {
				t.Errorf("error=%q, ожидалось %q", out["error"], c.msg)
			}
		})
	}
}

func TestParseHousesMode(t *testing.T) {
	cases := []struct {
		in      string
		none    bool
		wantErr bool
	}{
		{"", false, false},
		{"full", false, false},
		{"none", true, false},
		{" none ", true, false},
		{"NONE", false, true},
		{"partial", false, true},
	}
	for _, c := range cases {
		q := url.Values{}
		if c.in != "" {
			q.Set("houses", c.in)
		}
		none, err := parseHousesMode(q)
		if (err != nil) != c.wantErr {
			t.Errorf("houses=%q: ошибка %v, ожидалась %v", c.in, err, c.wantErr)
		}
		if err == nil && none != c.none {
			t.Errorf("houses=%q: none=%v, ожидалось %v", c.in, none, c.none)
		}
	}
}

func TestParseIDList(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"", nil},
		{"0,1,4", []int{0, 1, 4}},
		{" 0 , 1 ", []int{0, 1}},
		{"0,,1", []int{0, 1}},
		{"abc,2", []int{2}}, // нечисловые пропускаем, не падаем
	}
	for _, c := range cases {
		got := parseIDList(c.in)
		if len(got) != len(c.want) {
			t.Errorf("parseIDList(%q)=%v, ожидалось %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parseIDList(%q)=%v, ожидалось %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestParseAngleList(t *testing.T) {
	got := parseAngleList(" 0 , 90.5 ,, 180 ")
	want := []float64{0, 90.5, 180}
	if len(got) != len(want) {
		t.Fatalf("parseAngleList=%v, ожидалось %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseAngleList=%v, ожидалось %v", got, want)
			break
		}
	}
	if l := parseAngleList("abc"); len(l) != 0 {
		t.Errorf("мусор превратился в углы: %v", l)
	}
}

func TestParsePeriodRange(t *testing.T) {
	t.Run("без города: дата и время", func(t *testing.T) {
		start, end, err := parsePeriodRange("2026-01-01 00:00", "2026-01-05 12:30", "")
		if err != nil {
			t.Fatalf("parsePeriodRange: %v", err)
		}
		if want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); !start.Equal(want) {
			t.Errorf("start=%v, ожидалось %v", start, want)
		}
		if want := time.Date(2026, 1, 5, 12, 30, 0, 0, time.UTC); !end.Equal(want) {
			t.Errorf("end=%v, ожидалось %v", end, want)
		}
	})

	t.Run("без города: только дата", func(t *testing.T) {
		start, _, err := parsePeriodRange("2026-03-10", "2026-03-11", "")
		if err != nil {
			t.Fatalf("parsePeriodRange: %v", err)
		}
		if want := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC); !start.Equal(want) {
			t.Errorf("start=%v, ожидалось %v", start, want)
		}
	})

	t.Run("пустой конец — та же дата", func(t *testing.T) {
		start, end, err := parsePeriodRange("2026-03-10", "", "")
		if err != nil {
			t.Fatalf("parsePeriodRange: %v", err)
		}
		if !start.Equal(end) {
			t.Errorf("start=%v, end=%v — ожидалось равенство", start, end)
		}
	})

	t.Run("мусор — ошибка", func(t *testing.T) {
		for _, in := range [][2]string{{"xxx", "yyy"}, {"2026-13-45", "2026-01-01"}, {"2026-01-01 25:99", "2026-01-02"}} {
			if _, _, err := parsePeriodRange(in[0], in[1], ""); err == nil {
				t.Errorf("%v: ошибки нет, дата принята молча", in)
			}
		}
	})
}

func TestHandleGeoErrors(t *testing.T) {
	get := func(target string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handleGeo(w, httptest.NewRequest(http.MethodGet, target, nil))
		return w
	}

	t.Run("без города — 400 JSON", func(t *testing.T) {
		w := get("/api/v1/geo")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("код %d", w.Code)
		}
		var out map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("тело не JSON: %q", w.Body.String())
		}
		if out["error"] == "" {
			t.Error("нет текста ошибки")
		}
	})

	t.Run("мусорный город — 404 JSON", func(t *testing.T) {
		w := get("/api/v1/geo?city=" + url.QueryEscape("Ззззъъъ"))
		if w.Code != http.StatusNotFound {
			t.Fatalf("код %d, тело %q", w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type=%q", ct)
		}
	})

	t.Run("POST — 405 JSON", func(t *testing.T) {
		w := httptest.NewRecorder()
		handleGeo(w, httptest.NewRequest(http.MethodPost, "/api/v1/geo", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("код %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), "error") {
			t.Errorf("тело %q", w.Body.String())
		}
	})
}

func TestValidationHandlers(t *testing.T) {
	// Валидация отбивает запрос до расчёта, поэтому эфемериды не нужны.
	call := func(h http.HandlerFunc, target string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest(http.MethodGet, target, nil))
		return w
	}
	cases := []struct {
		name   string
		h      http.HandlerFunc
		target string
	}{
		{"calendar: год не число", handleCalendar, "/api/v1/calendar?lat=55.75&lon=37.61&year=abc"},
		{"calendar: месяц не число", handleCalendar, "/api/v1/calendar?lat=55.75&lon=37.61&year=2026&month=2026-11"},
		{"calendar: год вне диапазона", handleCalendar, "/api/v1/calendar?lat=55.75&lon=37.61&year=99999"},
		{"period: мусорные даты", handlePeriod, "/api/v1/period?lat=55.75&lon=37.61&start=xxx&end=yyy"},
		{"period: конец раньше начала", handlePeriod, "/api/v1/period?lat=55.75&lon=37.61&start=2026-01-05%2000:00&end=2026-01-01%2000:00"},
		{"natal: дата мимо календаря", handleNatal, "/api/v1/natal?lat=55.75&lon=37.61&date=1990-13-45&time=14:30"},
		{"natal: нет даты", handleNatal, "/api/v1/natal?lat=55.75&lon=37.61"},
		{"synastry: нет дат", handleSynastry, "/api/v1/synastry?lat=55.75&lon=37.61"},
		{"synastry: дата2 мимо календаря", handleSynastry, "/api/v1/synastry?lat=55.75&lon=37.61&date1=1990-05-15&date2=1990-13-99"},
		{"natal-period: нет даты", handleNatalPeriod, "/api/v1/natal-period?lat=55.75&lon=37.61"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := call(c.h, c.target)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("код %d, ожидался 400 (тело %q)", w.Code, w.Body.String())
			}
			var out map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatalf("тело не JSON (%q): %v", w.Body.String(), err)
			}
			if out["error"] == "" {
				t.Error("пустой текст ошибки")
			}
		})
	}
}

func TestHealthz(t *testing.T) {
	w := httptest.NewRecorder()
	handleHealth(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("код %d", w.Code)
	}
	var out map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("тело не JSON: %v", err)
	}
	if out["status"] != "healthy" {
		t.Errorf("status=%q", out["status"])
	}
}

func TestCacheKeyIgnoresJunkParams(t *testing.T) {
	a := url.Values{"city": {"Москва"}, "date": {"1990-05-15"}, "x": {"1"}}
	b := url.Values{"city": {"Москва"}, "date": {"1990-05-15"}, "x": {"2"}, "utm_source": {"tg"}}
	if generateCacheKey("natal", a) != generateCacheKey("natal", b) {
		t.Fatal("мусорные параметры не должны менять ключ кэша")
	}
	c := url.Values{"city": {"Москва"}, "date": {"1990-05-16"}}
	if generateCacheKey("natal", a) == generateCacheKey("natal", c) {
		t.Fatal("значимый параметр date должен менять ключ")
	}
}

func TestCacheKeyCalendarRange(t *testing.T) {
	month := url.Values{"year": {"2031"}, "month": {"3"}, "city": {"Москва"}}
	window := url.Values{"year": {"2031"}, "month": {"3"}, "city": {"Москва"},
		"start": {"2031-03-10"}, "end": {"2031-03-12"}}
	if generateCacheKey("calendar", month) == generateCacheKey("calendar", window) {
		t.Fatal("start/end должны менять ключ календаря: иначе окно отдаётся как месяц")
	}
	other := url.Values{"year": {"2031"}, "month": {"3"}, "city": {"Москва"},
		"start": {"2031-03-20"}, "end": {"2031-03-22"}}
	if generateCacheKey("calendar", window) == generateCacheKey("calendar", other) {
		t.Fatal("разные окна должны давать разные ключи")
	}
	filtered := url.Values{"year": {"2031"}, "month": {"3"}, "city": {"Москва"}, "event_types": {"noC,exA"}}
	if generateCacheKey("calendar", month) == generateCacheKey("calendar", filtered) {
		t.Fatal("event_types должен менять ключ календаря")
	}
}

func TestMemoryCacheLimits(t *testing.T) {
	c := newMemoryCache(3, 100)
	for i := 0; i < 10; i++ {
		c.Set(string(rune('a'+i)), []byte(strings.Repeat("x", 40)), time.Hour)
	}
	if len(c.items) > 3 {
		t.Fatalf("записей %d, лимит 3", len(c.items))
	}
	if c.bytes > 100 {
		t.Fatalf("байт %d, лимит 100", c.bytes)
	}
	total := 0
	for _, v := range c.items {
		total += len(v.Response)
	}
	if int64(total) != c.bytes {
		t.Fatalf("счётчик байт %d не совпадает с суммой %d", c.bytes, total)
	}
}

func TestPeriodBounds(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		start time.Time
		end   time.Time
		step  int
		ok    bool
	}{
		{"год посуточно", start, start.AddDate(1, 0, 0), 24, true},
		{"десять лет посуточно", start, start.AddDate(10, 0, 0), 24, true},
		{"больше десяти лет", start, start.AddDate(11, 0, 0), 24, false},
		{"год почасово", start, start.AddDate(1, 0, 0), 1, false},
		{"конец раньше начала", start.AddDate(0, 0, 1), start, 24, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePeriodBounds(c.start, c.end, c.step)
			if c.ok && err != nil {
				t.Fatalf("ожидалось ok, получено %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("ожидалась ошибка")
			}
		})
	}
}
