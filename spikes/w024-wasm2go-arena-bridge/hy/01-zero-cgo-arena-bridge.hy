// W-024 (#757): Managed OwnHandle (`serde_json.doc`) and BorrowHandle (`serde_json.view`)
// backed by a pure-Go AOT-transpiled Wasm linear-memory arena (`[]byte`), requiring
// zero cgo, zero libhyrt.a, and zero unsafe.Pointer.
import "serde_json"

fn worker(doc serde_json.doc, out chan string) {
    managed {
        v := doc.root()
        out <- v.get_str("tier")
    }
}

fn main() {
    out := make(chan string, 1)
    managed {
        doc := try serde_json.parse("{\"service\":\"hybrid\",\"tier\":\"zero-cgo\"}")
        task worker(move doc, out)
    }
    _ = <-out
}
