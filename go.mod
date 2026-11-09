module github.com/damirmur/astro-core

go 1.25

require (
	github.com/damirmur/swisseph_build v0.0.0
	github.com/joho/godotenv v1.5.1
)

// Локальная рабочая копия swisseph_build. Вне этого сервера путь нужно заменить:
//   go mod edit -replace github.com/damirmur/swisseph_build=../swisseph_build
// Подробности сборки — в NOTICE.
replace github.com/damirmur/swisseph_build => /home/dmyr/app/src/swisseph_build
