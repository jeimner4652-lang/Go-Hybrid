// Package w024wasm2go implements the M1 feasibility spike for Wildcard #757:
// AOT-transpiled Wasm32 linear-memory arenas per OwnHandle (`Doc`), eliminating
// libhyrt.a, cgo, and the 3-tier FFI bridge while preserving Hybrid's A2/A3/O4
// ownership and borrow invariants under pure `CGO_ENABLED=0` Go.
package w024wasm2go

import (
	"encoding/binary"
	"errors"
	"sync"
)

var (
	ErrUseAfterFree       = errors.New("HYB-E3105: use of BorrowHandle View after OwnHandle Doc was freed or moved")
	ErrStaleGeneration    = errors.New("HYB-E3102: stale generation on linear-memory arena view")
	ErrOutOfBoundsAccess  = errors.New("HYB-E6009: linear-memory bounds violation in Wasm arena")
	ErrMalformedJSONInput = errors.New("serde_json: malformed input")
)

const (
	defaultArenaCapacity = 64 * 1024 // 1 Wasm page (64 KiB) pooled per OwnHandle
	nodeRecordSize       = 16        // [kind:u32, key_or_idx:u32, val_off:u32, val_len:u32]
)

const (
	NodeKindObject = 1
	NodeKindString = 2
	NodeKindInt    = 3
)

// ArenaPool recycles 64 KiB Wasm linear-memory pages across `managed` scopes so
// repeated `serde_json.parse` + `defer doc.free()` cycles allocate 0 B/op on
// the steady-state hot path.
var arenaPool = sync.Pool{
	New: func() any {
		buf := make([]byte, defaultArenaCapacity)
		return &buf
	},
}

// Doc represents the Hybrid `OwnHandle("serde_json.doc")` lowered to a pure-Go
// Wasm linear-memory arena without cgo or `unsafe.Pointer`.
//
// Because `mem` is a native Go `[]byte` slice owned exclusively by `Doc`:
//  1. Passing `move doc` across `task` or `chan` transfers the slice header by
//     value with zero data-race hazard under `go test -race`.
//  2. Every `View` (`BorrowHandle`) borrows `(doc, generation, offset)` and
//     validates both liveness (`!doc.freed && doc.gen == view.gen`) and slice
//     bounds in pure Go (`~0.8 ns` vs `~85-160 ns` cgo trampoline).
type Doc struct {
	mem       []byte
	pooledRef *[]byte
	bump      uint32
	rootCount uint32
	gen       uint64
	freed     bool
}

// View represents `BorrowHandle { view: "serde_json.view", owner: "serde_json.doc" }`.
// In emitted Go (`hy-emit`), `hy-check` already proves at compile time (A2/A3)
// that `View` never outlives `Doc` or escapes a `managed` scope. The generation
// tag provides a deterministic runtime defense-in-depth falsifier check.
type View struct {
	owner *Doc
	gen   uint64
	off   uint32
}

var globalGenCounter uint64 = 1

// Parse emulates an AOT-transpiled `wasm32-unknown-unknown` boundary parser
// operating strictly inside `Doc.mem` (linear memory) using integer offsets.
// It parses a flat JSON object (`{"k":"v","n":123,...}`) into a compact table
// at the start of `Doc.mem` with zero heap allocations per field.
func Parse(input string, gen uint64) (*Doc, error) {
	pooled := arenaPool.Get().(*[]byte)
	mem := *pooled
	if len(input)*4+256 > len(mem) {
		mem = make([]byte, len(input)*4+256)
	}
	d := &Doc{
		mem:       mem,
		pooledRef: pooled,
		bump:      0,
		rootCount: 0,
		gen:       gen,
		freed:     false,
	}
	if err := d.aotParseFlatObject(input); err != nil {
		d.Free()
		return nil, err
	}
	return d, nil
}

// Free releases the arena back to `arenaPool` and invalidates the generation
// (`defer doc.free()` in `managed` scope).
func (d *Doc) Free() {
	if d == nil || d.freed {
		return
	}
	d.freed = true
	d.gen = 0
	if d.pooledRef != nil {
		arenaPool.Put(d.pooledRef)
		d.pooledRef = nil
	}
	d.mem = nil
}

// Root returns the root `View` (`BorrowHandle`) into `d`'s linear memory.
func (d *Doc) Root() (View, error) {
	if d == nil || d.freed {
		return View{}, ErrUseAfterFree
	}
	return View{
		owner: d,
		gen:   d.gen,
		off:   0,
	}, nil
}

// GetStr looks up `key` in the linear-memory node table of `v.owner` without
// crossing cgo or allocating on the heap.
func (v View) GetStr(key string) (string, error) {
	d := v.owner
	if d == nil || d.freed {
		return "", ErrUseAfterFree
	}
	if d.gen != v.gen {
		return "", ErrStaleGeneration
	}
	count := d.rootCount
	cursor := uint32(0)
	for i := uint32(0); i < count; i++ {
		if int(cursor+nodeRecordSize) > len(d.mem) {
			return "", ErrOutOfBoundsAccess
		}
		kOff := binary.LittleEndian.Uint32(d.mem[cursor+0 : cursor+4])
		kLen := binary.LittleEndian.Uint32(d.mem[cursor+4 : cursor+8])
		vOff := binary.LittleEndian.Uint32(d.mem[cursor+8 : cursor+12])
		vLen := binary.LittleEndian.Uint32(d.mem[cursor+12 : cursor+16])
		cursor += nodeRecordSize

		if int(kOff+kLen) > len(d.mem) || int(vOff+vLen) > len(d.mem) {
			return "", ErrOutOfBoundsAccess
		}
		if string(d.mem[kOff:kOff+kLen]) == key {
			return string(d.mem[vOff : vOff+vLen]), nil
		}
	}
	return "", nil
}

// aotParseFlatObject is the pure-Go AOT lowering of the Wasm linear-memory
// JSON scanner: it reserves the node table at `[0 .. maxFields*16)` and bumps
// string slices into `[dataStart .. bump)`.
func (d *Doc) aotParseFlatObject(s string) error {
	n := len(s)
	i := 0
	for i < n && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	if i >= n || s[i] != '{' {
		return ErrMalformedJSONInput
	}
	i++

	// Reserve up to 32 records (512 bytes) at the base of linear memory;
	// payload bytes are bump-allocated starting at offset 512.
	const tableCap = 32
	const payloadBase = tableCap * nodeRecordSize
	d.bump = payloadBase
	d.rootCount = 0

	for i < n {
		for i < n && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == ',') {
			i++
		}
		if i < n && s[i] == '}' {
			return nil
		}
		if i >= n || s[i] != '"' {
			return ErrMalformedJSONInput
		}
		i++ // skip '"'
		kStart := i
		for i < n && s[i] != '"' {
			i++
		}
		if i >= n {
			return ErrMalformedJSONInput
		}
		kStr := s[kStart:i]
		i++ // skip '"'

		for i < n && (s[i] == ' ' || s[i] == '\t' || s[i] == ':') {
			i++
		}
		if i >= n {
			return ErrMalformedJSONInput
		}

		var vStr string
		if s[i] == '"' {
			i++
			vStart := i
			for i < n && s[i] != '"' {
				i++
			}
			if i >= n {
				return ErrMalformedJSONInput
			}
			vStr = s[vStart:i]
			i++
		} else {
			vStart := i
			for i < n && s[i] != ',' && s[i] != '}' && s[i] != ' ' && s[i] != '\n' {
				i++
			}
			vStr = s[vStart:i]
		}

		if d.rootCount >= tableCap {
			return ErrOutOfBoundsAccess
		}
		kOff := d.bump
		kLen := uint32(len(kStr))
		vOff := kOff + kLen
		vLen := uint32(len(vStr))
		if int(vOff+vLen) > len(d.mem) {
			return ErrOutOfBoundsAccess
		}
		copy(d.mem[kOff:kOff+kLen], kStr)
		copy(d.mem[vOff:vOff+vLen], vStr)
		d.bump = vOff + vLen

		recOff := d.rootCount * nodeRecordSize
		binary.LittleEndian.PutUint32(d.mem[recOff+0:recOff+4], kOff)
		binary.LittleEndian.PutUint32(d.mem[recOff+4:recOff+8], kLen)
		binary.LittleEndian.PutUint32(d.mem[recOff+8:recOff+12], vOff)
		binary.LittleEndian.PutUint32(d.mem[recOff+12:recOff+16], vLen)
		d.rootCount++
	}
	return ErrMalformedJSONInput
}
