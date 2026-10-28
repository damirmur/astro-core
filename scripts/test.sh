#!/usr/bin/env bash
# Быстрые проверки сервиса: формат, go vet, сборка, тесты.
# CGO-переменные заданы на всякий случай — astro-core собирается с Swiss Ephemeris.
#
#   ./scripts/test.sh          формат + vet + сборка + go test
#   ./scripts/test.sh -short   только формат и vet
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

export CGO_CFLAGS="${CGO_CFLAGS:--I/home/dmyr/swisseph_build/include}"
export CGO_LDFLAGS="${CGO_LDFLAGS:--L/home/dmyr/swisseph_build/lib -lswe -lm}"

name=$(basename "$PWD")
rc=0
fail() { printf 'ПРОВАЛ: %s\n' "$1"; rc=1; }

unformatted=$(gofmt -l . 2>/dev/null | grep -v '^internal/buddy/.*/static/' || true)
if [ -n "$unformatted" ]; then
  printf 'не отформатировано (gofmt -w):\n%s\n' "$unformatted"
  fail "gofmt"
else
  printf '%s: ok gofmt\n' "$name"
fi

if go vet ./... 2>&1 | tee /tmp/"$name"-vet.log | grep -q .; then
  fail "go vet (см. /tmp/$name-vet.log)"
else
  printf '%s: ok go vet\n' "$name"
fi

if [ "${1:-}" = "-short" ]; then
  exit "$rc"
fi

if go build -o /tmp/"$name"-test-build/ ./... 2>/tmp/"$name"-build.log; then
  printf '%s: ok сборка\n' "$name"
else
  cat /tmp/"$name"-build.log
  fail "сборка"
fi

if go test ./... -count=1 -coverprofile=/tmp/"$name"-cover.out 2>&1 | tail -20; then
  if [ -f /tmp/"$name"-cover.out ]; then
    printf '%s: покрытие %s\n' "$name" "$(go tool cover -func=/tmp/"$name"-cover.out 2>/dev/null | tail -1)"
  fi
else
  fail "go test"
fi

exit "$rc"
