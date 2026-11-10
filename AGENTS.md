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
install -m 755 /tmp/astro-core-new /home/dmyr/app/services/astro-core/astro-core
sudo /usr/bin/systemctl restart astro-core.service
curl -s http://127.0.0.1:8084/healthz
```

CGO: `CGO_CFLAGS=-I/home/dmyr/app/src/swisseph_build/include`,
`CGO_LDFLAGS=-L/home/dmyr/app/src/swisseph_build/lib -lswe -lm`; в `go.mod` — `replace` на
`/home/dmyr/app/src/swisseph_build`. Голый `go build` без переменных падает.

Юнит — `astro-core.service`, env — `/home/dmyr/app/services/astro-core/*.env` (600).

## Env

`ASTRO_PORT`, `EPHE_PATH` (сейчас `/home/dmyr/app/data/ephe`),
`GEO_INDEX_PATH` (`/home/dmyr/app/data/geo/cities.idx`), `CALENDAR_STORE_DIR`
(по умолчанию `html/data/calendar`), `DAILY_LLM_MODELS`,
`LLM_GATEWAY_URL`/`LLM_PROXY_KEY` (:8086 для ИИ ежедневного прогноза), `NOTIFY_KEY`.

**Без `EPHE_PATH`** Swiss Ephemeris молча уходит в Moshier: расчёты работают, но времена
событий «плывут» на секунды-минуты и падают эталоны `astro3d-e2e`. В логе при старте
должен быть реальный путь, а не голое `ephe`.

## Фоновый расчёт прогноза

`tasks_calendar.go`, раннер `startTaskRunner`: задача `daily_calendar` из `tasks.json`
(читается из `/home/dmyr/app/astro3d.ru`), окно `[вчера, +3 дня]` по Москве, пишет
`/home/dmyr/app/astro3d.ru/html/data/daily.json`; стейт — `output_data/tasks_state.json`.
Ручной прогон — `RUN_TASKS_ONCE=1`. ИИ — HTTP в llm-1c, модели из `DAILY_LLM_MODELS`
(дальше `params.llm_models` → автопоиск `*free*`).

## Календарь и годовое хранилище

`calendar_store.go`: `html/data/calendar/<year>.json` (gitignored, `CALENDAR_STORE_DIR`)
— сырые события календаря в UTC, версия `calendar/v1;se=2.10.03`. Заполняется при
старте и раз в сутки для текущего и следующего года (1 января следующий появляется
сам), тяжёлый слот через `tryAcquire`. `/api/v1/calendar` читает файл, фильтрует
по диапазону и форматирует даты в поясе города; нет файла или версия устарела —
старый расчёт на месте (heavy gate). `calendarStoreVersion` бампать при правках
`pkg/astro/calendar*.go` или обновлении SE. Подробности — `docs/agents/calendar.md`.

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

## Лицензия

- Репозиторий — **AGPL-3.0** (`LICENSE`): сервис линкует `swisseph_build`, внутри которого
  Swiss Ephemeris под AGPL-3.0. Вариант Professional не выбран, поэтому расчётный контур
  открыт; `site-cloud` и фронтенд — отдельные программы за HTTP и остаются проприетарными.
- `NOTICE` — копирайты Astrodienst, версия SE, Corresponding Source, сборка и source offer §13.
  Репозиторий публичный: <https://github.com/damirmur/astro-core>.
- Секреты в репозиторий не попадают: env и ключи — в `/home/dmyr/app/services/astro-core/*.env` (600).
  Новые зависимости от SE-кода должны оставаться AGPL-совместимыми.
