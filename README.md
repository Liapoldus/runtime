# Runtime

Runtime executes commands from multiple immutable WASM modules and mediates
configured calls to other services through a bounded host ABI. Runtime settings
and module routes arrive from Core as exact generations through Service SDK;
artifacts are content-addressed and each invocation receives fresh isolated
WASM state.

The target contract and implementation gates are in
[Runtime v2 documentation](docs/site/services/runtime.md) and [TODO](TODO.md).
Settings v2, multi-module routing, atomic generation activation, fresh WASM
instances, the bounded host import, Core peer-directory/mTLS routing, and SDK
replica registration/lease/deregistration are wired into the process. The
retired single-module, Domain-scope and no-host-call paths have been removed.
Native Core→Runtime→Domain acceptance against real Core, hosted gates, and the
production contract remain incomplete.

Public `contracts/v2/*.json` are generated from `contracts/*.go` and
`contracts/schema/`. Use `make generate`, then `make check-generated`.
Independent gates: `npm ci`, `GOWORK=off GOFLAGS=-p=1 make check`, and
`make check-race`; hosted CI and cross-repository acceptance are separate.
