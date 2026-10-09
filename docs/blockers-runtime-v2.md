# Runtime v2 — blockers для общего релиза (hand-off)

Состояние на 2026-10-08. Локальный Runtime срез готов и зелёный: 13 файлов /
59 Vitest тестов (включая `tests/sandbox-adversarial.test.ts`,
`tests/runtime-restart-reconnect.test.ts` и `tests/peer-invoke.test.ts`),
`go test ./...`, `go build ./...`, `go vet ./...` (workspace и `GOWORK=off`),
`git diff --check`.
Перечисленное ниже НЕ заблокировано определённой ошибкой в Runtime; это внешние
зависимости и гейты, которые закрывает координирующий Codex вне этого репозитория.

## 1. Slice 2 — узкие host-функции и Domain access
- Что: `runtime.invoke` исполняет pure WASM с JSON ABI; host-imports запрещены и
  отклоняются при admission (`internal/infrastructure/artifacts/store_test.go`). Domain host-вызовы с
  tenant/site fencing и command entity ACL не реализованы и не могут быть
  безопасно добавлены только в Runtime при текущем Domain contract. Domain
  `ProductCaller` закрепляет один scope за mTLS URI SAN (`Identity`, `Tenant`,
  `Site`, `Group`), в то время как Runtime command допускает несколько
  `tenantSites` и конкретный scope приходит в `runtime.invoke`. Domain product
  request schemas намеренно запрещают клиентские tenant/site/group поля и не
  определяют доверенную per-call delegation. Поэтому Runtime не может передать
  выбранный scope так, чтобы Domain самостоятельно повторно проверил tenant,
  site и OwnerGroup. Текущий WASM ABI также запрещает любые imported host
  functions. Не добавлять Runtime-only envelope/endpoint или ослабление Domain
  fencing; для продолжения нужны Domain-owned delegated-scope contract и
  согласованный versioned Runtime host-import ABI.
- Блокирует: полную sandbox-приёмку и Domain conformance; пункт «Host-вызовы
  Domain» в `TODO.md`.
- Кому: Domain владеет делегированным scope/ACL контрактом; Runtime владеет
  host-import ABI и его enforcement. Core, Plugin SDK и pluginprotocol менять
  не требуется, пока не доказана необходимость их generic API.

## 2. Совместимая published revision Plugin SDK — replica lifecycle и peer directory
- Что: закреплённый в `core/go.mod` published `plugin-sdk v1.0.0` не содержит
  self-registration/lease, replica discovery и service/peer directory. Текущий
  локальный dirty SDK checkout уже содержит соответствующий WIP и проходит свои
  проверки, но это недоступно Runtime/Core при чистой сборке по опубликованной
  версии. `runtime.invoke` пока включён явно через `--peer-listen`,
  `--peer-identity`, `--peer-allowed-caller` и статический CA/CRL.
- Блокирует: release-compatible Core/SDK build и подтверждение registration,
  lease, cohorts, canary, ready/drain groups и независимого scale behavior в
  одном опубликованном source/dependency set.
- Кому: Plugin SDK + Core owners; не дублировать SDK API в Runtime.

## 3. Core Management API → replica reconciliation
- Что: Core local WIP уже реализует registration/renewal, directory, Reload
  fan-out и exact-generation reconciliation, но текущие Runtime fixtures не
  проходят через штатный `core serve` startup, Management API forwarding и
  полную авторизацию Admin Surface.
- Блокирует: clean cross-process Runtime lifecycle/artifact E2E с реальными
  Core и Runtime binaries, включая полный restart/reconnect обеих сторон и
  durable operation ACK. Runtime-side restart/reconnect проверен локально против
  fake Core в `tests/runtime-restart-reconnect.test.ts` (доступность Core на
  старте реплики, 503-отказ exact-generation pull, восстановление каталога из
  persistent filesystem, `alreadyActive` идемпотентность, прогресс поколений).
- Кому: Core/Runtime integration; published module compatibility и hosted gate
  подтверждаются отдельно.

## 4. Отложено до v3: Server HTTP terminal adapter
- Что: Runtime отдаёт Server v1 `httpRequestPayload` envelope как peer-метод
  `runtime.invoke` и возвращает `httpResponseAction`. Terminal adapter на стороне
  Server (HTTP → peer, ответ → HTTP, semantics/replay) в этом репо не пишется.
- Это не blocker v2: Server repository и его HTTP dispatch остаются неизменённым
  v1 regression baseline. Сквозная публичная HTTP-маршрутизация требует
  Server-owned изменения и открывается только в v3 после пересмотра
  envelope/cookie/replay semantics.

## 5. Платформенный гейт (Linux) и полный adversarial-сиггсут
- Что: разработка велась на macOS; sandbox adversarial suite и Core
  child-process E2E пройдены локально на macOS (`tests/sandbox-adversarial.test.ts`,
  11 тестов: import-env/import-wasi, bogus длина/указатель, non-json, infinite
  loop, alloc range, recursion, граница 1 MiB, panic-free). Linux CI прогон
  (тот же suite + load + отказ при несовместимом artifact при rolling/canary)
  остаётся открытым.
- Блокирует: заявление о готовности плагина.
- Кому: CI/Linux gate; Runtime код сам по себе платформенно-нейтральный
  (чистый Go + wazero).

## Что уже локально закрыто (для сведения)
- settings schema + atomic pointer-swap activate, digest/ABI preflight, pair
  settings+WASM (`runtime-lifecycle`, `runtime-generation`, `configuration-store`).
- ArtifactAcceptor с content-addressed store и ABI-валидацией, catalog
  (`module-artifact`, `artifact`).
- Изолированный bounded WASM execution: trap, memory cap, timeout, cancel,
  invalid ABI (`wasm-execution`, `wasm-memory-limit`).
- Sandbox adversarial conformance через дочерние процессы (host-function
  imports, malformed ABI, non-json, infinite loop deadline 5s, alloc range,
  recursion trap, 1 MiB input boundary, panic-free): `sandbox-adversarial`.
- Runtime restart/reconnect против fake Core (503-толерантность на старте,
  exact-generation resume без re-upload, artifact persistence, `alreadyActive`
  идемпотентность): `runtime-restart-reconnect`.
- Peer `runtime.invoke` product-surface: caller identity, scope/entity ACL,
  readiness fencing, bounds compose, envelope, versioned errors
  (`peer-invoke`, `invoke-contract`).
- Runtime peer ACL пока ограничивает сам входящий invoke, но не предоставляет
  Domain-side authorization и не заменяет его.
- Runtime process shutdown теперь fenced/drained: на SIGINT/SIGTERM readiness
  становится false, новые peer-вызовы получают `not_ready`, а уже принятый
  вызов ожидается в пределах Plugin SDK shutdown grace. Реальный `cmd/runtime`
  child-process сценарий входит в `tests/peer-invoke.test.ts`. Это локальный
  Runtime gate; он не закрывает Core rollout, replica lease или hosted CI.

Планируемые действия после снятия блокеров перечислены в `TODO.md`.
