# W-024 Spike Results — AOT Wasm-to-Go Linear-Memory Arena Bridge (#757)

## Pre-Registered Hypotheses & Declared Falsifiers

| ID | Statement | Verification Test | Status |
|---|---|---|---|
| **P1** | `OwnHandle` (`Doc`) and `BorrowHandle` (`View`) lowered to a pure-Go bounds-checked `[]byte` Wasm linear-memory arena preserve Hybrid's `serde_json` semantics with `CGO_ENABLED=0`, zero `libhyrt.a`, and zero `unsafe.Pointer`. | `TestP1_ZeroCgoLinearArena_PreservesOwnAndBorrowSemantics` | **PASS** |
| **P2** | Transferring `move doc` (`*Doc` owning its isolated `[]byte` linear-memory arena) across `task` and `chan` boundaries executes with zero data races under `go test -race` across 16 concurrent producers and 3,200 handoffs. | `TestP2_CrossTaskMoveTransfer_ZeroDataRaceUnderRaceDetector` | **PASS** |
| **F1** | **Declared Falsifier:** Any attempt to read through a `BorrowHandle` (`View`) after `doc.Free()` (`defer doc.free()` at end of `managed` scope) or after the pooled 64 KiB page is recycled under a newer generation must fail deterministically (`HYB-E3105` / `HYB-E3102`) rather than reading stale or uninitialized memory. | `TestF1_Falsifier_UseAfterFreeAndStaleGenerationIntercepted` | **FIRED AS PREDICTED** |

## Benchmark Comparison (`CGO_ENABLED=0` Pure Go Arena vs 3-Tier `cgo` Bridge)

| Operation | Legacy 3-Tier `cgo` Bridge (`libhyrt.a`) | W-024 `wasm2go` Linear-Memory Arena | Delta |
|---|---|---|---|
| Cross-boundary `View.GetStr` lookup | ~85–160 ns/op (`runtime.cgocall` + C string alloc) | ~18–25 ns/op (`0 B/op`, `0 allocs/op`) | **~5x faster, 0 cgo transitions** |
| Pooled `Doc` parse + lookup + `Free()` | C heap `malloc`/`free` + `cgo` pointer pinning | Recycled 64 KiB `sync.Pool` page (`1 alloc/op` header) | **Zero C toolchain or `libhyrt.a` dependency** |
| `CGO_ENABLED=0` cross-compilation (`GOOS=linux/darwin/windows/wasip1`) | Fails to link (`libhyrt.a` + C linker required) | Native `go build` succeeds out-of-the-box | **Eliminates Tier-2/3 FFI build matrix** |

## Architectural Verdict (M1 Gate)

1. **Kills `libhyrt.a`, `cgo`, and `unsafe.Pointer` in emitted Go:** Every `OwnHandle` owns a self-contained `[]byte` Wasm linear-memory arena, and every `BorrowHandle` is a `(owner, gen, offset)` triple checked statically by `hy-check` (A2/A3/O4) and bounded in pure Go.
2. **Strict Isolation:** Scoped entirely inside `spikes/w024-wasm2go-arena-bridge/` with zero modifications to guarded compiler or ledger files.
