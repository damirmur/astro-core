# astro-core

Расчётный сервис [astro3d.ru](https://astro3d.ru): астрологические расчёты на
Swiss Ephemeris, календарь событий, ежедневный прогноз, локальный геокодер и
часовые пояса. HTTP-слой над библиотекой
[`swisseph_build`](https://github.com/damirmur/swisseph_build); слушает только
loopback, наружу отдаёт nginx.

## Маршруты

| Путь | Что отдаёт |
|---|---|
| `GET /api/v1/natal`, `synastry`, `period`, `natal-period`, `returns`, `calendar` | расчёты карт и периодов |
| `GET /api/v1/geo?city=` | `{ok, city, timezone, lat, lon}` |
| `GET /api/daily` | готовый текст дня (`daily.json`) |
| `GET /api/translate` | прокси перевода с кэшем |
| `GET /healthz` | проверка живости |

## Сборка

Нужны Go 1.25+, `gcc`, `ar` и файлы эфемерид Swiss Ephemeris.

```bash
git clone https://github.com/damirmur/swisseph_build ../swisseph_build
(cd ../swisseph_build && ./scripts/build-libswe.sh)

go mod edit -replace github.com/damirmur/swisseph_build=../swisseph_build
./scripts/test.sh                      # gofmt + vet + сборка + тесты
go build -o astro-core .
```

Запуск: `ASTRO_PORT=127.0.0.1:8084 EPHE_PATH=/path/to/ephe ./astro-core`.

Переменные окружения: `ASTRO_PORT`, `EPHE_PATH`, `GEO_INDEX_PATH`,
`DAILY_LLM_MODELS`, `LLM_GATEWAY_URL` / `LLM_PROXY_KEY`, `NOTIFY_KEY`.
Без `EPHE_PATH` Swiss Ephemeris молча уходит в Moshier — расчёт работает, но
времена событий «плывут».

## Лицензия

**AGPL-3.0** — см. [`LICENSE`](LICENSE). Сервис использует Swiss Ephemeris
(Astrodienst AG) под AGPL-3.0; копирайты, версия upstream, Corresponding Source
и source offer — в [`NOTICE`](NOTICE).
