# Arca Design Decision Log

Full timeline of design discussions. Newest first. Details in topic files under `decisions/`.

Topics: [FFI](decisions/ffi.md) · [Types](decisions/types.md) · [Transpiler](decisions/transpiler.md) · [Toolchain](decisions/toolchain.md) · [Syntax](decisions/syntax.md) · [Ideas](decisions/ideas.md) · [Foundations](decisions/foundations.md)

---

## 2026-04-07

- [Lexical scope tree](decisions/toolchain.md#2026-04-07-lexical-scope-tree-for-symbol-resolution) — Scope tree with positions for LSP scope-aware symbol lookup. Replaces flat varNames/varIRTypes

## 2026-04-06

- [Triple-quoted multiline strings](decisions/syntax.md#2026-04-06-triple-quoted-multiline-strings) — `"""..."""` with interpolation, indent stripping, backtick emit
- [Tag-based snapshot and migration](decisions/ideas.md#2026-04-06-tag-based-snapshot-and-migration-system-idea) — Per-tag snapshots, diff, check. Arca outputs IR, libraries handle concrete formats. Idea

## 2026-04-05

- [Unified IRMatch + IRMatchPattern](decisions/transpiler.md#2026-04-05-unified-irmatch--irmatchpattern-design) — 6 match types → single IRMatch with typed patterns
- [Unit in void context](decisions/transpiler.md#2026-04-05-unit-in-void-context-generates-unused-value-open-bug) — `Ok(_) -> Unit` emits unused `struct{}{}`. Open bug
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
