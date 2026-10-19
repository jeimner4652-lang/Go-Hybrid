package w024wasm2go

import (
	"fmt"
	"sync"
	"testing"
)

const samplePayload = `{"service":"hybrid-compiler","tier":"zero-cgo","version":"2026.10","status":"verified"}`

// TestP1_ZeroCgoLinearArena_PreservesOwnAndBorrowSemantics verifies Hypothesis P1:
// OwnHandle (Doc) and BorrowHandle (View) operate entirely inside a pure-Go
// bounds-checked []byte Wasm linear-memory arena without cgo or unsafe.Pointer.
func TestP1_ZeroCgoLinearArena_PreservesOwnAndBorrowSemantics(t *testing.T) {
	doc, err := Parse(samplePayload, 1)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	defer doc.Free()

	root, err := doc.Root()
	if err != nil {
		t.Fatalf("unexpected root error: %v", err)
	}

	gotService, err := root.GetStr("service")
	if err != nil || gotService != "hybrid-compiler" {
		t.Fatalf("GetStr(service) = (%q, %v), want (\"hybrid-compiler\", nil)", gotService, err)
	}

	gotTier, err := root.GetStr("tier")
	if err != nil || gotTier != "zero-cgo" {
		t.Fatalf("GetStr(tier) = (%q, %v), want (\"zero-cgo\", nil)", gotTier, err)
	}
}

// TestP2_CrossTaskMoveTransfer_ZeroDataRaceUnderRaceDetector verifies Hypothesis P2:
// Moving an OwnHandle (`move doc`) across a `task` / `chan` boundary transfers the
// underlying linear-memory slice cleanly with 0 data races under `go test -race`.
func TestP2_CrossTaskMoveTransfer_ZeroDataRaceUnderRaceDetector(t *testing.T) {
	const workers = 16
	const iterations = 200
	ch := make(chan *Doc, workers)
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				payload := fmt.Sprintf(`{"worker":"%d","iter":"%d","mode":"wasm2go"}`, id, i)
				doc, err := Parse(payload, uint64(id*10000+i+1))
				if err != nil {
					t.Errorf("parse failed: %v", err)
					return
				}
				// Emulates `ch <- move doc`: producer relinquishes ownership.
				ch <- doc
			}
		}(w)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	consumed := 0
	for doc := range ch {
		root, err := doc.Root()
		if err != nil {
			t.Fatalf("consumer Root() failed: %v", err)
		}
		mode, err := root.GetStr("mode")
		if err != nil || mode != "wasm2go" {
			t.Fatalf("consumer GetStr(mode) = (%q, %v)", mode, err)
		}
		doc.Free()
		consumed++
	}

	if consumed != workers*iterations {
		t.Fatalf("consumed %d docs, want %d", consumed, workers*iterations)
	}
}

// TestF1_Falsifier_UseAfterFreeAndStaleGenerationIntercepted verifies Declared
// Falsifier F1: any attempt to read through a BorrowHandle (`View`) after the
// owning `Doc` is freed (or after its pooled linear-memory page is reused by a
// newer generation) is deterministically rejected with HYB-E3105 / HYB-E3102.
func TestF1_Falsifier_UseAfterFreeAndStaleGenerationIntercepted(t *testing.T) {
	doc1, err := Parse(samplePayload, 101)
	if err != nil {
		t.Fatalf("parse doc1 failed: %v", err)
	}
	staleView, err := doc1.Root()
	if err != nil {
		t.Fatalf("doc1.Root() failed: %v", err)
	}

	// Free doc1 (emulating end of `managed` scope `defer doc.free()`).
	doc1.Free()

	if _, err := staleView.GetStr("service"); err != ErrUseAfterFree {
		t.Fatalf("expected ErrUseAfterFree on freed Doc, got %v", err)
	}

	// Allocate doc2 (which reuses the pooled 64 KiB page under generation 202)
	// and verify that a view forged with old generation 101 against doc2 is
	// rejected with ErrStaleGeneration.
	doc2, err := Parse(`{"service":"reused-arena"}`, 202)
	if err != nil {
		t.Fatalf("parse doc2 failed: %v", err)
	}
	defer doc2.Free()

	forgedStaleView := View{
		owner: doc2,
		gen:   101, // old generation
		off:   0,
	}
	if _, err := forgedStaleView.GetStr("service"); err != ErrStaleGeneration {
		t.Fatalf("expected ErrStaleGeneration on mismatched generation, got %v", err)
	}
}
