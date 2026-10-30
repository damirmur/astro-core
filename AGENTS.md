# AGENTS.md — astro-core (:8084)

Астрологические расчёты на Swiss Ephemeris, календарь, ежедневный прогноз, геокодер и
часовые пояса. **Единственный сервис с CGO.**

Всё, что пишет человек (комментарии, лог, тексты ошибок), — **по-русски**.

## Маршруты

| Путь | Файл |
|---|---|
| `GET /api/v1/natal`, `synastry`, `period`, `natal-period`, `returns`, `calendar` | `astro.go` |
| `GET /api/v1/geo?city=` → `{ok, city, timezone, lat, lon}` | `geo.go` |
| `GET /api/daily` (отдаёт `html/data/daily.json`) | `daily_file.go` |
| `GET /api/translate` (прокси Google Translate с кэшем) | `main.go` |
| `GET /healthz` | `main.go` |

## Сборка, проверка, деплой

```bash
./scripts/test.sh        # gofmt + vet + сборка + go test -cover, CGO-переменные уже внутри
go build -o /tmp/astro-core-new .
install -m 755 /tmp/astro-core-new /home/dmyr/astro-core/astro-core
sudo /usr/bin/systemctl restart astro-core.service
curl -s http://127.0.0.1:8084/healthz
```

CGO: `CGO_CFLAGS=-I/home/dmyr/swisseph_build/include`,
`CGO_LDFLAGS=-L/home/dmyr/swisseph_build/lib -lswe -lm`; в `go.mod` — `replace` на
`/home/dmyr/swisseph_build`. Голый `go build` без переменных падает.

Юнит — `astro-core.service`, env — `/home/dmyr/astro-core/*.env` (600).

## Env

`ASTRO_PORT`, `EPHE_PATH` (эфемериды, по умолчанию `ephe` рядом с бинарём),
`GEO_INDEX_PATH` (`geo/cities.idx`), `DAILY_LLM_MODELS`, `LLM_GATEWAY_URL`/`LLM_PROXY_KEY`
(:8086 для ИИ ежедневного прогноза), `NOTIFY_KEY`.

## Фоновый расчёт прогноза

`tasks_calendar.go`, раннер `startTaskRunner`: задача `daily_calendar` из `tasks.json`
(читается из `/var/www/astro3d.ru`), окно `[вчера, +3 дня]` по Москве, пишет
`/var/www/astro3d.ru/html/data/daily.json`; стейт — `output_data/tasks_state.json`.
Ручной прогон — `RUN_TASKS_ONCE=1`. ИИ — HTTP в llm-1c, модели из `DAILY_LLM_MODELS`
(дальше `params.llm_models` → автопоиск `*free*`).

## Геокодер

Локальный дамп GeoNames `geo/cities.idx` (28 МБ, ~171 тыс. НП) читается в память при старте.
Поиск бинарный по нормализованному имени, выбирается самый населённый; смещение считается
**на дату** с историческими правилами DST (`tzOffsetAt`). Сеть (Nominatim) — только фолбэк
для мест вне дампа, с кэшем 30 дней и проверкой `nameMatchesCity`.
Обновление индекса — таймер `a3d-geo-update.timer` (1-е число, 05:10 МСК).

## Ловушки

- `GetGeoData` может вернуть ошибку → город не распознан: отвечать `400` с JSON, а не ронять
  хендлер (был `nil deref` на `gd.City` в `/api/v1/calendar`).
- `houses=none` (космограмма) — без `cusps` и номеров домов; проверяй на всех расчётных ручках.
- Аспекты в ответе — множество, порядок случайный: сравнивай множествами (`diff.sh`),
  а не построчно.
- Бинарь прода собран из «грязного» дерева: `vcs.revision` — база сборки, а не точный код.
  Сверка с `main` — эталонами `astro3d-e2e` (`TestGoldenAstro`).

## Где документация

`docs/agents/task-runner.md` (прогноз), `docs/api/astro.md` (контракты), `docs/agents/services.md`.
