# Foundation Decisions

Why Arca exists and core philosophy.

---

## 2026-03-28: Why Arca exists

**Problem:** PHP/Laravel project (63K lines, 75 models, Cloud Run) needs better language.

**Evaluated:** Kotlin (not strict enough), Gleam (syntax deviates from conventions), Rust (borrow checker overkill, slow compile), Scala (DX bad), OCaml (no web ecosystem), Go (no ADT/Result).

**Solution:** New language. ML type safety + Go ecosystem. Transpiles to Go for single binary, fast startup, full Go library access.

**Existing alternatives found:** Borgo (dead), Soppo (immature), Dingo (Go-syntax-bound, limits expression).

**Key user values:** Correctness, expressiveness, familiar conventions, Go's pragmatic benefits.
