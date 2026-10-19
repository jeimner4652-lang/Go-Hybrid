// W-024 (#757) Negative case: BorrowHandle (`v`) cannot escape `managed` scope
// or outlive its owning `Doc` arena (`HYB-E3102` / `HYB-E3105`).
import "serde_json"

fn escape_view() serde_json.view {
    managed {
        doc := try serde_json.parse("{\"k\":\"v\"}")
        v := doc.root()
        return v // rejected by hy-check (A2/A3): view cannot escape managed scope
    }
}
