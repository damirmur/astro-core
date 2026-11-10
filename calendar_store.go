package main

// calendar_store.go — годовое хранилище событий календаря.
//
// Календарь считается тяжело: месяц ~0,7 с, год ~2,6 с CPU. Набор событий
// от города не зависит (loc в pkg/astro/calendar.go используется только для
// форматирования дат), поэтому храним сырые события в UTC по годам и на
// чтении фильтруем по диапазону, применяем фильтры и форматируем в поясе
// запроса. Один файл обслуживает все города.
//
// Файл: html/data/calendar/<year>.json (gitignored), атомарная запись.
// Заполнение: при старте и раз в сутки — текущий и следующий год; 1 января
// следующий год появляется сам. Остальные годы считаются по запросу (fallback).
//
// ВАЖНО: calendarStoreVersion менять при правках pkg/astro/calendar*.go или
// обновлении Swiss Ephemeris — иначе старые минуты событий останутся в файлах.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/damirmur/swisseph_build/pkg/astro"
)

const calendarStoreVersion = "calendar/v1;se=2.10.03"

var errCalendarBusy = errors.New("calendar store: расчёт занят")

// calendarYearFile — один год сырых событий в UTC.
type calendarYearFile struct {
	Year       int                   `json:"year"`
	Version    string                `json:"version"`
	ComputedAt string                `json:"computed_at"`
	From       string                `json:"from"` // UTC, YYYY-MM-DDTHH:MM
	To         string                `json:"to"`
	Events     []astro.CalendarEvent `json:"events"`
}

func calendarStoreDir() string {
	if v := strings.TrimSpace(os.Getenv("CALENDAR_STORE_DIR")); v != "" {
		return v
	}
	return filepath.Join("html", "data", "calendar")
}

func calendarYearPath(year int) string {
	return filepath.Join(calendarStoreDir(), fmt.Sprintf("%d.json", year))
}

// loadCalendarYear читает год; ошибка — файла нет или версия устарела.
func loadCalendarYear(year int) (*calendarYearFile, error) {
	data, err := os.ReadFile(calendarYearPath(year))
	if err != nil {
		return nil, err
	}
	var f calendarYearFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("календарь %d: битый JSON: %w", year, err)
	}
	if f.Year != year {
		return nil, fmt.Errorf("календарь %d: внутри год %d", year, f.Year)
	}
	if f.Version != calendarStoreVersion {
		return nil, fmt.Errorf("календарь %d: версия %q, нужна %q", year, f.Version, calendarStoreVersion)
	}
	if len(f.Events) == 0 {
		return nil, fmt.Errorf("календарь %d: пустой набор", year)
	}
	return &f, nil
}

// saveCalendarYear пишет атомарно: tmp + rename, чтобы читатель не увидел
// половину файла.
func saveCalendarYear(f *calendarYearFile) error {
	dir := calendarStoreDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	final := calendarYearPath(f.Year)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// computeCalendarYear считает весь год в UTC (те же границы, что у запроса
// month=0: Jan 1 −24 ч … Dec 31 23:59 +24 ч).
func computeCalendarYear(ctx context.Context, year int) (*calendarYearFile, error) {
	tStart := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	tEnd := time.Date(year, 12, 31, 23, 59, 0, 0, time.UTC)
	calcStart := tStart.Add(-24 * time.Hour)
	calcEnd := tEnd.Add(24 * time.Hour)

	calc := astro.NewCalculator(GetEphePath())
	defer calc.Close()
	res, err := calc.ComputeCalendar(ctx, calcStart, calcEnd, time.UTC)
	if err != nil {
		return nil, err
	}
	return &calendarYearFile{
		Year:       year,
		Version:    calendarStoreVersion,
		ComputedAt: time.Now().UTC().Format(time.RFC3339),
		From:       calcStart.Format("2006-01-02T15:04"),
		To:         calcEnd.Format("2006-01-02T15:04"),
		Events:     res.Events,
	}, nil
}

// ensureCalendarYear проверяет файл и при необходимости пересчитывает.
// Тяжёлый слот берём без ожидания: фоновый предрасчёт не должен выбивать
// 503 у живых запросов, лучше повторим позже.
func ensureCalendarYear(ctx context.Context, year int) error {
	if _, err := loadCalendarYear(year); err == nil {
		return nil
	}
	release, ok := heavy.tryAcquire()
	if !ok {
		return errCalendarBusy
	}
	defer release()

	f, err := computeCalendarYear(ctx, year)
	if err != nil {
		return err
	}
	if err := saveCalendarYear(f); err != nil {
		return err
	}
	log.Printf("calendar store: %d — %d событий, %.1f КБ", year, len(f.Events), fileSizeKB(calendarYearPath(year)))
	return nil
}

func fileSizeKB(path string) float64 {
	if st, err := os.Stat(path); err == nil {
		return float64(st.Size()) / 1024
	}
	return 0
}

// startCalendarStore — фон: при старте и раз в сутки держим текущий и
// следующий год. Если слот занят, повторяем через 10 минут.
func startCalendarStore() {
	go func() {
		for {
			now := time.Now()
			busy := false
			for _, year := range []int{now.Year(), now.Year() + 1} {
				err := ensureCalendarYear(context.Background(), year)
				switch {
				case err == nil:
				case errors.Is(err, errCalendarBusy):
					busy = true
				default:
					log.Printf("calendar store: %d: %v", year, err)
				}
			}
			if busy {
				time.Sleep(10 * time.Minute)
			} else {
				time.Sleep(24 * time.Hour)
			}
		}
	}()
}

// yearsBetween — годы, которые покрывает диапазон (для окна на стыке лет).
func yearsBetween(from, to time.Time) []int {
	y1, y2 := from.UTC().Year(), to.UTC().Year()
	if y2 < y1 {
		y1, y2 = y2, y1
	}
	out := make([]int, 0, y2-y1+1)
	for y := y1; y <= y2; y++ {
		out = append(out, y)
	}
	return out
}

// calendarEventsForRange достаёт события за диапазон [from, to] из годовых
// файлов. Ошибка — хотя бы одного года нет или версия устарела (тогда
// вызывающий считает на месте).
func calendarEventsForRange(years []int, from, to time.Time) ([]astro.CalendarEvent, error) {
	from, to = from.UTC(), to.UTC()
	var out []astro.CalendarEvent
	seen := make(map[string]bool)
	for _, year := range years {
		f, err := loadCalendarYear(year)
		if err != nil {
			return nil, err
		}
		for _, e := range f.Events {
			t, err := time.ParseInLocation("2006-01-02T15:04", e.Date, time.UTC)
			if err != nil {
				continue
			}
			if t.Before(from) || t.After(to) {
				continue
			}
			key := calendarEventKey(e)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, e)
		}
	}
	return out, nil
}

func calendarEventKey(e astro.CalendarEvent) string {
	return strings.Join([]string{
		e.Date, e.Type, strings.Join(e.Planets, ","), e.Aspect,
		fmt.Sprint(e.Degrees), fmt.Sprint(e.Sign), e.ChangeSign, e.VoidEnd,
	}, "|")
}

// formatCalendarEvents переводит UTC-строки в пояс запроса. Даты в файле
// с точностью до минуты — как их и отдаёт API.
func formatCalendarEvents(events []astro.CalendarEvent, loc *time.Location) {
	for i := range events {
		events[i].Date = formatStoreTime(events[i].Date, loc)
		events[i].ChangeSign = formatStoreTime(events[i].ChangeSign, loc)
		events[i].VoidEnd = formatStoreTime(events[i].VoidEnd, loc)
	}
}

func formatStoreTime(s string, loc *time.Location) string {
	if s == "" {
		return s
	}
	if loc == nil {
		loc = time.UTC
	}
	t, err := time.ParseInLocation("2006-01-02T15:04", s, time.UTC)
	if err != nil {
		return s
	}
	return t.In(loc).Format("2006-01-02T15:04")
}
