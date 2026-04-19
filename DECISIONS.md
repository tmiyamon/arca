# Arca Design Decision Log

Full timeline of design discussions. Newest first. Details in topic files under `decisions/`.

Topics: [FFI](decisions/ffi.md) · [Types](decisions/types.md) · [Transpiler](decisions/transpiler.md) · [Toolchain](decisions/toolchain.md) · [Syntax](decisions/syntax.md) · [Ideas](decisions/ideas.md) · [Foundations](decisions/foundations.md)

---

## 2026-04-19

- [Option pointer-backed uniformly + two-stage IR direction (idea)](decisions/ideas.md#2026-04-19-two-stage-ir-and-option-as-pointer-backed-uniformly-idea) — Every `Option[T]` emits as `*T`. Collapse rule: when inner is `Ref[U]`/`Ptr[U]`, outer and inner share the same `*U` so the wrap is skipped. Otherwise helper `__ptrOf` wraps (handles non-addressable literals). Option-specific split machinery (`SplitNames` / `ExpandedValues` / `flattenArgs` / `resolveMatchBindings` / `expandFuncParams` Option cases) removed — Option flows through single-value paths uniformly. FFI `(T, bool)` returns wrap with `__optFrom`. `match opt` is uniformly `if subject != nil`. Aligns with spec drift where `design_ref_ptr_layers` already mandated pointer-backed primitive Option. Two-stage IR formalization (turning `expandResultOption` from annotating post-pass into a true rewrite) deferred to later slice; Result remains multi-return for now. Implemented.
- [Auto-Some — hint-driven implicit Option lift (idea)](decisions/ideas.md#2026-04-19-auto-some--hint-driven-implicit-option-lift-idea) — At hint-driven typed `Option[T]` positions, a value of type `T` is implicitly lifted into `Some(v)`. Single-layer only (`Option[Option[T]]` stays explicit). `None` and `&` never auto-inserted — explicit-first's spirit preserved by framing `Some` as information-neutral at typed positions. Implemented (`autoSomeLift` at the tail of `lowerExprHint`).
- [Error trait interface shape (idea)](decisions/ideas.md#2026-04-19-error-trait-interface-shape-idea) — Minimum `Error` trait with `fun message() -> String`. Compiler bridges to Go's `error` interface on emit. Named errors and wrap-chain semantics deferred.
- [Ref / Option / Ptr three-layer memory model (idea)](decisions/ideas.md#2026-04-19-ref--option--ptr-three-layer-memory-model-idea) — Separate safe `Ref<T>`, nullable `Option<T>`, and FFI-internal `Ptr<T>`. Users write `Ref<T>` and `Option<T>`; `Ptr<T>` stays compiler-internal. Immutability makes the model work without borrow checker. `?` becomes single-layer; monadic pipeline for Option↔Result conversion. Synthetic Builder (per-type, tag-driven) absorbs FFI mutation. Explicit-first principle (field/method auto-deref as the single exception). Replaces 2026-04-18 `*T` → Option auto-wrap.

## 2026-04-18

- [Go `*T` → Option auto-wrap](decisions/ffi.md#2026-04-18-go-t--option-auto-wrap) — Go pointer returns automatically wrapped in `IROptionType`. `*T` → `Option[*T]`, `(*T, error)` → `Result[Option[*T], Error]`. `?` double-unwraps with nil check. Implements systematic `*T` nullability rule from FFI boundary table.
- [try {} block](decisions/transpiler.md#2026-04-18-try-block) — `try { ... }` block expression creates a Result context for `?` in non-Result functions. Emitted as Go IIFE. HM inference via fresh type var + unify. `try` is not a keyword; only `try {` is recognized.
- [? compile error outside Result context](decisions/transpiler.md#2026-04-18--compile-error-outside-result-context) — `?` outside Result-returning functions and try blocks is now a compile error. Previously generated panic.
- [w.Unreachable()](decisions/transpiler.md#2026-04-18-wunreachable) — Exhaustive match Go compiler stubs separated from intentional panic via `w.Unreachable()`. Distinguishes "structurally impossible" from "runtime assertion".
- [IR naming: ErrorReturnValues + NilCheckReturnValues](decisions/transpiler.md#2026-04-18-ir-naming-errorreturnvalues--nilcheckreturnvalues) — `PropagateValues` renamed to `ErrorReturnValues`. Added `NilCheckReturnValues` for Option `?` unwrap paths.

## 2026-04-15

- [FFI has multiple boundaries, one per danger](decisions/ffi.md#2026-04-15-ffi-has-multiple-boundaries-one-mechanism-per-danger) — Per-danger boundaries, not a single universal mechanism. Rejects `@adapter`, public-API purity checks, separate `opaque` keyword, effect system, linear/typestate. Consistent with Rust per-call markers, Kotlin pragmatism, serde/Codable derive.
- [Phase ordering revised](decisions/foundations.md#2026-04-15-goal-framing--best-practical-backend-language) — Layer 1 completion before DB schema projection (previously Phase 1 head).
- [FFI design principle](decisions/foundations.md#2026-04-15-goal-framing--best-practical-backend-language) — "Arca makes guarantees. FFI is allowed only where guarantees can be preserved." Single rule mechanically decides nil/panic/`any`/mutation handling.
- [Two-layer architecture](decisions/foundations.md#2026-04-15-goal-framing--best-practical-backend-language) — Layer 1 (safety: nil/panic/any/mutation sealing) is prerequisite. Layer 2 (SSOT as derived) is byproduct. Layer 1 necessity comes from Layer 2's validity requirement, not from "safer than Go" pitch.
- [Goal framing — "best practical backend language"](decisions/foundations.md#2026-04-15-goal-framing--best-practical-backend-language) — Goal is internal compass; SSOT and safety are consequences, not identity. Positioning is the combination no competitor offers simultaneously (Go ops + Rust-level types + SSOT byproduct + easy curve).

## 2026-04-12

- [Symmetric boundary conversion (idea)](decisions/ideas.md#2026-04-12-symmetric-boundary-conversion--eliminate-synthetic-types-idea) — Make Go↔Arca type conversion bidirectional. Result/Option exist in IR only; generated Go uses native `(T, error)`, `error`, `(T, bool)`. Eliminates all synthetic types (`Result_`, `Option_`, `Ok_`, `Err_`). Blocked on traits (Error trait).
- [Go FFI nullable/pointer ambiguity (idea)](decisions/ideas.md#2026-04-12-go-ffi-nullablepointer-ambiguity-and-the-stdlib-boundary-idea) — Compiler can't auto-infer Go's nullable/pointer intent. Safe APIs go in stdlib (human-written wrappers); Go FFI stays as-is. Error representation: `Result[Unit, Error]` alias (leaning) vs `Option[Error]` (deferred). Blocked on traits.
- [Merge validate into lower](decisions/transpiler.md#2026-04-12-merge-validate-into-lower) — Delete validate.go, move structural checks (type existence, arg/field count, match exhaustiveness) into lower. Pipeline becomes Parse → Lower → Emit.
- [unify is the single type-check path](decisions/transpiler.md#2026-04-12-unify-is-the-single-type-check-path) — delete `irTypesMatch`, fold constraint compatibility into `Lowerer.unify`, `checkTypeHint` routes hints through HM. Fixes a silent `IRMapType` gap caught by Phase-1 fallback flip.
- [Lowerer.unify always reports](decisions/transpiler.md#2026-04-12-lowererunify-always-reports-substitution-only-uses-linferunify) — the silent flag is gone; type checks go through `l.unify(a, b, pos)`, raw substitution through `l.infer.unify(a, b)`. 2 dead unify sites deleted, 5 demoted to raw substitution.
- [if/else branch mismatch reported](decisions/transpiler.md#2026-04-12-lowererunify-always-reports-substitution-only-uses-linferunify) — covered by the unify cleanup; if/else promoted to reporting.
- [Arca generic constructors use HM](decisions/transpiler.md#2026-04-12-unify-arca-generic-constructors-with-go-ffi-hm) — per-call fresh type vars via `instantiateGenericType`, same shape as Go FFI. Removes `inferGoType` and the `isTypeParam` fallback.

## 2026-04-11

- [Unresolved type parameter detection](decisions/transpiler.md#2026-04-11-detect-unresolved-generic-type-parameters-at-the-binding-site) — `ErrCannotInferTypeParam` on `let todo = stdlib.BindJSON(req)?` when T can't be inferred; stops `interface{}` leaking to Go
- [if/match in value position](decisions/transpiler.md#2026-04-11-ifmatch-in-value-position-via-body-mode-refactor) — `let x = if ...` and `let x = match ...` now supported. Unified `emitBody(e, mode)` with return/void/assign leaves replaces `isReturn bool`
- [Unused package detection](decisions/transpiler.md#2026-04-11-unused-package-detection) — `ErrUnusedPackage` at Arca source position, flows through LSP diagnostics. `GoPackage` extended with Pos/SideEffect/Used instead of parallel maps
- [Arca package system](decisions/toolchain.md#2026-04-11-arca-package-system) — Built-in packages bundled via go:embed. `import stdlib` works without go.mod
- [Trait system design (proposal)](decisions/ideas.md#2026-04-11-trait-system-design-proposal) — Kotlin/Swift hybrid, `impl User: Display`, `&` for multiple bounds. Design only, not implemented
- [LSP features](decisions/toolchain.md#2026-04-11-lsp-features) — Hover, diagnostics, go-to-definition, completion. Per-session resolver cache for speed
- [Map type](decisions/syntax.md#2026-04-11-map-type) — `Map[K, V]` with `{k: v}` literal, immutable, lowered to Go map[K]V
- [Explicit type args + HM generics](decisions/transpiler.md#2026-04-11-explicit-type-args-and-hm-for-go-generics) — `f[T](args)` syntax, Go generic calls use HM unification uniformly

## 2026-04-10

- [Arrow convention](decisions/syntax.md#2026-04-10-arrow-convention) — Scala-style: -> for types, => for values (match arms + lambdas)

## 2026-04-09

- [HM type inference](decisions/transpiler.md#2026-04-09-hm-type-inference) — Type variables, unification, resolution pass. Ok/Error/None/empty list infer from usage
- [If expression](decisions/syntax.md#2026-04-09-if-expression) — if/else as expression with branch type unification
- [Index access](decisions/syntax.md#2026-04-09-index-access) — expr[index] for list element access
- [Shorthand lambda](decisions/syntax.md#2026-04-09-shorthand-lambda) — x -> body and (x, y) -> body without type annotations
- [GoWriter integration into emit](decisions/transpiler.md#2026-04-09-gowriter-integration-into-emit) — Replace manual writeln+indent with structured GoWriter methods. gofmt-normalized snapshots

## 2026-04-08

- [Bidirectional type checking](decisions/transpiler.md#2026-04-08-bidirectional-type-checking) — lowerExprHint propagates expected types. Lambda inference, constraint compat, validate cleanup

## 2026-04-07

- [Undefined variable detection and IR type propagation](decisions/transpiler.md#2026-04-07-undefined-variable-detection-and-ir-type-propagation) — Detect undefined vars via scope, fix Ok/Error/Some type inference
- [Sum type method expansion as IR post-pass](decisions/transpiler.md#2026-04-07-sum-type-method-expansion-as-ir-post-pass) — Lower as normal method, expand to per-variant in post-pass
- [Lexical scope tree](decisions/toolchain.md#2026-04-07-lexical-scope-tree-for-symbol-resolution) — Scope tree with positions for LSP scope-aware symbol lookup. Replaces flat varNames/varIRTypes

## 2026-04-06

- [Triple-quoted multiline strings](decisions/syntax.md#2026-04-06-triple-quoted-multiline-strings) — `"""..."""` with interpolation, indent stripping, backtick emit
- [Tag-based snapshot and migration](decisions/ideas.md#2026-04-06-tag-based-snapshot-and-migration-system-idea) — Per-tag snapshots, diff, check. Arca outputs IR, libraries handle concrete formats. Idea

## 2026-04-05

- [Unified IRMatch + IRMatchPattern](decisions/transpiler.md#2026-04-05-unified-irmatch--irmatchpattern-design) — 6 match types → single IRMatch with typed patterns
- [Unit in void context](decisions/transpiler.md#2026-04-05-unit-in-void-context-generates-unused-value) — `Ok(_) -> Unit` emitted unused `struct{}{}`. Solved with IRVoidExpr + condition inversion
- [Project structure and build pipeline](decisions/toolchain.md#2026-04-05-project-structure-and-build-pipeline) — go.mod at project root, build/ copies it, go get removed from pipeline
- [Go package availability detection](decisions/toolchain.md#2026-04-05-go-package-availability-detection-open-problem--solved-by-project-structure) — module cache masked missing packages → solved by project-level go.mod
- [Dependency management](decisions/toolchain.md#2026-04-05-dependency-management--gomod-now-arca-packages-later) — go.mod now, Arca package system later
- [Go FFI Result auto-wrapping](decisions/ffi.md#2026-04-05-go-ffi-return-type--result-auto-wrapping) — GoMultiReturn flag, tuple → Result mechanical conversion. Implemented
- [DB migration from types](decisions/ideas.md#2026-04-05-db-migration-from-types-idea) — DDL IR from type definitions. Idea

## 2026-04-04

- [Move type checking to IR](decisions/transpiler.md#2026-04-04-move-type-checking-to-ir) — parse → lower (error-tolerant) → validate IR → emit
- [IR implementation](decisions/transpiler.md#2026-04-04-ir-implementation-ast--ir--go) — ir.go + lower.go + emit.go, value-before-declare for shadowing
- [IR introduction](decisions/transpiler.md#2026-04-04-ir-intermediate-representation-introduction) — AST → IR → Go output. Structural guarantees over ad-hoc detection
- [LSP server](decisions/toolchain.md#2026-04-04-lsp-server-implementation) — glsp, diagnostics + hover, Scope.onDefine for symbol recording
- [Go type integration](decisions/ffi.md#2026-04-04-go-type-integration-via-gotypes) — go/types via TypeResolver interface. Phases 1-4 complete
- [Sum type methods](decisions/ffi.md#2026-04-04-sum-type-methods--per-variant-expansion) — match self expanded to per-variant Go methods
- [JSON serialize/deserialize](decisions/ideas.md#2026-04-04-json-serializedeserialize-idea) — auto-generation from tags. Idea
- [Record copy/spread](decisions/ideas.md#2026-04-04-record-copyspread-idea) — .copy() or spread syntax. Idea
- [Constrained field auto-construction](decisions/ideas.md#2026-04-04-constrained-field-auto-construction-future) — auto Email from String. Future
- [Variable shadowing](decisions/types.md#2026-04-04-variable-shadowing-in-codegen) — suffixed names (email_2) for shadowed variables
- [Constructor returns Result](decisions/types.md#2026-04-04-constrained-type-constructor-returns-result-without-) — Email("a@b.c") without ? wraps in Result
- [Builtin println/print](decisions/transpiler.md#2026-04-04-builtin-printlnprint) — no import needed, auto-imports fmt
- [Associated functions and Self](decisions/types.md#2026-04-04-associated-functions-static-fun-and-self) — static fun keyword, Self type reference

## 2026-04-03

- [Qualified constructors](decisions/types.md#2026-04-03-qualified-constructor-syntax--arca-init) — Type.Ctor() for sum types/enums + arca init command
- [Constraint compatibility](decisions/types.md#2026-04-03-constraint-compatibility-level-2) — dimension-based normalization, stricter → wider allowed

## 2026-04-02

- [Type alias codegen](decisions/types.md#2026-04-02-type-alias-codegen) — defined types + NewType() constructor
- [let type annotation](decisions/types.md#2026-04-02-let-type-annotation) — let x: Type = expr for empty collections
- [struct tags — tags block](decisions/syntax.md#2026-04-02-struct-tags--implemented-as-tags-block) — tags { json, db(snake) } separate from constraints

## 2026-03-31

- [1:1 file mapping](decisions/toolchain.md#2026-03-31-11-file-mapping-and-visibility) — each .arca → .go, pub = package-level
- [Package and import system](decisions/syntax.md#2026-03-31-package-and-import-system) — directory = package, qualified/selective/alias imports
- [Entry point resolution](decisions/toolchain.md#2026-03-31-entry-point-resolution) — arca build finds main.arca
- [.go accessor idea](decisions/ideas.md#2026-03-31-go-accessor-idea) — raw Go access boundary marker. Idea
- [struct tags rethinking](decisions/syntax.md#2026-03-31-struct-tags--rethinking-superseded-by-2026-04-02) — superseded by tags block

## 2026-03-30

- [Pipe operator — keeping it](decisions/syntax.md#2026-03-30-pipe-operator--keeping-it) — methods for domain, pipe for collections (Go generics constraint)
- [struct tags from constrained types](decisions/syntax.md#2026-03-30-struct-tags-from-constrained-types-superseded-by-2026-04-02) — superseded by tags block
- [Short record syntax](decisions/syntax.md#2026-03-30-short-record-syntax) — type User(name: String)
- [& operator](decisions/ffi.md#2026-03-30--operator-for-go-ffi) — immutability boundary marker for Go FFI
- [Unit type](decisions/syntax.md#2026-03-30-unit-type) — Result[Unit, error] for error-only functions
- [let _](decisions/syntax.md#2026-03-30-let-_-for-discarding-values) — explicit discard
- [Import redesign](decisions/syntax.md#2026-03-30-import-redesign--string-literals) — import go "path" with string literal
- [fun keyword](decisions/syntax.md#2026-03-30-function-keyword--fun) — fn → fun (Kotlin philosophy)
- [camelCase](decisions/syntax.md#2026-03-30-naming-convention--camelcase) — match Go conventions
- [Methods](decisions/types.md#2026-03-30-methods--decided-to-add) — implicit self, types express domains
- [Constrained types — levels](decisions/types.md#2026-03-30-constrained-types--levels-and-scope) — 7 levels identified, opt-in
- [Constrained types — syntax](decisions/types.md#2026-03-30-constrained-types--syntax-) — {} for constraints
- [Constrained types — design](decisions/types.md#2026-03-30-constrained-types--design-decisions) — two layers, alias reuse, Result constructor
- [Static vs runtime constraints](decisions/types.md#2026-03-30-static-vs-runtime-constraints) — Arca = values, Go = execution
- [defer](decisions/syntax.md#2026-03-30-defer--decided-not-to-add-then-added) — initially rejected, then added pragmatically

## 2026-03-29

- [Pipe vs methods](decisions/syntax.md#2026-03-29-pipe-operator-vs-methods-superseded-by-2026-03-30-keeping-it) — superseded by "keeping it"
- [UFCS rejected](decisions/syntax.md#2026-03-29-ufcs-uniform-function-call-syntax--rejected) — prefer explicit methods
- [interface/trait deferred](decisions/syntax.md#2026-03-29-interfacetrait--deferred) — revisit when io.Reader blocks
- [Builtin name shadowing](decisions/types.md#2026-03-29-builtin-names-okerrorsomenone--shadowing) — Ok/Error shadow like Rust
- [Result/Option as struct](decisions/types.md#2026-03-29-resultoption-as-struct-not-pointer) — no nil leaking

## 2026-03-28

- [Import syntax — dot separator](decisions/syntax.md#2026-03-28-import-syntax--dot-separator-superseded-by-2026-03-30-string-literals) — superseded by string literals
- [Go FFI — opaque](decisions/ffi.md#2026-03-28-go-ffi--opaque-no-type-checking) — later revisited with go/types integration
- [Immutability](decisions/types.md#2026-03-28-immutability--fully-immutable-go-types-opaque) — Arca immutable, Go types opaque
- [Initial spec](decisions/syntax.md#2026-03-28-language-spec--initial-decisions) — type params [], ?, lambda, let, |>, pub
- [Why Arca exists](decisions/foundations.md#2026-03-28-why-arca-exists) — ML type safety + Go ecosystem
