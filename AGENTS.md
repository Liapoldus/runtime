# AGENTS.md — Runtime

Репозиторий владеет Runtime-группами, WASM artifacts, контрактами команд,
host-функциями и собственными peer-методами. Не добавлять product behavior в
Core, Plugin SDK или `pluginprotocol`.

Соблюдать четыре production-слоя: `internal/domain/{models,interfaces}`,
плоский `internal/application`, тематические `internal/infrastructure` и
`internal/presentation`. Composition размещать в `cmd/`. Схемы и публичные
коды ошибок версионировать в `contracts/v1/`.

Писать TypeScript/Vitest тесты в `tests/` до реализации. Не добавлять Go
`*_test.go` в production-пакеты. Перед передачей работы выполнить весь Vitest,
`go build ./...` и `go vet ./...`; отличать работающую функцию от одного лишь
описанного контракта.

Не давать WASM прямой доступ к сети, файловой системе, WASI или неограниченной
памяти. Не логировать секреты и payloads. Сохранять чужие незакоммиченные
файлы. Не делать push, tag и публикацию без запроса пользователя.
