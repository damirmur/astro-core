package main

// tasks_calendar.go — задача daily_calendar из tasks.go монолита (фаза 5).
// Пишет html/data/daily.json (окно [вчера,+3], Москва). LLM — через llm-1c.
// Раннер стартует из main (startTaskRunner).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/damirmur/swisseph_build/pkg/astro"
)

const (
	tasksStateFile    = "output_data/tasks_state.json"
	tasksFireWindow   = 10 * time.Minute
	tasksDefaultPoll  = 60 * time.Second
	dailyDefaultCity  = "Moscow"
	dailyDefaultTZ    = "Europe/Moscow"
	dailyPastDays     = 1
	dailyFutureDays   = 3
	dailyLLMTimeout   = 180 * time.Second
	dailyCalcTimeout  = 5 * time.Minute
	tasksRunnerEnvRun = "RUN_TASKS_ONCE"
)

// dailyTransit — точный аспект дня в машиночитаемом виде. events хранит все
// события календаря (лунный день, смены знака, станции, Луна без курса), а
// transits — только точные аспекты: их удобно отдавать в рассылку и чат.
type dailyTransit struct {
	At     string `json:"at"` // ISO-8601 со смещением: 2026-11-04T10:33:00+03:00
	P1     string `json:"p1"`
	P2     string `json:"p2"`
	Aspect int    `json:"aspect"`
	De     []int  `json:"de,omitempty"` // эклиптические долготы участников
}

type dailyDay struct {
	Date             string               `json:"date"` // YYYY-MM-DD по Москве
	TZ               string               `json:"tz,omitempty"`
	UTCOffset        float64              `json:"utc_offset,omitempty"`
	Events           []astro.CompactEvent `json:"events"`
	Transits         []dailyTransit       `json:"transits,omitempty"`
	Text             string               `json:"text,omitempty"`    // готовый текст дня (RU, как на главной)
	TextEN           string               `json:"text_en,omitempty"` // то же по-английски
	Interpretation   string               `json:"interpretation,omitempty"`
	InterpretationEN string               `json:"interpretation_en,omitempty"`
	Model            string               `json:"model,omitempty"`
	FetchedAt        string               `json:"fetched_at,omitempty"`
}

type dailyFile struct {
	UpdatedAt string     `json:"updated_at"`
	City      string     `json:"city"`
	TZ        string     `json:"tz"`
	Days      []dailyDay `json:"days"`
}

func loadTaskState() map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(tasksStateFile)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(data, &out)
	return out
}

func saveTaskState(st map[string]string) {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(tasksStateFile), 0o755)
	_ = os.WriteFile(tasksStateFile, data, 0o644)
}

// taskShouldFire — ежедневное срабатывание HH:MM в timezone задачи (дефолт UTC)
// с окном догона tasksFireWindow. Повтор исключается через last_run из state.
func taskShouldFire(now time.Time, t fileTask) (bool, time.Time) {
	loc := time.UTC
	if t.Timezone != "" {
		if l, err := time.LoadLocation(t.Timezone); err == nil {
			loc = l
		}
	}
	hh, mm, err := parseTimeOfDay(t.Schedule)
	if err != nil {
		log.Printf("Задачи: %s — неверный schedule %q: %v", t.ID, t.Schedule, err)
		return false, time.Time{}
	}
	nowLoc := now.In(loc)
	target := time.Date(nowLoc.Year(), nowLoc.Month(), nowLoc.Day(), hh, mm, 0, 0, loc)
	if nowLoc.Before(target) || nowLoc.After(target.Add(tasksFireWindow)) {
		return false, target
	}
	if s := loadTaskState()[t.ID]; s != "" {
		if last, err := time.Parse(time.RFC3339, s); err == nil && !last.Before(target) {
			return false, target
		}
	}
	return true, target
}

func parseTimeOfDay(s string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("ожидается HH:MM, получено %q", s)
	}
	hh, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	mm, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, fmt.Errorf("неверное время %q", s)
	}
	return hh, mm, nil
}
func dailyInt(p map[string]any, key string, def int) int {
	if p == nil {
		return def
	}
	switch v := p[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

func dailyFloat(p map[string]any, key string, def float64) float64 {
	if p == nil {
		return def
	}
	switch v := p[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return def
}

// processDailyCalendar — окно [вчера, +3] по Москве: prune лишнего, добор
// недостающих дней (календарь одним вызовом на всё окно), интерпретация ИИ.
func processDailyCalendar(t fileTask, now time.Time) {
	p := t.Params
	city := dailyStr(p, "city", dailyDefaultCity)
	outPath := dailyStr(p, "out_file", dailyDefaultOut)
	past := dailyInt(p, "past_days", dailyPastDays)
	future := dailyInt(p, "future_days", dailyFutureDays)
	if past < 0 {
		past = dailyPastDays
	}
	if future < 0 {
		future = dailyFutureDays
	}

	msk, err := time.LoadLocation(dailyDefaultTZ)
	if err != nil {
		msk = time.FixedZone("MSK", 3*3600)
	}
	todayMSK := now.In(msk).Format("2006-01-02")
	var want []string
	for d := -past; d <= future; d++ {
		day := now.In(msk).AddDate(0, 0, d).Format("2006-01-02")
		want = append(want, day)
	}
	wantSet := map[string]bool{}
	for _, d := range want {
		wantSet[d] = true
	}

	df := loadDailyFile(outPath, city)
	// Prune: удаляем дни вне окна.
	kept := df.Days[:0]
	for _, d := range df.Days {
		if wantSet[d.Date] {
			kept = append(kept, d)
		}
	}
	df.Days = kept
	have := map[string]*dailyDay{}
	for i := range df.Days {
		have[df.Days[i].Date] = &df.Days[i]
	}
	var missing []string
	for _, d := range want {
		// Нет дня, пустые события, нет готового текста/интерпретации хотя бы
		// на одном языке — такие дни добираем. Текст дня детерминирован:
		// он появился позже, поэтому старые daily.json тоже попадают сюда
		// и молча доливают text/transits без вызова ИИ.
		if dd, ok := have[d]; !ok || strings.TrimSpace(dd.Interpretation) == "" || strings.TrimSpace(dd.InterpretationEN) == "" || strings.TrimSpace(dd.Text) == "" || len(dd.Events) == 0 {
			missing = append(missing, d)
		}
	}
	if len(missing) == 0 {
		log.Printf("Задачи: %s — окно %s…%s полное, пропуск", t.ID, want[0], want[len(want)-1])
		return
	}

	// Календарь одним вызовом на всё окно (расширение ±24ч под сдвиги зон).
	lo, _ := time.ParseInLocation("2006-01-02 15:04", want[0]+" 00:00", msk)
	hi, _ := time.ParseInLocation("2006-01-02 15:04", want[len(want)-1]+" 23:59", msk)
	ctx, cancel := context.WithTimeout(context.Background(), dailyCalcTimeout)
	defer cancel()
	calc := astro.NewCalculator(GetEphePath())
	defer calc.Close()
	res, err := calc.ComputeCalendar(ctx, lo.Add(-24*time.Hour).UTC(), hi.Add(24*time.Hour).UTC(), msk)
	if err != nil {
		log.Printf("Задачи: %s — ошибка календаря: %v", t.ID, err)
		return
	}
	// Полный набор событий: фильтр не применяем (включая lunD/noC/r).
	byDay := map[string][]astro.CompactEvent{}
	for _, d := range want {
		byDay[d] = []astro.CompactEvent{}
	}
	for _, e := range res.Events {
		day := ""
		if len(e.Date) >= 10 {
			day = e.Date[:10]
		}
		if !wantSet[day] {
			continue
		}
		byDay[day] = append(byDay[day], astro.ToCompactEvents([]astro.CalendarEvent{e})[0])
	}

	for _, d := range missing {
		events := byDay[d]
		// Сохраняем уже накопленное (русский текст и события), догенерируем
		// только недостающее: так старые дни без interpretation_en молча
		// добирают английский без перегенерации русского.
		dd := dailyDay{Date: d, Events: events, FetchedAt: now.UTC().Format(time.RFC3339)}
		if old, ok := have[d]; ok {
			dd = *old
			dd.Date = d
			dd.Events = events
			dd.FetchedAt = now.UTC().Format(time.RFC3339)
		}
		// Готовый текст и транзиты — всегда из свежего календаря, без ИИ.
		dd.TZ = dailyDefaultTZ
		dd.UTCOffset = dailyUTCOffset(d, msk)
		dd.Transits = dailyTransits(events, msk)
		dd.Text = dailyText(d, events, msk, false)
		dd.TextEN = dailyText(d, events, msk, true)
		if strings.TrimSpace(dd.Interpretation) == "" {
			text, model, err := interpretDay(t, d, events)
			if err != nil {
				log.Printf("Задачи: %s — день %s: календарь %d событий, ИИ недоступен: %v", t.ID, d, len(events), err)
			} else {
				dd.Interpretation = text
				dd.Model = model
				log.Printf("Задачи: %s — день %s: %d событий, интерпретация %s", t.ID, d, len(events), model)
			}
		}
		if strings.TrimSpace(dd.InterpretationEN) == "" {
			textEn, modelEn, err := interpretDayEn(t, d, events)
			if err != nil {
				log.Printf("Задачи: %s — день %s: EN-интерпретация недоступна: %v", t.ID, d, err)
			} else {
				dd.InterpretationEN = textEn
				log.Printf("Задачи: %s — день %s: EN-интерпретация %s", t.ID, d, modelEn)
			}
		}
		if old, ok := have[d]; ok {
			*old = dd
		} else {
			df.Days = append(df.Days, dd)
			have[d] = &df.Days[len(df.Days)-1]
		}
		df.UpdatedAt = now.UTC().Format(time.RFC3339)
		df.City = city
		df.TZ = dailyDefaultTZ
		sort.Slice(df.Days, func(i, j int) bool { return df.Days[i].Date < df.Days[j].Date })
		saveDailyFile(outPath, df)
		_ = todayMSK
	}
}

func loadDailyFile(path, city string) *dailyFile {
	df := &dailyFile{City: city, TZ: dailyDefaultTZ, Days: []dailyDay{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return df
	}
	var parsed dailyFile
	if err := json.Unmarshal(data, &parsed); err != nil || parsed.Days == nil {
		return df
	}
	if parsed.Days == nil {
		parsed.Days = []dailyDay{}
	}
	return &parsed
}

func saveDailyFile(path string, df *dailyFile) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	data, err := json.MarshalIndent(df, "", "  ")
	if err != nil {
		log.Printf("Задачи: ошибка сериализации %s: %v", path, err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("Задачи: ошибка записи %s: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("Задачи: ошибка публикации %s: %v", path, err)
	}
}

// --- Готовый текст дня для daily.json ---------------------------------------
//
// Тот же вид, что на главной: число, день недели, лунный день/четверть,
// события с временами и подпись «Время (мск)». Собирается кодом, а не ИИ:
// текст детерминирован, не зависит от модели и не тратит токены. Номера домов
// сюда не попадают: daily.json — общий файл, Asc/натальной карты у него нет.

var dailyWeekdaysRu = [...]string{
	"Воскресенье", "Понедельник", "Вторник", "Среда", "Четверг", "Пятница", "Суббота",
}

var dailySignGlyphs = [...]string{
	"♈\uFE0E", "♉\uFE0E", "♊\uFE0E", "♋\uFE0E", "♌\uFE0E", "♍\uFE0E",
	"♎\uFE0E", "♏\uFE0E", "♐\uFE0E", "♑\uFE0E", "♒\uFE0E", "♓\uFE0E",
}

var dailyAspectGlyphs = map[string]string{
	"0": "☌", "30": "⚺", "45": "∠", "60": "⚹", "72": "⬠",
	"90": "☐", "120": "∆", "135": "⚼", "150": "⚻", "180": "☍",
}

var dailyPlanetGlyphs = map[int]string{
	0: "☉", 2: "☿", 3: "♀", 4: "♂", 5: "♃", 6: "♄", 7: "♅",
	8: "♆", 9: "♇", 10: "☊", 11: "☋", 12: "⚸", 14: "♁", 15: "⚷",
	17: "⚳", 18: "⚴", 19: "⚵", 20: "⚶",
}

func dailyMoonGlyph(lunarDay int) string {
	if lunarDay > 15 {
		return "☾"
	}
	return "☽"
}

func dailyPlanetGlyph(id string, lunarDay int) string {
	n, err := strconv.Atoi(strings.TrimSpace(id))
	if err != nil {
		return id
	}
	if n == 1 { // Луна: как на главной, растущая/убывающая
		return dailyMoonGlyph(lunarDay)
	}
	if g, ok := dailyPlanetGlyphs[n]; ok {
		return g
	}
	return id
}

func dailySignGlyph(sign int) string {
	if sign >= 0 && sign < len(dailySignGlyphs) {
		return dailySignGlyphs[sign]
	}
	return ""
}

// dailyZodText — «(12♍︎)»: градус внутри знака + глиф знака.
func dailyZodText(lon int) string {
	lon = ((lon % 360) + 360) % 360
	return fmt.Sprintf("(%d%s)", lon%30, dailySignGlyph(lon/30))
}

func dailyAspectGlyph(a string) string {
	if g, ok := dailyAspectGlyphs[strings.TrimSpace(a)]; ok {
		return g
	}
	return strings.TrimSpace(a) + "°"
}

func dailyQuarterName(lunarDay int, en bool) string {
	q := (lunarDay-1)/7 + 1
	if q < 1 {
		q = 1
	}
	if q > 4 {
		q = 4
	}
	if en {
		return [...]string{"first quarter", "second quarter", "third quarter", "fourth quarter"}[q-1]
	}
	return [...]string{"I четверть", "II четверть", "III четверть", "IV четверть"}[q-1]
}

func dailyEventTime(s string, loc *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02T15:04", strings.TrimSpace(s), loc)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func dailyEventLine(e astro.CompactEvent, loc *time.Location, lunarDay int, en bool) string {
	t, ok := dailyEventTime(e.Date, loc)
	if !ok {
		return ""
	}
	hhmm := t.Format("15:04")
	switch e.Type {
	case "lunD":
		if e.Sign != nil {
			unit := "л.д."
			if en {
				unit = "l.d."
			}
			return fmt.Sprintf("%s → %d %s", hhmm, *e.Sign, unit)
		}
	case "exA":
		if len(e.Planets) >= 2 && len(e.Degrees) >= 2 {
			return fmt.Sprintf("%s ⬤%s%s %s %s%s", hhmm,
				dailyPlanetGlyph(e.Planets[0], lunarDay), dailyZodText(e.Degrees[0]),
				dailyAspectGlyph(e.Aspect),
				dailyPlanetGlyph(e.Planets[1], lunarDay), dailyZodText(e.Degrees[1]))
		}
	case "chS":
		if len(e.Planets) >= 1 && e.Sign != nil {
			return fmt.Sprintf("%s %s → %s", hhmm,
				dailyPlanetGlyph(e.Planets[0], lunarDay), dailySignGlyph(*e.Sign))
		}
	case "noC":
		end := strings.TrimSpace(e.VoidEnd)
		if end == "" {
			end = strings.TrimSpace(e.ChangeSign)
		}
		if end != "" {
			et, ok := dailyEventTime(end, loc)
			if ok {
				s := fmt.Sprintf("%s -%s %s", hhmm, dailyMoonGlyph(lunarDay), et.Format("15:04"))
				if et.Format("2006-01-02") != t.Format("2006-01-02") {
					s += " " + et.Format("02.01")
				}
				return s
			}
		}
	case "r":
		if len(e.Planets) >= 1 {
			dir := "D"
			if e.Retro != nil && *e.Retro == 1 {
				dir = "R"
			}
			return fmt.Sprintf("%s %s %s", hhmm, dailyPlanetGlyph(e.Planets[0], lunarDay), dir)
		}
	}
	return ""
}

// dailyText — готовый текст дня. date — YYYY-MM-DD, events — уже в loc.
func dailyText(date string, events []astro.CompactEvent, loc *time.Location, en bool) string {
	t, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return ""
	}
	lines := []string{t.Format("2")}
	if en {
		lines = append(lines, t.Weekday().String())
	} else {
		lines = append(lines, dailyWeekdaysRu[int(t.Weekday())])
	}
	lunarDay := 0
	for _, e := range events {
		if e.Type == "lunD" && e.Sign != nil {
			lunarDay = *e.Sign
			break
		}
	}
	if lunarDay > 0 {
		if en {
			lines = append(lines, fmt.Sprintf("%d l.d., %s %s", lunarDay, dailyQuarterName(lunarDay, true), dailyMoonGlyph(lunarDay)))
		} else {
			lines = append(lines, fmt.Sprintf("%d л.д., %s %s", lunarDay, dailyQuarterName(lunarDay, false), dailyMoonGlyph(lunarDay)))
		}
	}
	for _, e := range events {
		if line := dailyEventLine(e, loc, lunarDay, en); line != "" {
			lines = append(lines, line)
		}
	}
	if en {
		lines = append(lines, "Time (MSK)")
	} else {
		lines = append(lines, "Время (мск)")
	}
	return strings.Join(lines, "\n")
}

// dailyTransits — только точные аспекты дня, с явным смещением времени.
func dailyTransits(events []astro.CompactEvent, loc *time.Location) []dailyTransit {
	out := make([]dailyTransit, 0, len(events))
	for _, e := range events {
		if e.Type != "exA" || len(e.Planets) < 2 {
			continue
		}
		t, ok := dailyEventTime(e.Date, loc)
		if !ok {
			continue
		}
		angle, _ := strconv.Atoi(strings.TrimSpace(e.Aspect))
		tr := dailyTransit{At: t.Format(time.RFC3339), P1: e.Planets[0], P2: e.Planets[1], Aspect: angle}
		if len(e.Degrees) >= 2 {
			tr.De = []int{e.Degrees[0], e.Degrees[1]}
		}
		out = append(out, tr)
	}
	return out
}

func dailyUTCOffset(date string, loc *time.Location) float64 {
	t, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return 0
	}
	_, off := t.Add(12 * time.Hour).Zone()
	return float64(off) / 3600
}

// Дефолтный промпт, если нет prompts/daily.txt. Плейсхолдеры {{DATE}}, {{LEGEND}} и {{EVENTS}}.
const dailyDefaultPrompt = `Ты — астролог. Дай краткую интерпретацию астрологических событий дня {{DATE}} (время московское) для широкой аудитории, на русском.
Справочник кодов (используй ТОЛЬКО эти русские названия, не выдумывай соответствия):
{{LEGEND}}
События дня (компактный JSON, d — дата/время, t — тип события, p — коды планет, a — угол аспекта в градусах, s — код знака Зодиака или номер лунного дня, de — эклиптические долготы участников в градусах, r — направление: 0 прямое, 1 ретроградное):
{{EVENTS}}
Требования: 500–800 знаков, 2–3 абзаца, спокойный тон, без медицинских/финансовых рекомендаций, в конце одна строка-вывод дня.`

func dailyPrompt(p map[string]any, date string, events []astro.CompactEvent) string {
	tmpl := dailyDefaultPrompt
	if f := dailyStr(p, "prompt_file", "prompts/daily.txt"); f != "" {
		if data, err := os.ReadFile(f); err == nil && len(bytes.TrimSpace(data)) > 0 {
			tmpl = string(data)
		}
	}
	evJSON, _ := json.Marshal(events)
	out := strings.ReplaceAll(tmpl, "{{DATE}}", date)
	out = strings.ReplaceAll(out, "{{LEGEND}}", dailyLegend())
	return strings.ReplaceAll(out, "{{EVENTS}}", string(evJSON))
}

// dailyLegend — русские названия кодов календаря (планеты, знаки, аспекты, типы).
func dailyLegend() string {
	var b strings.Builder
	b.WriteString("Планеты (p): ")
	for i := 0; i <= 12; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d=%s", i, astro.GetPlanetRu(strconv.Itoa(i)))
	}
	b.WriteString("\nЗнаки Зодиака (s): ")
	for i := 0; i < 12; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d=%s", i, astro.GetSignRu(i))
	}
	b.WriteString("\nАспекты (a, градусы): ")
	angles := []string{"0", "30", "60", "72", "90", "120", "135", "180"}
	for i, a := range angles {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s=%s", a, dailyAspectRu(a))
	}
	b.WriteString("\nТипы событий (t): exA=точный аспект, lunD=лунный день (s=номер дня), chS=смена знака (s=новый знак), noC=Луна без курса, r=смена направления (r: 0 прямое, 1 ретроградное)")
	return b.String()
}

const dailyDefaultPromptEn = `You are an astrologer. Give a brief interpretation of the day's astrological events for {{DATE}} (Moscow time) for a general audience, in English.
Code legend (use ONLY these English names, do not invent mappings):
{{LEGEND}}
Day events (compact JSON, d — date/time, t — event type, p — planet codes, a — aspect angle in degrees, s — Zodiac sign code or lunar day number, de — ecliptic longitudes of participants in degrees, r — direction: 0 direct, 1 retrograde):
{{EVENTS}}
Requirements: 500–800 characters, 2–3 paragraphs, calm tone, no medical or financial advice, end with a one-line takeaway of the day.`

func dailyPromptEn(p map[string]any, date string, events []astro.CompactEvent) string {
	tmpl := dailyDefaultPromptEn
	if f := dailyStr(p, "prompt_file_en", "prompts/daily_en.txt"); f != "" {
		if data, err := os.ReadFile(f); err == nil && len(bytes.TrimSpace(data)) > 0 {
			tmpl = string(data)
		}
	}
	evJSON, _ := json.Marshal(events)
	out := strings.ReplaceAll(tmpl, "{{DATE}}", date)
	out = strings.ReplaceAll(out, "{{LEGEND}}", dailyLegendEn())
	return strings.ReplaceAll(out, "{{EVENTS}}", string(evJSON))
}

// dailyLegendEn — английские названия кодов календаря (зеркало dailyLegend,
// swisseph_build не трогаем: английская каноника — из meta API).
func dailyLegendEn() string {
	planets := []string{"Sun", "Moon", "Mercury", "Venus", "Mars", "Jupiter", "Saturn",
		"Uranus", "Neptune", "Pluto", "Mean Node", "North Node", "Lilith"}
	signs := []string{"Aries", "Taurus", "Gemini", "Cancer", "Leo", "Virgo",
		"Libra", "Scorpio", "Sagittarius", "Capricorn", "Aquarius", "Pisces"}
	aspects := [][2]string{
		{"0", "Conjunction (0°)"}, {"30", "Semi-sextile (30°)"},
		{"60", "Sextile (60°)"}, {"72", "Quintile (72°)"},
		{"90", "Square (90°)"}, {"120", "Trine (120°)"},
		{"135", "Sesquiquadrate (135°)"}, {"180", "Opposition (180°)"},
	}
	var b strings.Builder
	b.WriteString("Planets (p): ")
	for i, name := range planets {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d=%s", i, name)
	}
	b.WriteString("\nZodiac signs (s): ")
	for i, name := range signs {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d=%s", i, name)
	}
	b.WriteString("\nAspects (a, degrees): ")
	for i, a := range aspects {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s=%s", a[0], a[1])
	}
	b.WriteString("\nEvent types (t): exA=exact aspect, lunD=lunar day (s=day number), chS=sign ingress (s=new sign), noC=void-of-course Moon, r=station change (r: 0 direct, 1 retrograde)")
	return b.String()
}

// dailyAspectRu — GetAspectRu плюс углы, которых нет в базовом словаре.
func dailyAspectRu(a string) string {
	if ru := astro.GetAspectRu(a); ru != a {
		return ru
	}
	switch a {
	case "30":
		return "Полусекстиль (30°)"
	case "72":
		return "Квинтиль (72°)"
	case "135":
		return "Сесквиквадрат (135°)"
	}
	return a
}

func interpretDay(t fileTask, date string, events []astro.CompactEvent) (string, string, error) {
	return interpretWithPrompt(t.Params, dailyPrompt(t.Params, date, events))
}

// interpretDayEn — английская интерпретация дня (для interpretation_en).
// Та же логика выбора моделей, но английский промпт и легенда.
func interpretDayEn(t fileTask, date string, events []astro.CompactEvent) (string, string, error) {
	return interpretWithPrompt(t.Params, dailyPromptEn(t.Params, date, events))
}

// ─── Раннер daily_calendar (в монолите — runTasksOnce; здесь только календарь) ─

var tasksRunning sync.Mutex

// startTaskRunner — тик по tasks.json; RUN_TASKS_ONCE=1 — один прогон и выход.
func startTaskRunner() {
	go func() {
		time.Sleep(2 * time.Second)
		log.Println("astro: планировщик daily_calendar запущен (tasks.json).")
		if os.Getenv(tasksRunnerEnvRun) == "1" {
			runCalendarOnce()
			return
		}
		runCalendarOnce()
		ticker := time.NewTicker(tasksPollInterval())
		defer ticker.Stop()
		for range ticker.C {
			runCalendarOnce()
		}
	}()
}

func tasksPollInterval() time.Duration {
	if tf, err := loadTaskFile(); err == nil && tf.PollIntervalSec > 0 {
		return time.Duration(tf.PollIntervalSec) * time.Second
	}
	return tasksDefaultPoll
}

func runCalendarOnce() {
	if !tasksRunning.TryLock() {
		return
	}
	defer tasksRunning.Unlock()
	tf, err := loadTaskFile()
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("astro: ошибка чтения %s: %v", tasksFileName, err)
		}
		return
	}
	now := time.Now()
	st := loadTaskState()
	changed := false
	for _, t := range tf.Tasks {
		if !t.Enabled || t.Type != "daily_calendar" {
			continue
		}
		if os.Getenv(tasksRunnerEnvRun) != "1" {
			if fire, _ := taskShouldFire(now, t); !fire {
				continue
			}
		}
		processDailyCalendar(t, now)
		st[t.ID] = now.UTC().Format(time.RFC3339)
		changed = true
	}
	if changed {
		saveTaskState(st)
	}
}

// ─── LLM через шлюз llm-1c (в монолите — прямые пулы keyPool) ─────────────────

// llmBaseURL — шлюз llm-1c (ASTRO_BASE_URL нет: это LLM_GATEWAY_URL).
func llmBaseURL() string {
	if u := strings.TrimSpace(os.Getenv("LLM_GATEWAY_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://127.0.0.1:8086"
}

func llmKey() string {
	if v := strings.TrimSpace(os.Getenv("LLM_PROXY_KEY")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("NOTIFY_KEY"))
}

// resolveChatModels — явный список (DAILY_LLM_MODELS > params.llm_models,
// формат "provider/model") либо автопоиск *free* через GET /v1/models шлюза.
func resolveChatModels(p map[string]any) []string {
	if env := strings.TrimSpace(os.Getenv("DAILY_LLM_MODELS")); env != "" {
		var out []string
		for _, s := range strings.Split(env, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if v, ok := p["llm_models"].([]any); ok {
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return discoverFreeModels()
}

// discoverFreeModels — живые *free* chat-модели шлюза (muse-spark/embeddings мимо).
func discoverFreeModels() []string {
	key := llmKey()
	if key == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodGet, llmBaseURL()+"/v1/models", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	var out []string
	for _, m := range parsed.Data {
		low := strings.ToLower(m.ID)
		if !strings.Contains(low, "free") {
			continue
		}
		if strings.Contains(low, "muse-spark") || strings.Contains(low, "embed") {
			continue
		}
		out = append(out, m.ID)
	}
	sort.Strings(out)
	return out
}

// chatCompleteLLM — POST /v1/chat/completions шлюза (без стрима).
func chatCompleteLLM(model, prompt string, maxTokens int, temp float64) (string, error) {
	key := llmKey()
	if key == "" {
		return "", fmt.Errorf("не задан LLM_PROXY_KEY")
	}
	body, _ := json.Marshal(map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens":  maxTokens,
		"temperature": temp,
		"stream":      false,
	})
	req, err := http.NewRequest(http.MethodPost, llmBaseURL()+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: dailyLLMTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%s: HTTP %d", model, resp.StatusCode)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("%s: плохой JSON ответа: %v", model, err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("%s: пустые choices", model)
	}
	return parsed.Choices[0].Message.Content, nil
}

func interpretWithPrompt(p map[string]any, prompt string) (string, string, error) {
	providers := []string{"zen", "routerai"}
	if v, ok := p["llm_providers"].([]any); ok && len(v) > 0 {
		providers = providers[:0]
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				providers = append(providers, s)
			}
		}
	}
	_ = providers // провайдеры используются шлюзом; здесь — для логов ниже
	maxTokens := dailyInt(p, "max_tokens", 1500)
	temp := dailyFloat(p, "temperature", 0.7)
	cands := resolveChatModels(p)
	if len(cands) == 0 {
		return "", "", fmt.Errorf("нет доступных моделей (провайдеры %v)", providers)
	}
	var lastErr error
	for _, model := range cands {
		text, err := chatCompleteLLM(model, prompt, maxTokens, temp)
		if err != nil {
			lastErr = err
			continue
		}
		if strings.TrimSpace(text) == "" {
			lastErr = fmt.Errorf("%s: пустой ответ", model)
			continue
		}
		return strings.TrimSpace(text), model, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("все модели недоступны")
	}
	return "", "", lastErr
}
