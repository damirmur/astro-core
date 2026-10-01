#!/usr/bin/env bash
# Дифференциальные тесты astro-core :8084 против монолита :8080.
# Сравниваются ТЕЛА ответов (X-Cache игнорируется: кэши разные).
set -u
NEW="${NEW:-http://127.0.0.1:8084}"
OLD="${OLD:-http://127.0.0.1:8080}"
FAIL=0

diff_ep() { # diff_ep <имя> <query-path>
  local name="$1" ep="$2"
  curl -s -m 60 -o /tmp/diff_new "$NEW$ep"
  curl -s -m 60 -o /tmp/diff_old "$OLD$ep"
  if cmp -s /tmp/diff_new /tmp/diff_old; then
    echo "PASS $name (байтов: $(wc -c < /tmp/diff_new))"
  elif [ "$name" = "natal-period" ] && python3 -c "
import json,sys
n=json.load(open('/tmp/diff_new')); o=json.load(open('/tmp/diff_old'))
n['data']['aspects']=sorted([json.dumps(a,sort_keys=True) for a in n['data']['aspects']])
o['data']['aspects']=sorted([json.dumps(a,sort_keys=True) for a in o['data']['aspects']])
sys.exit(0 if n==o else 1)"; then
    echo "PASS $name (семантически равен: порядок aspects случаен в исходнике)"
  else
    echo "FAIL $name: тела различаются"
    FAIL=1
  fi
}

echo "== astro-core diff: $NEW vs $OLD =="
if [ "$(curl -s -m 8 -o /dev/null -w "%{http_code}" "$OLD/healthz")" != 200 ]; then
  echo "SKIP byte-diff: монолит $OLD остановлен (дифференциал заморожен на 8/8 от 01.10.2026)"
else
diff_ep "natal"          "/api/v1/natal?city=Moscow&date=1990-06-15&time=12:00"
diff_ep "synastry"       "/api/v1/synastry?city1=Moscow&date1=1990-06-15&time1=12:00&city2=London&date2=1985-03-20&time2=08:00"
diff_ep "period"         "/api/v1/period?start=2026-01-01%2000:00&end=2026-01-03%2000:00&city=Moscow"
diff_ep "natal-period"   "/api/v1/natal-period?city=Moscow&date=1990-06-15&time=12:00&start=2026-09-01%2000:00&end=2026-09-03%2000:00&max_orb=1.5"
diff_ep "returns"        "/api/v1/returns?city=Moscow&date=1990-06-15&time=12:00&ref_date=2026-09-27"
diff_ep "calendar"       "/api/v1/calendar?year=2026&month=1&city=Moscow"
diff_ep "daily"          "/api/daily"
diff_ep "translate"      "/api/translate?q=%D0%9F%D1%80%D0%B8%D0%B2%D0%B5%D1%82&tl=en"
fi

echo "== только astro-core (в монолите нет) =="
code=$(curl -s -m 15 -o /tmp/diff_geo "$NEW/api/v1/geo?city=Moscow" -w "%{http_code}")
tz=$(python3 -c "import json; print(json.load(open('/tmp/diff_geo')).get('timezone',''))" 2>/dev/null)
if [ "$code" = 200 ] && [ "$tz" = "Europe/Moscow" ]; then
  echo "PASS geo (timezone Europe/Moscow)"
else
  echo "FAIL geo: код $code, tz $tz"
  FAIL=1
fi

if [ "$FAIL" = 0 ]; then echo "ВСЕ ПРОВЕРКИ ПРОШЛИ"; else echo "ЕСТЬ ОШИБКИ"; fi
exit "$FAIL"
