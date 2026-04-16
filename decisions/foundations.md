# Foundation Decisions

Why Arca exists and core philosophy.

---

## 2026-04-15: Goal framing — "best practical backend language"

**Goal (internal compass):** Be the most practical language for backend development.

Everything else is consequence, not identity:

- **Layer 1 — Safety prerequisite.** To stand on top of Go at all, Arca must seal Go's root dangers (nil panic, typed nil, `interface{}` trap, panic leak, zero value surprise, unintended mutation). Without this, any downstream value proposition is undermined. This is table stakes, not differentiation.
- **Layer 2 — SSOT as derived property.** Type-driven + constrained types + backend focus naturally yield Single Source of Truth: model definition → OpenAPI / DB schema / validator / (future: client SDK, form, event schema). SSOT emerges from the design; it isn't the stated goal.
- Go runtime (cold start, single binary, compile speed, ecosystem): picked because it serves practicality on deployment.
- ML-level types (ADT, HM inference, constrained types): picked because they serve practicality on correctness.
- Low learning curve (Kotlin-level): picked because impractical learning kills adoption.

**Positioning note.** "Most practical backend language" is the compass but not a standalone pitch (every language claims practicality). External positioning is the *combination no competitor offers at once*: Go operational characteristics + Rust-level type expressiveness + SSOT as derived byproduct + easy learning curve. Competitors each compromise on one axis; Arca targets the intersection.

**Design principle derived from the goal.** *Arca makes guarantees. FFI is only allowed to the extent that guarantees can be preserved across it.* This single rule mechanically decides Layer 1 questions (nil → `Option[T]`, panic → `Result`, `any` → safe cast, mutation → Builder/Freeze).

**Phase ordering.** Layer 1 safety completion precedes Layer 2 projection work. An SSOT built on unsealed Go dangers is unreliable: derivations would project invalid values.

---

## 2026-03-28: Why Arca exists

**Problem:** PHP/Laravel project (63K lines, 75 models, Cloud Run) needs better language.

**Evaluated:** Kotlin (not strict enough), Gleam (syntax deviates from conventions), Rust (borrow checker overkill, slow compile), Scala (DX bad), OCaml (no web ecosystem), Go (no ADT/Result).

**Solution:** New language. ML type safety + Go ecosystem. Transpiles to Go for single binary, fast startup, full Go library access.

**Existing alternatives found:** Borgo (dead), Soppo (immature), Dingo (Go-syntax-bound, limits expression).

**Key user values:** Correctness, expressiveness, familiar conventions, Go's pragmatic benefits.
