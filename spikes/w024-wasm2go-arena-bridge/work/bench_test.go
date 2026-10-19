package w024wasm2go

import "testing"

func BenchmarkWasm2GoLinearArenaParseAndLookup(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		doc, err := Parse(samplePayload, uint64(i+1))
		if err != nil {
			b.Fatal(err)
		}
		root, _ := doc.Root()
		v, _ := root.GetStr("tier")
		if v != "zero-cgo" {
			b.Fatal(v)
		}
		doc.Free()
	}
}

func BenchmarkWasm2GoViewLookupHotLoop(b *testing.B) {
	doc, err := Parse(samplePayload, 1)
	if err != nil {
		b.Fatal(err)
	}
	defer doc.Free()
	root, _ := doc.Root()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, err := root.GetStr("status")
		if err != nil || v != "verified" {
			b.Fatal(err)
		}
	}
}
