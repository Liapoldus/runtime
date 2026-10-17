# Runtime v2 — production implementation gates

The repository is partway through the approved breaking transition in
[`docs/site/services/runtime.md`](docs/site/services/runtime.md). Do not retain a
parallel tenant-scope or no-host-call architecture.

## Studio configurator scope — 2026-10-13

Runtime/WASM product configuration is explicitly deferred by
[`../../studio/plan.md`](../../studio/plan.md). No Runtime editor, ABI, module
management or product setting changes are part of this goal; keep only required
shared Service SDK/Wool consumer fixes and their regressions in scope.

## P0 — contracts and configuration generation

- [x] Re-inventory repository state, consumers, contracts and current CI while
  preserving checkout WIP. Removal map: retired v1 WASM/admin contracts and
  obsolete blocker document are deleted; settings now use the SDK v2 and
  `wool/v3` module paths; Core→SDK→Runtime→Domain and mock-Core
  process fixtures cover the remaining consumers. Source searches found no
  live single-module, OwnerGroup, delegated-scope or `domain.*`-only host-call
  path; the remaining tenant/site phrase states that caller context is absent.
  `make check` and `make check-race` are the current local gates; hosted OS/CI
  remains open.
- [x] Define versioned settings with a module catalog `moduleId → digest`,
  commands mapped to `moduleId + export`, per-command entity restrictions and
  per-command target-service/method allowlists. Remove tenant/site scope fields.
- [x] Keep the published settings JSON Schema aligned with generic
  `service.method` validation; do not restrict host-call methods to `domain.*`.
  Regression verifies non-Domain methods are admitted while malformed or
  multi-segment method names remain rejected.
- [x] Validate artifact digests, ABI, configured export existence/signature and
  target/method allowlist syntax before generation activation. The command's
  configured export is passed to the executor, not replaced by the ABI default.
- [x] Resolve host target authorization against the live Core caller→target
  policy per call; absent or revoked links fail closed. Config activation does
  not snapshot mutable Core links as permanent permission.
- [x] Atomically activate settings, artifact set, routes and permissions. A
  rejected candidate must leave the entire previous generation active.
- [x] Migrate composition to the `service-sdk/v2` and generic
  `wool/v3` APIs. Independent `GOWORK=off` tests/build now pass using
  published `v2.0.0`/`v3.0.0` tags without local replacements.
- [x] Wire SDK replica registration, renewable Core lease and explicit
  deregistration into the Runtime process using the peer certificate URI bound
  to the immutable instance/replica/incarnation/placement identity. REST and
  peer advertised endpoints are explicit required bootstrap inputs; a rejected
  registration prevents startup, and a failed renewal fences the process for a
  supervisor-issued new incarnation.
- [x] Complete real-Core lifecycle acceptance for exact-generation pull,
  ACK/readiness, fresh-incarnation restart/reconnect and coordinated drain.
  The real Core→SDK→Domain→Runtime child-process acceptance now verifies
  Runtime process restart under a fresh incarnation, exact-generation
  reapplication and a successful host call after Domain also restarts. Explicit
  deregistration is now asserted through Core's not-ready/drift status after
  Runtime shutdown. Real Core rejection/recovery of a structurally valid but
  semantically invalid module route is now covered, including the old active
  generation, pending/not-ready status, and successful routing after the next
  valid Core generation. A real Runtime child-process test now confirms an
  admitted finite invocation completes while shutdown fences new calls;
  the real Core→SDK→Runtime→Domain scenario also observes the Domain write from
  an admitted WASM host call, sends SIGTERM, verifies a new call is fenced,
  confirms the admitted call completes, then checks Core-visible deregistration.
  This closes local real-Core process acceptance, not hosted OS or full lease-
  failure/cancellation qualification.

## P1 — isolated execution and service calls

- [x] Support multiple immutable content-addressed WASM artifacts in one active
  generation and route each command to its configured module/export.
- [x] Instantiate fresh WASM execution state per invocation; only immutable
  compiled module caching may cross calls. Bound module count, artifacts, memory,
  input/output, concurrency, execution, host calls and total call chain.
- [x] Add a versioned host ABI over generic `wool` for bounded
  synchronous request/response calls. Enforce the local per-command target/
  method allowlist, host payload bounds, deadlines and cancellation.
- [x] Connect host calls to the Core peer-directory resolver and authenticated
  `wool` dialer. Each call resolves one authorized target, validates
  peer-contract compatibility/eligibility, pins the remote mTLS URI identity,
  and does not retry. Runtime publishes its peer endpoint through Core replica
  registration; the real Core directory delivery is verified by the current
  `serve-domain` process acceptance.
- [x] Verify a real Core→SDK→Runtime→Domain path using Runtime process mTLS
  identity and Domain Core grants. The child-process test proves a Domain write
  is denied without the grant, succeeds after a Core settings generation adds
  it, and becomes visible to another authenticated service. Runtime sends no
  tenant/site/group/OwnerGroup context.
- [x] Add negative end-to-end cases for Runtime command entity/method narrowing
  and Core link deletion. Separate Core grant enforcement remains proven by the
  Domain-level deny/allow case above.
- [x] Deny direct network/filesystem/WASI/environment imports. Extend module
  admission and adversarial fixtures to cover each permitted host capability,
  malformed inputs, reentrancy, cancellation, timeout, trap and resource limits.
- [x] Never auto-replay side-effecting calls or unknown outcomes. No distributed
  transaction across services; expose typed partial/unknown outcome to the caller.

## P2 — quality, integration and release gates

- [ ] Complete Go-native acceptance for config validation, catalog/routing,
  atomic activation, host authorization, per-call instance isolation and
  bounded concurrency; keep TypeScript for public contracts and real
  child-process E2E. Regression tests now prove invocation does not copy
  unrelated artifacts and admission is capped at four concurrent invocations;
  a race-tested Go case now drives four actual concurrent `Invoke` calls,
  verifies the fifth is overloaded, fences new calls during drain, and proves
  drain waits for admitted calls. A WASM unit test also executes a mutable-
  global counter twice through the same Executor and proves each fresh instance
  starts from its initial state. Remaining adversarial and config-generation
  concurrency cases are still open.
- [x] Add Go-native tests for multi-module settings/generation, per-command host
  authorization and the actual versioned WASM host import. The full cross-service
  and isolation acceptance set remains open below.
- [x] Add real Core→SDK→Runtime→Domain process E2E for Core exact-generation
  config, directory eligibility, allowlisted WASM host calls, Runtime identity,
  Domain grant deny/allow, Runtime entity/method narrowing and Core link deletion.
- [x] Add mock-service conformance coverage at the host ABI and invoker boundaries:
  the WASM host import preserves a non-Domain service/method/payload, and the
  invoker routes a configured non-Domain call only through its command allowlist.
- [x] Exercise lease-renewal refusal in the Runtime child process against an
  mTLS Core lifecycle fixture using the contract's real 10-second renewal
  cadence. Runtime exits fail-closed and explicitly deregisters its incarnation.
- [x] Cancel an admitted Runtime peer-caller operation over an actual mTLS
  `wool` session to a mock service; assert cancellation reaches the
  remote handler and the call count remains one.
- [x] Add a child-process cancellation acceptance for an in-flight Runtime→
  service host call. The real Runtime executable is configured from a Core-
  format mTLS peer directory, calls a mock metrics service over authenticated
  `wool`, then receives cancellation from the invoking caller; the
  remote handler observes cancellation and its delivery count remains one.
  Generation rejection/recovery and real Core restart/reconnect are also
  covered cross-repository. Artifact upload uses the SDK streaming action in
  process-level lifecycle tests; aggregate generation bytes are capped and
  covered at config validation.
- [ ] Verify artifact handling and Runtime host calls across supported OS/carrier
  combinations with hosted Linux/macOS CI. Do not claim hosted status from local
  runs.
- [x] Repeat the race-instrumented Core→SDK→Domain→Runtime acceptance and
  diagnose the prior transient host-call refusal. Runtime now emits a bounded,
  closed `stage` diagnostic without peer addresses, identities, payloads or raw
  transport errors. The failure was `no_ready_peer`: Core's eligible directory
  lease lagged service-local readiness. The Core fixture now waits for published
  route eligibility, builds its race-instrumented peer client once, and keeps
  the finite drain loop inside the ABI deadline. Two consecutive complete
  `GOFLAGS=-race` process runs passed; additional platform and lease-failure
  qualification stays open.
- [x] Remove stale single-module product contracts, scope ACL, delegated-scope/
  OwnerGroup code, fixtures and contradictory architecture text. Keep protocol
  and Admin Surface versions that belong to their separate contracts.
- [ ] Remove any further duplicate/unused wrappers only with consumer and
  coverage evidence. Regenerate contracts and pass independent lint,
  typecheck, tests, race, vet, build and doc gates. Production-ready requires
  no open P0/P1 blockers.

## Latest verification evidence

Independent `GOWORK=off` Go tests/vet/build and full `make check-race` passed
against published SDK/protocol tags without replacements. `make check` also
passed generated-contract verification, Go lint, TypeScript lint/typecheck,
42 Vitest tests, Go tests, vet and build. The aggregated VitePress build passed.
Hosted Linux/macOS and the remaining Core→SDK→Runtime→Domain lifecycle,
artifact and negative-authorization cases remain open.

An earlier Runtime `make check` and `make check-race` passed with the four-call
admission cap, command-scoped immutable generation view and aggregate 128 MiB
artifact ceiling. At that checkpoint, 43 Vitest tests passed. Regression tests confirm an
invocation does not copy an unrelated 16 MiB artifact, the fifth concurrent
call is rejected as retryable `overloaded`, and an over-limit generation
preserves the previous active configuration. A test-first cancellation regression
also confirms that a candidate cancelled during module validation is not
activated and leaves the previous generation intact. Command routing now passes
the configured `moduleId + export` through the Invoker and WASM executor; Core
configuration application rejects missing or ABI-incompatible exports before
activation. Removed the unused default-export/no-host Executor wrappers;
fixtures now invoke the explicit export/capability API. The latest aggregate
VitePress attempt remains blocked by four dead links in out-of-scope Studio.
The current aggregate documentation build is blocked by four dead links in the
out-of-scope Studio owner sources (`studio/index.md` and
`studio/technical-architecture.md`).

The current real Core→SDK→Domain→Runtime child-process scenario now submits a
Runtime settings generation whose command references a missing catalog module.
Core reports `activation_failed`; Runtime readiness exposes the still-applied
generation with the rejected generation pending; a later valid Core generation
restores readiness and the previous command route. The full integration test
passed on Darwin through the bundled Node runtime. Coordinating drain with a
real Core, hosted OS CI and the remaining resource/security gates are still
open. A separate Runtime child-process test confirms successful completion of
an admitted finite call during local graceful shutdown, fencing of new calls,
and process exit after completion.

The latest Core `serve-domain` child-process acceptance now restarts both
Domain and Runtime under fresh Core incarnations. It confirms Core restores the
Domain model and Runtime exact configuration generation, Domain volatile data
is lost only on Domain restart, Core republishes the Domain peer route, and
Runtime host calls resume after both process restarts; after graceful Runtime
shutdown, Core reports the desired instance as not-ready. It now also observes
a Domain write made by an admitted in-flight Runtime host call, SIGTERMs Runtime,
verifies new calls are fenced while that call completes, and checks the
subsequent Core deregistration. This coordinated drain acceptance passed on
Darwin through the local multi-repository workspace. Hosted OS, invalid-
candidate ACK against real Core, and end-to-end resource ceilings remain open.

The coordinated-drain change was verified with the real multi-repository
Core→SDK→Runtime→Domain integration on Darwin. Runtime `make check` and
`make check-race` pass, including tests for closed peer-call failure stages and a
Go test that compiles and executes a host-importing WASM module which returns
its host response after a bounded guest loop. The fixture exposed and corrected
the local-index encoding for WASM parameters versus declared locals. Two
consecutive full Core→SDK→Domain→Runtime process runs with `GOFLAGS=-race` pass
after waiting for Core directory eligibility before the write and using a
prebuilt peer client for bounded polling.

The generic host-call path now has a paired non-Domain conformance regression:
the actual WASM host import preserves a `metrics.aggregate` call, and the
Invoker forwards a non-Domain target only when that exact service/method pair is
present in the active command allowlist. Targeted Go and race tests pass. A new
Runtime lifecycle process scenario also makes the mTLS Core fixture refuse the
next lease renewal; Runtime exits fail-closed and deregisters its incarnation.
At that checkpoint, the Runtime test suite passed (43 Vitest tests), along with the complete
Runtime `make check` using bundled Node: generated contracts, golangci-lint
(0 issues), ESLint, typecheck, Go tests, vet and build. Hosted supported-OS
runs and outstanding production resource gates remain open.

Peer-caller cancellation now has a race-tested regression: cancellation reaches
the in-flight service session and the side-effecting call is not retried. The
Runtime lifecycle child-process scenario passes under `GOFLAGS=-race` while the
mTLS Core fixture refuses renewal, observes fail-closed process exit and
deregistration, and preserves the active-generation lifecycle assertions.
An additional real mTLS `wool` test cancels the caller while a mock
service handler is running, confirms the remote handler observes cancellation,
and confirms one delivery. The full Runtime `make check` passes again on this
tree after those regressions. Cancellation through a full Runtime child process
was not covered by those earlier tests; the child-process gate is now closed as
recorded above.

That process-level gate is now closed locally: `tests/peer-invoke.test.ts`
drives the Runtime child through a Core-format mTLS directory response and a
real authenticated `wool` mock handler. Cancelling the inbound
`runtime.invoke` cancels the remote handler and leaves its delivery count at
one. The focused child-process test passes (12 cases), and the fixture also
passes under `GOFLAGS=-race`. This is mock-Core/local acceptance, not hosted OS
qualification or the real-Core Domain grant scenario.

The generated Runtime settings schema previously constrained every configured
host-call method to `domain.*`, despite Go validation and the host ABI supporting
generic service methods. A code-owned schema regression reproduced the mismatch;
the schema now accepts bounded `service.method` identifiers for any service and
the generated v2 artifact is synchronized. Full Runtime `make check` passes
after the fix (generated-contract check, golangci-lint, ESLint, typecheck, 43
Vitest tests at that checkpoint, Go tests, vet and build). Hosted OS qualification remains open.

Latest verification after adding Runtime child-process host-call cancellation:
`make check` passes with 44 Vitest tests, generated-contract parity, zero
golangci-lint issues, ESLint, typecheck, Go tests, vet and build. The focused
12-case child-process test passes in normal mode and its fixture passes under
`GOFLAGS=-race`; full `make check-race` also passes with 44 tests. Race mode
exposed that a shutdown test inferred admission from CPU-loop duration. Replace
that timing assumption with a deterministic barrier: the Runtime fixture holds
an actual authenticated mTLS `wool` host call at a mock handler,
signals shutdown only after the call is admitted, verifies new calls are
refused, then releases the admitted call and verifies graceful completion. The
fixture no longer contains the unused finite CPU-loop generator. `make check`
and full `make check-race` pass with this barrier. This closes local mock-Core
drain acceptance only; hosted OS/CI, real-Core grant/link behavior under
cancellation, and remaining Domain resource qualification remain open.

Linux ARM64 container verification also passes for race-enabled
`peercaller`/Invoker/WASM package tests and the race-instrumented Runtime
child-process lease-refusal lifecycle scenario. This is local container evidence,
not hosted CI or cross-OS qualification.

## Explicit non-goals

- No direct WASM network/filesystem/WASI access, arbitrary target/method
  selection, streaming host calls, distributed transactions or automatic replay.
- No tenant/site/group or Domain delegation payload; Domain authorization uses
  authenticated Runtime mTLS identity plus Core entity/operation grants.
- No public Server HTTP route in v2. Operator controls process count and
  deployment; Core does not install or supervise Runtime workloads.


## Config preflight endpoint — 2026-10-17

- Runtime metadata now validates candidate command/module routing without activation. This lets Core reject a broken candidate while keeping the active catalog and generation ready. Go tests, vet and build passed. Commit/push pending.
