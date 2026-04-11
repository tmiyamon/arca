# Toolchain Decisions

LSP, project structure, build pipeline, dependency management. Newest first.

---

## 2026-04-11: LSP features

**Context:** LSP had hover and diagnostics, but no go-to-definition or completion. Real editor integration needs more.

**Decision:** Add go-to-definition and completion, optimized for speed.

**Go-to-definition:**
- `SymbolInfo.Pos` records definition position; set via `SymbolRegInfo.Pos` in `registerSymbol` for variables, parameters, let bindings, packages, functions.
- `FnDecl.NamePos` (position of function name) separate from `Pos` (position of `fun` keyword, used for scope start). Definition jumps to `NamePos`.
- `TypeDecl.Pos` for type name position.
- Go FFI definition: `TypeResolver.MemberPos` and `MethodPos` return file+position from `go/types` object's `Pos()` via the cached `FileSet`. `GoTypeResolver.loadPackageFull` caches both `*types.Package` and `*token.FileSet`.
- Arca stdlib definitions: `resolveEmbedFilePath` maps embed paths (e.g. `stdlib/db.go`) to a persistent cache at `~/.cache/arca/packages/` so editors can open real files.

**Completion:**
- Triggered by `.`. Parser is re-invoked on source with placeholder identifier inserted after dangling dots so incomplete expressions parse successfully.
- `getReceiverBeforeDot` extracts the full dotted expression before the cursor (handles chained access `a.b.c`).
- Receiver is parsed and lowered to get its IR type; the IR field-type resolution naturally handles chains.
- `self` is registered as a symbol with the receiver type in method bodies, enabling `self.` completion.
- `TypeResolver.PackageMembers` and `TypeMembers` list exported Go package members and type methods/fields for completion items.

**Performance:**
- `resolverCache` keyed by goModDir: one `GoTypeResolver` per session, reused across hover/definition/completion. Avoids re-running `packages.Load` on every request. Benchmarked: 137ms → 88μs (1500x faster for completion).

**Status:** Done

---

## 2026-04-11: Arca package system

**Context:** stdlib was treated as a Go module (`import go "github.com/tmiyamon/arca/stdlib"`), forcing users to set up go.mod and `go get` to use it. Other languages bundle stdlib with the toolchain — Python, Rust, Go itself. Arca should do the same.

**Decision:** Introduce an Arca package system, parallel to Go modules. Built-in packages are bundled with the arca binary via `go:embed`. `import stdlib` works in any script without go.mod setup.

**Implementation:**
- `arca_packages.go` defines `ArcaPackage` registry; `stdlib` is the first entry
- Embed stdlib source via `//go:embed stdlib/*.go`
- Parser unchanged; `lookupArcaPackage` distinguishes Arca packages from same-directory module files
- `GoTypeResolver.loadPackage` detects Arca packages and uses `loadArcaPackageTypes` (parses embedded sources with `go/parser` + `go/types` directly, no temp dir)
- `writeBuildDir` extracts used Arca packages to `build/<pkg>/` and adds `require` + `replace` directives so `go run` succeeds

**Three import kinds coexist:**
- `import go "fmt"` — Go FFI (existing)
- `import stdlib` — Arca built-in package (new)
- `import user` — Arca module (same-directory file, existing)

**Benefits:**
- `arca run script.arca` with stdlib usage works without project setup
- Type resolution is fully in-memory for Arca packages
- Foundation for future external Arca packages (third-party libraries)

**Status:** Done

---

## 2026-04-07: Lexical scope tree for symbol resolution

**Context:** LSP hover showed wrong type for shadowed variables (e.g. match arm `Error(e)` showed `*echo.Echo` from outer `let e`). Flat symbol list had no scoping — `LookupSymbol` returned last-defined symbol regardless of position.

**Decision:** Lexical scope tree with positions.

- `Scope` struct with `parent`, `Children`, `StartPos`, `EndPos`, `symbols`, `declCount`
- `withScope(startPos, endPos, symbols, fn)` manages push/pop + initial symbol registration
- All variable bindings go through `registerSymbol` → `Scope.Define`
- Scope tree preserved after lowering (`rootScope`)
- `FindSymbolAt(name, pos)` traverses tree to find innermost scope containing cursor
- LSP hover uses `FindSymbolAt` instead of flat `LookupSymbol`
- `SymbolInfo` carries both `ArcaType` and `IRType` (unified from `varArcaTypes`/`varIRTypes`)
- Block AST node records `{`/`}` positions for scope boundaries

**Replaces:** `declaredVars`, `varNames`, `varArcaTypes`, `varIRTypes` maps on Lowerer. All unified into `Scope.symbols`.

**Pending:** None. Match arm bindings now registered via `registerSymbol` with `withScope`.

**Status:** Implemented.

---

## 2026-04-05: Project structure and build pipeline

**Context:** The transpiler pipeline generates go.mod in a temporary build/ directory and runs `go get` during build. This causes two problems: (1) TypeResolver runs before `go get`, so external packages can't be resolved — type info is missing, GoMultiReturn not set, wrong code generated. (2) Module cache can make TypeResolver succeed even without `go get`, causing inconsistent behavior.

**Decision: Arca projects use go.mod at the project root. Go-standard dependency management.**

Project structure:
```
myproject/
  go.mod          ← arca init creates, user manages with go get
  main.arca
  build/          ← generated .go files + copied go.mod/go.sum
    go.mod        ← copy of parent's go.mod
    go.sum        ← copy of parent's go.sum
    main.go
```

Pipeline:
```
1. parse
2. Find go.mod by walking up from .arca file
3. TypeResolver init (dir = go.mod directory)
4. lower + validate
5. emit
6. write build/ (.go files + copy go.mod/go.sum)
7. go build/run in build/
```

Key decisions:
- **go.mod at project root**: user manages with `go get`, same as Go projects
- **build/ inside project**: conventional (like Rust target/, TS dist/)
- **build/go.mod**: copy of parent, not generated. Module name read from go.mod, not hardcoded
- **go get removed from pipeline**: user responsibility, like Go itself
- **go.mod discovery**: walk up from .arca file to find nearest go.mod (same as Go toolchain)
- **No go.mod = stdlib only**: TypeResolver initialized without dir, external packages produce error
- **Missing package UX**: list all unresolvable packages, suggest `go get pkg1 pkg2 ...`
- **arca init**: creates go.mod via `go mod init`
- **examples/ with external deps**: separate project directories with own go.mod
- **emit / LSP**: same pipeline, benefits automatically from TypeResolver improvements

**Future:** Arca will likely need its own package system for transpiler plugins, tag extensions, Arca-native libraries. Format TBD — decide when the first Arca library is needed.

**Status:** Implemented. go.mod discovery, GoPackage struct, go.mod require check, build/ copies go.mod/go.sum.

---

## 2026-04-05: Go package availability detection (open problem → solved by project structure)

**Context:** `examples/todo.arca` imports `github.com/labstack/echo/v5`. The package was not `go get`-ed for the current project, but existed in the Go module cache (`~/go/pkg/mod/`) from another project. `GoTypeResolver` (via `go/packages.Load`) resolved the package from cache and returned type information successfully — but `go build` failed because the package was not in `go.mod`.

**Problem:** TypeResolver succeeding does not mean the build will succeed. The module cache makes it impossible to distinguish "package is properly in go.mod" from "package happens to be cached".

**Attempted fix:** Added `PackageAvailable` check to detect whether a package is in `go.mod`. Reverted because the cache made the detection unreliable.

**Root cause:** The real problem was that the pipeline ran `go get` AFTER TypeResolver (step 6 vs step 2). TypeResolver couldn't resolve packages that weren't yet available. The module cache masked this by sometimes resolving packages that weren't in the project's go.mod.

**Solution:** Restructure so that go.mod exists at the project root BEFORE transpilation. TypeResolver uses the project's go.mod via `packages.Config.Dir`. User manages dependencies with `go get` upfront, like Go itself.

---

## 2026-04-05: Dependency management — go.mod now, Arca packages later

**Decision:** Use go.mod for dependency management. Arca projects are Go modules.

**Future:** Arca will likely need its own package system. Use cases: transpiler plugins, tag system extensions, Arca-native libraries that need transpilation before use. These don't fit in go.mod because they have a different lifecycle (transpile → then compile). Format TBD (`arca.toml`, go.mod extension, etc.) — decide when the first Arca library is needed.

---

## 2026-04-04: LSP server implementation

**Context:** Editing Arca without IDE support is painful. Errors only appear at compile time. No hover, no go-to-definition, no completion.

**Decision: Implement LSP server using `github.com/tliron/glsp`.**

- Command: `arca lsp` (stdio transport)
- Works with VS Code and Neovim out of the box

**Phases:**
- Phase A: Diagnostics — parse/type errors shown in editor on save/change ✅
- Phase B: Hover — show type info at cursor position ✅
- Phase C: Go FFI type tracking (Phase 3/4) — method/field resolution for Go types ✅ (TypeResolver now passed to LSP)

**Why glsp:** Go library that handles JSON-RPC and LSP protocol dispatch. Avoids writing ~500 lines of protocol boilerplate. LSP spec is stable so dependency risk is low.

**Architecture:** LSP server reuses existing pipeline (parse → check → lower). IR carries type info for hover. TypeResolver provides Go FFI type info.

**Symbol recording:** Initially symbols were recorded manually at each binding site (`recordSymbol` calls). This was error-prone — Lambda params, ForExpr bindings, etc. were missed. Refactored to use `Scope.onDefine` callback: any `scope.Define()` call automatically records the symbol for LSP. New binding points are covered automatically.

**Hover coverage:**
- Functions, types, type aliases — from checker's global maps
- Methods and associated functions — from type declaration method lists
- Local variables, parameters — from Scope.onDefine callback
- Match pattern bindings (Ok/Error/Some/constructor fields) — from Scope.onDefine
- `Result[T]` treated as `Result[T, error]` for Error pattern binding

**Status:** Phase A+B+C complete. LSP uses GoTypeResolver with project go.mod for full Go FFI type resolution.

---

## 2026-03-31: Entry point resolution

**Context:** Needed `arca build` to work without specifying file every time.

**Decision:** Three patterns:
- `arca build` → finds `main.arca` in current directory
- `arca build cmd/server` → finds `main.arca` in directory
- `arca build main.arca` → direct file (backwards compat)

Follows Go convention. Package system deferred — currently 1 file = 1 module, directory is just structure.

---

## 2026-03-31: 1:1 file mapping and visibility

**Context:** Previously all same-directory .arca files merged into one .go file. Changed to 1 .arca = 1 .go.

**Decision:** Each .arca generates its own .go file. Same-directory files share `package main`. Sub-directory modules get separate Go package.

**pub = package-level visibility (not file-level).** Same as Go. Same directory can access non-pub functions. If file-level privacy needed, move to separate directory.

**Why:** Go compiler handles same-package type resolution across files. Simpler than merging. Easier to debug (1:1 source mapping).
