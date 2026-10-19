//! Regression tests for issue #749:
//! Passing an O8 map-slot loan (`&m[k]`) through a return-forwarding call
//! (`p2 := id(p)`, `p2 = id(p)`) or multi-value unpack (`p2, ok := id_pair(p)`,
//! `p2, ok = id_pair(p)`), both in top-level functions and inside closure bodies,
//! must preserve:
//! 1. A3 loan-window extension (`HYB-E3102` when `m` is inserted/deleted into
//!    before `p2`'s last use).
//! 2. T24 (`HYB-E3104` when taking `&p2` or `&p2.field`).
//! Non-reference unpacked results (`int`, `bool`) must not falsely inherit the loan.

use hy_check::{check_file, Verdict};
use hy_diag::Severity;
use hy_syntax::parse_file;

fn check(src: &str) -> hy_check::CheckResult {
    let ast = parse_file("inline.hy", src)
        .unwrap_or_else(|diags| panic!("must parse (parser contract): {:?}", diags));
    check_file(&ast)
}

fn error_codes(r: &hy_check::CheckResult) -> Vec<String> {
    r.diagnostics
        .iter()
        .filter(|d| d.severity == Severity::Error)
        .map(|d| d.code.clone())
        .collect()
}

#[test]
fn return_forwarded_slot_loan_local_and_assign_extend_a3_window_e3102() {
    let local_src = r#"
import "fmt"

struct entry {
    n int
}

fn id(p *entry) *entry {
    return move p
}

fn main() {
    m := map[string]entry{"a": entry{n: 1}}
    p := &m["a"]
    p2 := id(p)
    m["b"] = entry{n: 2}
    p2.n = 9
    fmt.println(p2.n)
}
"#;
    let r1 = check(local_src);
    assert_eq!(r1.verdict, Verdict::Reject, "{:?}", r1.diagnostics);
    assert!(
        error_codes(&r1).iter().any(|c| c == "HYB-E3102"),
        "expected HYB-E3102 for local return-forwarded slot loan, got: {:?}",
        r1.diagnostics
    );

    let assign_src = r#"
import "fmt"

struct entry {
    n int
}

fn id(p *entry) *entry {
    return move p
}

fn main() {
    m := map[string]entry{"a": entry{n: 1}}
    p := &m["a"]
    var p2 *entry
    p2 = id(p)
    m["b"] = entry{n: 2}
    p2.n = 9
    fmt.println(p2.n)
}
"#;
    let r2 = check(assign_src);
    assert_eq!(r2.verdict, Verdict::Reject, "{:?}", r2.diagnostics);
    assert!(
        error_codes(&r2).iter().any(|c| c == "HYB-E3102"),
        "expected HYB-E3102 for assigned return-forwarded slot loan, got: {:?}",
        r2.diagnostics
    );
}

#[test]
fn multi_value_unpacked_slot_loan_local_and_assign_extend_a3_window_e3102() {
    let local_src = r#"
import "fmt"

struct entry {
    n int
}

fn id_pair(p *entry) (*entry, bool) {
    return move p, true
}

fn main() {
    m := map[string]entry{"a": entry{n: 1}}
    p := &m["a"]
    p2, ok := id_pair(p)
    m["b"] = entry{n: 2}
    p2.n = 9
    fmt.println(ok)
    fmt.println(p2.n)
}
"#;
    let r1 = check(local_src);
    assert_eq!(r1.verdict, Verdict::Reject, "{:?}", r1.diagnostics);
    assert!(
        error_codes(&r1).iter().any(|c| c == "HYB-E3102"),
        "expected HYB-E3102 for multi-value local unpack of slot loan, got: {:?}",
        r1.diagnostics
    );

    let assign_src = r#"
import "fmt"

struct entry {
    n int
}

fn id_pair(p *entry) (*entry, bool) {
    return move p, true
}

fn main() {
    m := map[string]entry{"a": entry{n: 1}}
    p := &m["a"]
    var p2 *entry
    var ok bool
    p2, ok = id_pair(p)
    m["b"] = entry{n: 2}
    p2.n = 9
    fmt.println(ok)
    fmt.println(p2.n)
}
"#;
    let r2 = check(assign_src);
    assert_eq!(r2.verdict, Verdict::Reject, "{:?}", r2.diagnostics);
    assert!(
        error_codes(&r2).iter().any(|c| c == "HYB-E3102"),
        "expected HYB-E3102 for multi-value assign unpack of slot loan, got: {:?}",
        r2.diagnostics
    );
}

#[test]
fn closure_body_forwarded_and_unpacked_slot_loans_enforce_a3_and_t24() {
    let closure_a3_src = r#"
import "fmt"

struct entry {
    n int
}

fn id_pair(p *entry) (*entry, bool) {
    return move p, true
}

fn main() {
    run := fn() {
        m := map[string]entry{"a": entry{n: 1}}
        p := &m["a"]
        p2, ok := id_pair(p)
        m["b"] = entry{n: 2}
        p2.n = 9
        fmt.println(ok)
        fmt.println(p2.n)
    }
    run()
}
"#;
    let r1 = check(closure_a3_src);
    assert_eq!(r1.verdict, Verdict::Reject, "{:?}", r1.diagnostics);
    assert!(
        error_codes(&r1).iter().any(|c| c == "HYB-E3102"),
        "expected HYB-E3102 for closure multi-value unpacked slot loan, got: {:?}",
        r1.diagnostics
    );

    let closure_t24_src = r#"
import "fmt"

struct entry {
    n int
}

fn id_pair(p *entry) (*entry, bool) {
    return move p, true
}

fn main() {
    run := fn() {
        m := map[string]entry{"a": entry{n: 1}}
        p := &m["a"]
        p2, ok := id_pair(p)
        q := &p2
        fmt.println(ok)
        fmt.println(q)
    }
    run()
}
"#;
    let r2 = check(closure_t24_src);
    assert_eq!(r2.verdict, Verdict::Reject, "{:?}", r2.diagnostics);
    assert!(
        error_codes(&r2).iter().any(|c| c == "HYB-E3104"),
        "expected HYB-E3104 for address-of multi-value unpacked slot loan in closure, got: {:?}",
        r2.diagnostics
    );
}

#[test]
fn addr_of_forwarded_or_unpacked_slot_loan_is_rejected_e3104() {
    for snippet in [
        "p2 := p\n    q := &p2\n    fmt.println(q)",
        "p2 := id(p)\n    q := &p2\n    fmt.println(q)",
        "p2 := id(p)\n    q := &p2.n\n    fmt.println(q)",
        "p2, ok := id_pair(p)\n    q := &p2\n    fmt.println(ok)\n    fmt.println(q)",
    ] {
        let src = format!(
            r#"
import "fmt"

struct entry {{
    n int
}}

fn id(p *entry) *entry {{
    return move p
}}

fn id_pair(p *entry) (*entry, bool) {{
    return move p, true
}}

fn main() {{
    m := map[string]entry{{"a": entry{{n: 1}}}}
    p := &m["a"]
    {snippet}
}}
"#
        );
        let r = check(&src);
        assert_eq!(r.verdict, Verdict::Reject, "{:?}", r.diagnostics);
        assert!(
            error_codes(&r).iter().any(|c| c == "HYB-E3104"),
            "expected HYB-E3104 for `{snippet}`, got: {:?}",
            r.diagnostics
        );
    }
}

#[test]
fn primitive_result_of_multi_value_unpack_does_not_extend_slot_loan() {
    let src = r#"
import "fmt"

struct entry {
    n int
}

fn id_and_count(p *entry) (*entry, int) {
    return move p, 42
}

fn main() {
    m := map[string]entry{"a": entry{n: 1}}
    p := &m["a"]
    p2, n := id_and_count(p)
    p2.n = 9
    m["b"] = entry{n: 2}
    q := &n
    fmt.println(q)
}
"#;
    let r = check(src);
    assert_eq!(r.verdict, Verdict::Accept, "{:?}", r.diagnostics);
    assert!(r.diagnostics.is_empty(), "{:?}", r.diagnostics);
}
