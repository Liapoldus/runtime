# Runtime — задачи общего v2

## Cleanup/refactoring — 2026-10-09

Четыре слоя сохранены; application разделён на config/ и module/, SDK admin
adapter перенесён в presentation/admin/modules.go. Импорты composition,
adapters и fixtures обновлены без compatibility aliases.
Native definitions tests перенесены из tests/unit/ к владельцам. Configuration
decode/store и artifact admission проверяются native tests рядом с кодом;
три прежних TS wrappers и их Go probes заменены после успешного native прогона.
Настоящие peer/lifecycle/sandbox E2E остаются в tests/.

ABI, metadata, schemas и error/action artifacts теперь code-owned в contracts/
и contracts/schema/. JSON loaders/embed/cache удалены. make generate
детерминированно публикует весь contracts/v1/; make check-generated проверяет
exact bytes и отсутствие лишних artifacts. Семантика всех 13 документов
сверена с исходным WIP: значения, поля и limits сохранены; изменился формат
canonical JSON, следовательно документные digests требуется получать из новых bytes.

Root .golangci.yml: pinned v2.12.2, correctness/security/context/resource checks,
без baseline/global exclusions. Typed ESLint включает unsafe/promise checks
для handwritten TS/tests. make check последовательно проверяет generation,
Go/TS lint, typecheck, native tests, полный Vitest, vet и build с GOWORK=off,
GOFLAGS=-p=1. Linux/macOS CI блокируется при ошибке любого шага, включая race.
Первый полный локальный gate до замены unit wrappers: PASS, 13 TS suites / 62 tests.
Окончательный локальный gate после переноса unit tests — PASS (macOS ARM64,
Go 1.26.0): `GOWORK=off GOFLAGS=-p=1 make check`. Generation/parity, pinned
golangci-lint (0 issues), typed ESLint, typecheck, `go test ./... -count=1`,
полный Vitest (10 files / 45 tests), `go vet ./...` и `go build ./...` прошли.
Native race — PASS: `GOWORK=off GOFLAGS=-p=1 make check-race` (native step).
Затем весь E2E suite отдельно прошёл с `GOWORK=off GOFLAGS='-p=1 -race'
npm test -- --maxWorkers=1`: 10 files / 45 tests, включая race-enabled Runtime
child processes. Оба шага включены в итоговый `make check-race` и CI.
`git diff --check` — PASS. Это локальные результаты, не hosted acceptance.
Повтор `npm ci`, ESLint/typecheck и `make check-generated` после проверки
lockfile — PASS; npm сообщает 0 vulnerabilities.

Изменённые пути cleanup:

| Область | Пути |
| --- | --- |
| Application | `internal/application/config/{decode,limits,store}.go` и native tests; `internal/application/module/{invoke,artifacts}.go` и native tests |
| Adapters/composition | `internal/presentation/admin/modules.go`, `internal/presentation/peerplugin/handler.go`, `cmd/runtime/main.go`, `internal/infrastructure/{artifacts,wasm}/`, package comments/domain native tests |
| Contracts | `contracts/{abi,actions,codes,invoke,documents,definitions_test}.go`, `contracts/schema/{admin,invoke,module,settings}.go`, весь generated `contracts/v1/`, `cmd/contracts/main.go` |
| E2E | Typed `tests/*.ts`; обновлённые `tests/fixtures/{module-artifact-action,peer-invoke,runtime-generation,runtime-lifecycle,runtime-restart-reconnect,sandbox-adversarial,wasm-memory-limit,wasm-probe}/main.go` |
| Quality | `.golangci.yml`, `eslint.config.mjs`, `tsconfig.json`, `Makefile`, `.github/workflows/check.yml`, `package{,-lock}.json`, `.gitignore` |
| Docs/instructions | `AGENTS.md`, `README.md`, `TODO.md`, `docs/site/plugins/runtime.md`, `docs/blockers-runtime-v2.md` |

Удалены старые application flat paths, restplugin path, contracts/assets.go
и переименованный contracts/module_actions.go. Unit wrappers
`tests/{artifact,configuration,configuration-store}.test.ts` и probes
`tests/fixtures/{artifact-probe,config-probe,configuration-store-probe}` заменены
native coverage; `tests/unit/definitions_test.go` разделён по владельцам.
Их функциональность сохранена в новых исходниках/tests; пустых пакетов нет.

OPEN: hosted CI/required branch checks не запускались; реальные Core Management/
reconciliation, release-compatible SDK, Domain delegated-scope/host ABI и
полная межрепозиторная sandbox acceptance не закрываются этим cleanup.
Другие репозитории не редактировались; commits/push/releases не выполнялись.

Проверка graceful peer drain — 2026-10-08 (macOS): целевой child-process
сценарий `tests/peer-invoke.test.ts` прошёл (14 тестов), полный suite после
изменения — 13 файлов / 62 теста; `GOWORK=off go test ./...`, `go build ./...`,
`go vet ./...` и `git diff --check` прошли. Это локальная Runtime-проверка,
не hosted CI и не Core/SDK rollout conformance.

Linux ARM64-проверка — 2026-10-08 (OrbStack): `npx vitest run tests
--maxWorkers=1` прошёл (13 файлов / 59 тестов), `go build ./...` и `go vet
./...` прошли на Go 1.26.8. Для первого adversarial fixture увеличен timeout
с Vitest default 5 s до 30 s: холодная компиляция дочернего Go fixture на
эмулируемой Linux ARM64-среде занимала около 7 s. Это платформенный локальный
gate, не hosted CI и не сквозная Core→SDK→Runtime приёмка.

Повторная локальная проверка текущего WIP — 2026-10-07: полный Vitest suite
прошёл (13 файлов / 59 тестов, включая `tests/sandbox-adversarial.test.ts`,
`tests/runtime-restart-reconnect.test.ts` и `tests/peer-invoke.test.ts`),
`go test ./...`, `go build ./...`, `go vet ./...` (в режиме workspace и с
`GOWORK=off`) и `git diff --check` также прошли. Сквозной
Core→SDK→Runtime→Server и полная sandbox/Domain conformance остаются открытыми;
этот результат их не закрывает.

Начальные локальные срезы сохранены как прототип. Все незакрытые пункты ниже
относятся к общему релизу Liapoldus v2 и не входят в приёмку v1. Версия
`contracts/v1` обозначает первую версию контракта самого плагина, а не этап
экосистемы.

Milestone платформы перед production integration: интегрировать Runtime с
совместимой published revision Plugin SDK. Текущий локальный SDK WIP уже
содержит self-registration/lease, peer directory и compatibility metadata, но
Core закреплён на опубликованном `v1.0.0`, где этих API нет; временный workspace
не заменяет согласованную dependency revision и чистый build. Для Runtime также
обязательны совместимость WASM ABI/artifact/settings/peer contracts и
production cohort gate. Отдельные Runtime-группы масштабирует оператор; Core не
запускает replicas и не интерпретирует WASM. Canary допускается только при
совместимых когортах и подтверждённом traffic weight; неизвестный результат
`runtime.invoke` не replay-ится.

- [ ] Gate платформы: lease expiry, новая incarnation, совместимость WASM ABI
  при rolling/canary, drain длинного вызова и отказ при несовместимом artifact.
  - [x] Runtime-owned shutdown slice: на SIGINT/SIGTERM fence новые peer-вызовы,
    сообщить `ready=false`, закрыть приём сессий и дождаться уже допущенных
    `runtime.invoke` в пределах `pluginShutdownGraceSeconds`. Child-process
    conformance доказывает, что новый вызов получает `503 not_ready`, а принятый
    bounded WASM-вызов завершается до выхода процесса:
    `tests/peer-invoke.test.ts`. Это не закрывает lease/incarnation/cohort gates.

- [x] Начальная строгая settings-схема и тестируемая проверка group/command/scope.
- [x] Product Manifest содержит единственный peer capability `runtime.invoke`; request/response/error schemas генерируются в `contracts/v1/`. Server HTTP adapter относится к v3; self-registration/lease остаются отдельным milestone.
- [x] Подключить строгий `config.DecodeConfiguration` к Plugin SDK REST Reload: процесс Runtime получает точные bytes указанного поколения по mTLS, проверяет SHA-256 именно этих bytes и соответствие schema version, валидирует документ до применения и одним in-memory pointer swap делает настройки активными. Некорректный или повреждённый candidate получает `applyRejected`; активный snapshot сохраняется. Сквозные проверки: `tests/runtime-lifecycle.test.ts`, `internal/application/config/store_test.go`.
- [x] Расширить Reload preflight до пары settings+WASM: до ACK открыть content-addressed artifact по `module.sha256`, ограниченно прочитать его, повторно сверить digest и ABI без исполнения start function, затем одной атомарной публикацией заменить in-memory settings+module snapshot. Неизменяемый artifact остаётся на persistent filesystem; после рестарта SDK lifecycle должен получить новый `Reload` и повторно pull-ить точную конфигурацию у Core. Child-process fixture доказывает missing/tampered/mismatched artifact rejection без изменения active pair и полную замену согласованной пары: `tests/runtime-generation.test.ts`.
- [x] Runtime restart/reconnect slice: Core-side Plugin SDK client против двух
  дочерних Runtime replicas с общим artifact-dir и fake Core, выключаемым на 503.
  Пока Core недоступен, exact-generation `Reload` чисто отклоняется
  (pending, ready=false) без падения процесса; после возврата Core та же пара
  поколение+digest применяется повторно без нового upload — каталог восстановлен
  из persistent filesystem. Evidence: `tests/runtime-restart-reconnect.test.ts`.
  Повторное объявление того же поколения идемпотентно
  (`alreadyActive`), следующее поколение применяется штатно. Это runtime-side
  срез; полный Core restart/reconciliation gate обеих сторон остаётся пунктом
  ниже.
- [ ] Закрыть сквозной Core serve → Plugin SDK → Runtime child-process lifecycle
  на рестартах обеих сторон. Core local WIP уже содержит replica registration,
  Reload fan-out и exact-generation reconciliation; непокрытый gate — реальный
  штатный startup/listener ordering, reconnect и восстановление operation до
  ACK без повторного исполнения product invoke.
- [x] Подключить Runtime Admin Surface к generic Plugin SDK `ArtifactAcceptor`: bounded multipart callback передаёт WASM в content-addressed store, который публикует только ABI-валидный модуль с совпавшим digest. Добавлены plugin-owned action/metadata/receipt contracts и каталог проверенных digest: `tests/module-artifact.test.ts`.
- [x] Проверить SDK→Runtime artifact transport на настоящем дочернем Runtime-процессе и mTLS: `PluginClient.ArtifactStream` загружает два модуля через multipart, получает receipts, `AdminAction` читает проверенный каталог, затем Reload активирует exact generations с этими digest. Evidence: `tests/runtime-lifecycle.test.ts`.
- [ ] Добавить Core Management API → Core `SDKAdminControl` → Runtime child-process E2E: доказать авторизацию Admin Surface, streaming без полной буферизации, Core request/artifact limits, cancellation и сохранение product-owned response. Текущий process fixture вызывает Plugin SDK Core-side client напрямую и не покрывает Core HTTP forwarding.
- [x] Изолированный вызов WASM через versioned JSON ABI с bounds из typed ABI definition; без WASI/host imports. `tests/wasm-execution.test.ts` дополнительно подтверждает, что trap в `alloc` или `invoke` возвращает `execution_failed`, отмена/deadline остаются отдельными исходами, а неверная арность результата — `invalid_abi`. Это локальный execution slice, не полная sandbox-приёмка.
- [x] Adversarial-проверка memory cap: fixture пытается `memory.grow` на 1024 pages сверх начальной страницы при contract ceiling 1024 и обращается за пределами оставшейся памяти. Вызов получает bounded `execution_failed`, generation/settings/module active snapshot остаётся неизменным. Код ошибки закреплён в `contracts/v1/wasm-errors.json`; проверка: `tests/wasm-memory-limit.test.ts`. Это не закрывает полную sandbox-приёмку.
- [x] Artifact admission проверяет WASM ABI до content-addressed publication: обязательные exports и signatures определены в `contracts/abi.go`, модули с import-ами отклоняются без запуска start-функции. Evidence: `internal/infrastructure/artifacts/store_test.go` покрывает отсутствие ABI exports, несовпадающую signature, host function import и положительную ABI-совместимую публикацию.
- [x] Sandbox adversarial conformance child-process: генератор hostile WASM (import-env/import-wasi, bogus длина/указатель, non-json return, infinite loop, alloc range, recursion) прогоняется через реальный `wasm-probe` и дочерний Runtime. Проверены все отказы: `invalid_abi`, `payload_too_large`, `invalid_json`, `execution_timeout` (deadline 5s), `execution_failed`, при этом executor/processor изменения не потребовались; граница входных данных в 1 MiB добита модулем с 16-page memory. Evidence: `tests/sandbox-adversarial.test.ts` (11 тестов). Это не закрывает полную sandbox-приёмку (host-функции/Domain остаются пунктом ниже).
- [ ] Host-вызовы Domain заблокированы текущим межплагинным product contract;
  не добавлять Runtime-local scope payload или второй API. Domain `ProductCaller`
  сопоставляет mTLS URI SAN ровно с одним фиксированным `Tenant/Site/Group`,
  тогда как Runtime command разрешает несколько `tenantSites`, а каждый
  `runtime.invoke` выбирает scope из входного запроса. Domain product request
  schemas не принимают tenant/site/group/delegation claim; передать выбранный
  Runtime scope в Domain и получить Domain-проверку tenant/site + OwnerGroup
  без изменения Domain-контракта сейчас нельзя. Дополнительно текущий WASM ABI
  запрещает все imported host functions. Возобновить slice после Domain-owned
  версии контракта, которая задаёт аутентифицированное per-call delegated scope
  для Runtime identity с проверкой entity OwnerGroup, и после согласования
  versioned Runtime WASM host-import ABI. До этого сохранить Runtime peer
  command/scope/entity ACL как отдельную границу, но не считать её Domain
  авторизацией.
- [x] Базовый bounded WASM deadline/cancellation: ABI deadline применяется к
  compile/instantiate/start/alloc/invoke, отмена до запуска и прерывание
  зависшей start function проверены настоящим Go fixture в
  `tests/wasm-execution.test.ts`; прерывание долгого peer-вызова проверено
  отдельным дочерним Runtime в `tests/peer-invoke.test.ts`. Эти проверки не
  закрывают будущую host-import приёмку.
- [ ] После согласования и реализации Domain host-import ABI расширить sandbox
  adversarial suite на cancellation/deadline каждого разрешённого host call,
  проверку tenant/site delegation и невозможность выхода из настроенных
  command/entity scopes. До этого не объявлять Runtime↔Domain интеграцию
  готовой.
- [x] Product-owned peer `runtime.invoke`: `internal/presentation/peerplugin`
  регистрирует единственный unary call-method через pluginprotocol, строго
  декодирует Server v1 request-envelope или голый invoke-request, проверяет
  caller identity через `callerAuthorizer` (AuthorizeStream закрыт), проводит
  масштабирование в `internal/application`. Command/scope/entity ACL, bounds
  compose, readiness fencing (not_ready до первого применённого поколения),
  bounded-execution cancellation/timeout и versioned error-контракт
  (`contracts/v1/errors-invoke.json`, `contracts/invoke.go`) покрыты сквозными
  peer-сценариями: `tests/peer-invoke.test.ts`; HTTP terminal adapter и
  routing остаются в зоне Server.
- [x] Scope/entity ACL на `runtime.invoke`: запрещённая tenant/site → 403
  `forbidden_scope`, entity вне allowlist команды → 403 `forbidden_entity`;
  подтверждено в `tests/peer-invoke.test.ts`. Host-вызовы Domain с fencing
  остаются открытым пунктом ниже.
- [ ] Domain host access с tenant/site fencing и command/entity ACL остаётся
  заблокирован внешним контрактом, описанным выше; не считать текущие
  `runtime.invoke` ACL эквивалентом Domain-проверки.
- [ ] Независимое масштабирование групп, digest parity и readiness fencing.
- [ ] Сквозная HTTP-маршрутизация Server → Runtime `runtime.invoke` не входит в
  v2, поскольку потребовала бы изменений Server plugin. Перенесена в v3; в v2
  проверять только Runtime-owned peer/WASM путь и общие Core lifecycle gates.
- [x] Базовый ручной запуск через CLI flags, Plugin SDK REST Reload/readiness, exact-generation config pull и mTLS. Это не включает self-registration/lease, replica discovery или rollout coordination.
- [ ] Сквозные E2E (Core→SDK→Runtime→Server) и hosted Linux CI; не объявлять плагин готовым до их прохождения. Sandbox adversarial suite прошёл на macOS и Linux ARM64 (`tests/sandbox-adversarial.test.ts`, 11 тестов); реальный Core restart/reconciliation gate остаётся открытым. Server HTTP route относится к v3.
