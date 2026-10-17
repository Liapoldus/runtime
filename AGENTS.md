# AGENTS.md — Runtime

Runtime owns WASM module/artifact lifecycle, command routing, versioned host ABI,
service-call policy enforcement, product contracts and peer methods. Core owns
desired settings and caller→target link policy; Service SDK owns lifecycle REST;
`wool` owns generic authenticated service-to-service transport.

## Target architecture

- A configuration generation contains an immutable module catalog
  (`moduleId → digest`) and commands mapped to `moduleId + export`.
- Activate settings, module catalog, command routes and host-call allowlists as
  one atomic generation. Persist only immutable content-addressed WASM artifacts;
  pull config from Core through exact-generation SDK lifecycle.
- Instantiate fresh isolated WASM state for each invocation. A compiled immutable
  module may be cached; memory/global/table state must not cross invocations.
- WASM has no direct network, filesystem, WASI, environment or unbounded-memory
  access. Host calls use a versioned, bounded, synchronous request/response ABI
  and generic `wool`, restricted per command to configured
  service/method pairs. Respect Core-owned peer-link policy.
- Domain has global data and no tenant/site/group. Domain reads require mTLS
  authentication; writes also require Core grants for Runtime identity/entity/
  operation. Runtime allowlists may narrow but never grant Domain permissions.
- No distributed transaction across service calls and no auto-replay of
  side-effecting or unknown-outcome calls. Domain transaction semantics do not
  extend to other services.

## Code, tests and contracts

Keep four production layers: `internal/domain/{models,interfaces}`;
`internal/application/{config,module}`; themed `internal/infrastructure`; and
`internal/presentation/{admin,peerservice}`. Composition/generators live in
`cmd/`. Use short names and cohesive packages; avoid empty packages, wrappers
and compatibility aliases.

`contracts/*.go` and `contracts/schema/` own versioned contract definitions;
`contracts/v2/*.json` are deterministic generated outputs. Never edit generated
files manually. Regenerate with `make generate` and verify with
`make check-generated`. Protobuf remains canonical in `wool`.

Native Go unit/regression tests live next to code. TypeScript tests cover public
contract consumers and real child-process/E2E boundaries. Preserve coverage
when deleting fixture wrappers. Use red → implementation → green and retain
adversarial WASM tests for imports, memory, deadlines, cancellation and traps.

Required independent gates: `npm ci`, `GOWORK=off GOFLAGS=-p=1 make check`,
then `make check-race`. Keep pinned golangci-lint, typed ESLint, full handwritten
source/test coverage, reproducible generation, typecheck, tests, vet and build.
Hosted Linux/macOS CI is distinct from local PASS. Preserve WIP and inspect
consumers before deleting contracts/code/fixtures. Do not log secrets or raw
payloads. No commits, push, tags or release without explicit request.

## Граница плана Studio

`studio/plan.md` откладывает продуктовую конфигурацию Runtime/WASM. В рамках этой
цели допустимы только необходимые mechanical Service SDK/Wool terminology/API
consumer updates и доказанные build/conformance fixes. Не добавлять Runtime
settings UI, ABI, module-management или product features ради Server/Echo
конфигуратора.
