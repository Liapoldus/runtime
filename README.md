# Runtime

Отдельный Liapoldus plugin для WASM-команд и независимо развёртываемых групп.
Это прототип для общего v2, не часть приёмки v1 и не production release. Цель и текущие разрывы
описаны в [документации](docs/site/plugins/runtime.md) и [TODO](TODO.md).

Четыре слоя сохраняются: application/config отвечает за decode и active
snapshot, application/module — за artifacts и invoke, presentation/admin —
за SDK administration, presentation/peerplugin — за peer-вызовы. Native Go
tests находятся рядом с кодом; tests/ содержит contract consumers и E2E.

Публичные contracts/v1/*.json генерируются из contracts/*.go и
contracts/schema/. Runtime использует кодовые определения. После изменения
источников: `make generate`; `make check-generated` проверяет весь набор
артефактов и exact bytes без перезаписи.

Независимые проверки: `npm ci`, `GOWORK=off GOFLAGS=-p=1 make check`,
`make check-race`. Root .golangci.yml и typed ESLint проверяют весь собственный
Go/TS код, включая fixtures и tests. Make устанавливает golangci-lint v2.12.2;
полный gate включает generation, lint, typecheck, tests, vet и build.
Blocking workflow: .github/workflows/check.yml (Linux/macOS).
Hosted CI и межрепозиторная v2 приёмка отмечаются отдельно в TODO.
