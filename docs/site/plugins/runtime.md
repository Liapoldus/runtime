# Runtime — целевой плагин v2

Платформенный milestone v2 добавляет Plugin SDK self-registration/lease для
каждой replica Runtime-группы, SemVer/digest и диапазоны совместимости WASM ABI,
artifact format, settings и peer contracts. Число replicas определяет
оператор, не Core. Две rollout-когорты допускаются только при совместимости
и подтверждённых traffic weights; неизвестный результат команды не replay-ится.

Runtime — отдельный плагин исполнения объявленных WASM-команд. Core хранит
точные байты JSON-конфигурации, вызывает Plugin SDK REST `Reload`; экземпляр
Runtime сам получает точные bytes поколения, сверяет SHA-256 документа и
schema version, валидирует его и атомарно активирует настройки в памяти.
[Нормативная схема настроек](https://github.com/Liapoldus/runtime/blob/main/contracts/v1/settings.schema.json)
принадлежит этому репозиторию.

Runtime mappings, ABI и metadata принадлежат Go definitions в
`contracts/{abi,actions,codes,invoke,documents}.go`; JSON schemas — в
`contracts/schema/`. `make generate` публикует весь набор `contracts/v1/*.json`,
а `make check-generated` проверяет воспроизводимость exact bytes. Runtime
использует определения из кода. Wire values, limits и execution semantics
сохранены; canonical JSON formatting меняет document digests, которые SDK
получает из актуальных bytes.

Application разделён на `config/` (decode и атомарный snapshot) и `module/`
(artifact operations и invoke). SDK admin adapter находится в
`internal/presentation/admin/`, peer adapter — в `peerplugin/`.
Native Go tests находятся рядом с владельцами; `tests/` сохраняет
child-process E2E. Полный независимый quality gate: `GOWORK=off GOFLAGS=-p=1
make check` и `make check-race`; hosted Linux/macOS gate остаётся отдельным
подтверждением.

Каждая Runtime-группа — отдельная единица развёртывания и масштабирования.
Группа имеет явный `groupId`, ссылку на Domain instance, immutable WASM
artifact digest и список команд. Каждая команда допускает только перечисленные
tenant/site scopes и сущности; host передаёт доверенный scope из caller context,
а не из произвольного WASM payload.

WASM запускается через wazero с [версионированным JSON input/output ABI](https://github.com/Liapoldus/runtime/blob/main/contracts/v1/wasm-abi.json):
module экспортирует `memory`, `alloc(length) → pointer` и
`invoke(pointer, length) → i64`, где старшие 32 бита результата содержат
длину, младшие — адрес JSON-ответа. Вызов ограничен временем, объёмом
input/output и памятью значениями из ABI contract. Исполнение идёт без
WASI, прямых network и filesystem функций. Узкие host-функции проверяют права
перед Domain или другими разрешёнными peer-вызовами. `runtime.invoke` —
product-owned peer method. Публичная Server HTTP-маршрутизация этого метода
отложена до v3: в v2 Server остаётся неизменённым v1 regression baseline.
Одна транзакция ограничена одним Domain batch, не всей цепочкой между плагинами.

`maxMemoryPages` — жёсткий предел линейной памяти модуля: попытка превысить
его не может расширить WASM instance за установленную границу. Если модуль
после отказа роста обращается за пределы оставшейся памяти и traps, Runtime
возвращает только bounded `execution_failed`; динамические сообщения и детали
trap не входят в ответ. Код описан в [каталоге ошибок](https://github.com/Liapoldus/runtime/blob/main/contracts/v1/wasm-errors.json).
Adversarial fixture проверяет попытку вырасти на 1024 pages сверх исходной
страницы при contract cap 1024 и сохранение активного поколения:
[`tests/wasm-memory-limit.test.ts`](https://github.com/Liapoldus/runtime/blob/main/tests/wasm-memory-limit.test.ts).

Лимит `maxDurationMillis` начинается до компиляции модуля и охватывает
компиляцию, инстанцирование (включая WASM start function) и ABI-вызовы.
Истечение этого общего deadline возвращает `execution_timeout`, в том числе
если зависает start function; ошибка структуры модуля без истечения deadline
остаётся `invalid_module`.
Отмена родительского `context` сохраняется как отмена вызова и не подменяется
ошибкой `execution_timeout`; при отмене WASM instance закрывается без возврата
частичного результата.

При `SIGINT` или `SIGTERM` Runtime сначала помечает себя неготовым и прекращает
принимать новые peer-вызовы, затем останавливает приём новых peer-сессий и
gracefully закрывает Plugin SDK REST listener. Уже допущенные `runtime.invoke`
могут завершиться в пределах общего `pluginShutdownGraceSeconds`; после начала
drain новые вызовы получают `503 not_ready`. Если grace истёк, процесс
завершается в установленный срок, а результат оборванного вызова считается
неизвестным и не воспроизводится автоматически. Проверка настоящего дочернего
`cmd/runtime` покрывает SIGINT во время bounded WASM-вызова и отказ нового
вызова после начала drain: [`tests/peer-invoke.test.ts`](https://github.com/Liapoldus/runtime/blob/main/tests/peer-invoke.test.ts).

Runtime подключён к Plugin SDK REST lifecycle. При `Reload` SDK запрашивает у
Core точные bytes целевого поколения через mTLS, а application applier
проверяет settings до их применения. Валидация отвергает неизвестные и
trailing JSON-поля, проверяет идентификаторы, scope, уникальность и границы
коллекций. Plugin повторно сверяет digest с исходными bytes и проверяет
соответствие schema version до атомарного pointer swap; отказанная конфигурация
не заменяет активный snapshot и не подтверждается readiness. Сквозной тест запускает собранный `cmd/runtime` и
проверяет успешное поколение, отказ invalid candidate с сохранением предыдущих
настроек и последующую активацию валидного поколения:
[`tests/runtime-lifecycle.test.ts`](https://github.com/Liapoldus/runtime/blob/main/tests/runtime-lifecycle.test.ts).

При `Reload` Runtime до ACK находит immutable WASM artifact по
`module.sha256`, ограниченно читает его, повторно проверяет SHA-256 и ABI без
исполнения WASM start function, затем одним atomic pointer swap публикует
settings и байты модуля как согласованную in-memory generation. Missing,
tampered, digest-mismatched или ABI-incompatible module оставляет прежнюю пару
активной. Artifact хранится на persistent filesystem в content-addressed
directory, задаваемой абсолютным `--artifact-dir`; пользовательская конфигурация
по-прежнему приходит только из Core через Plugin SDK `Reload`/exact-generation
pull.

Загрузка модулей принадлежит Runtime Admin Surface: action использует общий
Plugin SDK multipart artifact stream с `application/wasm`, метаданными SHA-256 и
лимитом артефакта 16 MiB. Runtime сверяет заявленный digest с фактическими
байтами, проверяет ABI и сохраняет неизменяемый файл по digest; повторная
передача тех же байт не создаёт вторую копию. Каталог показывает только файлы,
повторно прошедшие digest- и ABI-проверку. Описания action, metadata, receipt и
лимитов принадлежат `contracts/v1/admin-*` и `module-artifact-*.schema.json`.
`tests/runtime-lifecycle.test.ts` использует Core-side Plugin SDK client против
реального Runtime child process: проверяет mTLS, multipart upload, receipts,
каталог и последующий Reload. Core Management API forwarding до Runtime ещё не
покрыт этим тестом.

In-memory snapshot не переживает остановку процесса, но артефакты переживают:
файл модуля лежит в artifact-dir по digest. После старта Core повторно вызывает
`Reload`, а Runtime вновь получает exact generation и собирает пару из
сохранённого артефакта.
`tests/runtime-restart-reconnect.test.ts` запускает два дочерних Runtime
процесса против fake Core в том же процессе: первый применяет поколение,
затем Core выключается (503) и стартует второй реплика с тем же artifact-dir.
Пока Core недоступен, exact-generation `Reload` чисто отклоняется
(pending, ready=false), процесс жив; после возврата Core та же пара поколение +
digest применяется повторно без нового upload (каталог восстановлен из
persistent filesystem), повторное объявление того же поколения идемпотентно
репортится как `alreadyActive` без смены active pair, и следующее поколение
применяется штатно. Полный Core restart/reconciliation gate (обе стороны, Core
SDK v2 registration/lease) остаётся открытым. Сквозной transport E2E загрузки
модуля, продуктовые host-функции и peer-вызовы также остаются в
[TODO](https://github.com/Liapoldus/runtime/blob/main/TODO.md).
Runtime не объявляется production ready, пока эти и остальные пункты v2 не
пройдут сквозные проверки. `contracts/v1` — версия собственного контракта
плагина, не обещание релиза Liapoldus v1.
