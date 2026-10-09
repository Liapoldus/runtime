# AGENTS.md — Runtime

Репозиторий владеет Runtime-группами, WASM artifacts, контрактами команд,
host-функциями и собственными peer-методами. Менять только Runtime; не добавлять
product behavior в Core, Plugin SDK или pluginprotocol.

Четыре production-слоя: internal/domain/{models,interfaces},
internal/application/{config,module}, тематические internal/infrastructure и
internal/presentation/{admin,peerplugin}. Composition и генераторы — cmd/.
Группировать код по смыслу, использовать короткие имена файлов; не возвращать
плоский application и не оставлять пустые пакеты или compatibility aliases.

Native Go unit/regression tests размещать рядом с кодом. TypeScript в tests/
используется для contract consumers и child-process E2E. Fixture wrappers
заменять native tests только после равноценного покрытия. Сохранять WIP.

contracts/{abi,actions,codes,invoke,documents}.go и contracts/schema/ владеют
публичными определениями. contracts/v1/*.json — deterministic generated artifacts;
не редактировать их вручную. Runtime не читает эти JSON как static configuration.
Изменение определений: make generate, сверка diff, make check-generated.
Protobuf остаётся canonical у pluginprotocol; Runtime не создаёт его копий.

Обязательный независимый gate: npm ci, GOWORK=off GOFLAGS=-p=1 make check,
затем make check-race. Gate блокируется на reproducibility, pinned golangci-lint
v2.12.2 с root .golangci.yml, typed ESLint всех handwritten TS/tests,
typecheck, native tests, полном Vitest, vet и build. Fixtures и generated
compilation не исключать; baseline/global exclusions запрещены.
Узкие объяснённые suppressions допустимы только для доказанных false positives
или сохранения контрактного токена, а не вместо обработки ошибок.
Hosted Linux/macOS workflow должен завершиться успешно; локальный PASS не
заменяет hosted CI или branch protection.

Не давать WASM прямой доступ к сети, filesystem, WASI или неограниченной памяти.
Не логировать секреты и payloads. Не менять wire values, limits и sandbox
семантику в cleanup. Число replicas и процессы принадлежат оператору.
Коммиты, push, tags и releases выполняет main/integrator, не этот агент.
