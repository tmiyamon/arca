package main

import (
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"
)

// loadedPackage holds a type-checked package along with its fileset for position lookup.
type loadedPackage struct {
	types *types.Package
	fset  *token.FileSet
}

// GoTypeResolver implements TypeResolver using go/types.
// It loads Go package type information and caches it.
type GoTypeResolver struct {
	mu    sync.Mutex
	cache map[string]*loadedPackage // import path → loaded package
	dir   string                    // project directory (for go.mod resolution)
}

func NewGoTypeResolver(dir string) *GoTypeResolver {
	return &GoTypeResolver{
		cache: make(map[string]*loadedPackage),
		dir:   dir,
	}
}

func (r *GoTypeResolver) loadPackage(pkg string) *types.Package {
	lp := r.loadPackageFull(pkg)
	if lp == nil {
		return nil
	}
	return lp.types
}

func (r *GoTypeResolver) loadPackageFull(pkg string) *loadedPackage {
	r.mu.Lock()
	defer r.mu.Unlock()

	if cached, ok := r.cache[pkg]; ok {
		return cached
	}

	// Arca built-in packages: load from embed.FS via go/parser + go/types
	for _, ap := range arcaPackages {
		if pkg == ap.GoModPath {
			tp, fset := loadArcaPackageTypes(&ap)
			lp := &loadedPackage{types: tp, fset: fset}
			r.cache[pkg] = lp
			return lp
		}
	}

	cfg := &packages.Config{
		Mode: packages.NeedTypes | packages.NeedName | packages.NeedSyntax | packages.NeedTypesInfo,
		Dir:  r.dir,
	}
	pkgs, err := packages.Load(cfg, pkg)
	if err != nil || len(pkgs) == 0 || pkgs[0].Types == nil || len(pkgs[0].Errors) > 0 {
		r.cache[pkg] = nil
		return nil
	}

	lp := &loadedPackage{types: pkgs[0].Types, fset: pkgs[0].Fset}
	r.cache[pkg] = lp
	return lp
}

// MemberPos returns the file and position where a package-level member is defined.
// Returns ("", Pos{}) if not found.
func (r *GoTypeResolver) MemberPos(pkg, name string) (string, Pos) {
	lp := r.loadPackageFull(pkg)
	if lp == nil || lp.types == nil || lp.fset == nil {
		return "", Pos{}
	}
	obj := lp.types.Scope().Lookup(name)
	if obj == nil {
		return "", Pos{}
	}
	position := lp.fset.Position(obj.Pos())
	if !position.IsValid() {
		return "", Pos{}
	}
	return position.Filename, Pos{Line: position.Line, Col: position.Column}
}

// PackageMembers lists exported package-level members of a package.
func (r *GoTypeResolver) PackageMembers(pkg string) []MemberInfo {
	goPkg := r.loadPackage(pkg)
	if goPkg == nil {
		return nil
	}
	var members []MemberInfo
	scope := goPkg.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		var kind, detail string
		switch o := obj.(type) {
		case *types.Func:
			kind = "func"
			detail = o.Type().String()
		case *types.TypeName:
			kind = "type"
			detail = obj.Type().String()
		case *types.Var:
			kind = "var"
			detail = o.Type().String()
		case *types.Const:
			kind = "const"
			detail = o.Type().String()
		default:
			continue
		}
		members = append(members, MemberInfo{Name: name, Kind: kind, Detail: detail})
	}
	return members
}

// TypeMembers lists exported methods and fields of a named type in a package.
func (r *GoTypeResolver) TypeMembers(pkg, typeName string) []MemberInfo {
	goPkg := r.loadPackage(pkg)
	if goPkg == nil {
		return nil
	}
	obj := goPkg.Scope().Lookup(typeName)
	if obj == nil {
		return nil
	}
	named, ok := obj.Type().(*types.Named)
	if !ok {
		return nil
	}
	var members []MemberInfo
	// Methods
	for i := 0; i < named.NumMethods(); i++ {
		m := named.Method(i)
		if !m.Exported() {
			continue
		}
		members = append(members, MemberInfo{
			Name:   m.Name(),
			Kind:   "method",
			Detail: m.Type().String(),
		})
	}
	// Fields (for struct types)
	if st, ok := named.Underlying().(*types.Struct); ok {
		for i := 0; i < st.NumFields(); i++ {
			f := st.Field(i)
			if !f.Exported() {
				continue
			}
			members = append(members, MemberInfo{
				Name:   f.Name(),
				Kind:   "field",
				Detail: f.Type().String(),
			})
		}
	}
	return members
}

// MethodPos returns the file and position of a method on a type in a package.
func (r *GoTypeResolver) MethodPos(pkg, typeName, method string) (string, Pos) {
	lp := r.loadPackageFull(pkg)
	if lp == nil || lp.types == nil || lp.fset == nil {
		return "", Pos{}
	}
	obj := lp.types.Scope().Lookup(typeName)
	if obj == nil {
		return "", Pos{}
	}
	named, ok := obj.Type().(*types.Named)
	if !ok {
		return "", Pos{}
	}
	for i := 0; i < named.NumMethods(); i++ {
		m := named.Method(i)
		if m.Name() == method {
			position := lp.fset.Position(m.Pos())
			if position.IsValid() {
				return position.Filename, Pos{Line: position.Line, Col: position.Column}
			}
		}
	}
	return "", Pos{}
}

func (r *GoTypeResolver) ResolveFunc(pkg, name string) *FuncInfo {
	goPkg := r.loadPackage(pkg)
	if goPkg == nil {
		return nil
	}

	obj := goPkg.Scope().Lookup(name)
	if obj == nil {
		return nil
	}

	fn, ok := obj.(*types.Func)
	if !ok {
		return nil
	}

	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return nil
	}

	return sigToFuncInfo(sig)
}

func (r *GoTypeResolver) ResolveType(pkg, name string) *TypeInfo {
	goPkg := r.loadPackage(pkg)
	if goPkg == nil {
		return nil
	}

	obj := goPkg.Scope().Lookup(name)
	if obj == nil {
		return nil
	}

	named, ok := obj.Type().(*types.Named)
	if !ok {
		return nil
	}

	info := &TypeInfo{}

	switch named.Underlying().(type) {
	case *types.Struct:
		info.Kind = TypeInfoStruct
	case *types.Interface:
		info.Kind = TypeInfoInterface
	case *types.Basic:
		info.Kind = TypeInfoBasic
	default:
		info.Kind = TypeInfoOther
	}

	// Collect methods
	mset := types.NewMethodSet(named)
	for i := 0; i < mset.Len(); i++ {
		info.Methods = append(info.Methods, mset.At(i).Obj().Name())
	}

	// Collect struct fields
	if strct, ok := named.Underlying().(*types.Struct); ok {
		for i := 0; i < strct.NumFields(); i++ {
			f := strct.Field(i)
			info.Fields = append(info.Fields, FieldInfo{
				Name: f.Name(),
				Type: f.Type().String(),
			})
		}
	}

	return info
}

func (r *GoTypeResolver) ResolveMethod(pkg, typ, method string) *FuncInfo {
	goPkg := r.loadPackage(pkg)
	if goPkg == nil {
		return nil
	}

	obj := goPkg.Scope().Lookup(typ)
	if obj == nil {
		return nil
	}

	// Try both value and pointer receiver
	for _, t := range []types.Type{obj.Type(), types.NewPointer(obj.Type())} {
		mset := types.NewMethodSet(t)
		for i := 0; i < mset.Len(); i++ {
			sel := mset.At(i)
			if sel.Obj().Name() == method {
				if fn, ok := sel.Obj().(*types.Func); ok {
					if sig, ok := fn.Type().(*types.Signature); ok {
						return sigToFuncInfo(sig)
					}
				}
			}
		}
	}

	return nil
}

func (r *GoTypeResolver) ResolveUnderlying(goType string) string {
	// Split "github.com/labstack/echo/v5.HandlerFunc" → pkg + name
	dotIdx := strings.LastIndex(goType, ".")
	if dotIdx < 0 {
		return ""
	}
	pkgPath := goType[:dotIdx]
	typeName := goType[dotIdx+1:]

	goPkg := r.loadPackage(pkgPath)
	if goPkg == nil {
		return ""
	}
	obj := goPkg.Scope().Lookup(typeName)
	if obj == nil {
		return ""
	}
	underlying := obj.Type().Underlying()
	if underlying == nil {
		return ""
	}
	return underlying.String()
}

func (r *GoTypeResolver) CanLoadPackage(pkg string) bool {
	if isStdLib(pkg) {
		return r.loadPackage(pkg) != nil
	}
	// Arca built-in packages: bundled with arca binary
	for _, ap := range arcaPackages {
		if pkg == ap.GoModPath {
			return true
		}
	}
	// Same-module subpackage: always available
	if r.isSameModule(pkg) {
		return true
	}
	// Non-stdlib: check go.mod require entries, not just module cache
	return r.isInGoMod(pkg)
}

// isSameModule checks if a package is a subpackage of the current module.
func (r *GoTypeResolver) isSameModule(pkg string) bool {
	if r.dir == "" {
		return false
	}
	moduleName := readGoModuleName(r.dir)
	return moduleName != "" && strings.HasPrefix(pkg, moduleName+"/")
}

// isInGoMod checks if a package path is required in the project's go.mod.
func (r *GoTypeResolver) isInGoMod(pkg string) bool {
	if r.dir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(r.dir, "go.mod"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		// Match "require" lines: both single-line and block form
		// Single: require github.com/foo/bar v1.0.0
		// Block:  \tgithub.com/foo/bar v1.0.0
		if strings.HasPrefix(line, "require ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 && isModuleMatch(pkg, parts[1]) {
				return true
			}
		}
		// Inside require block: lines start with module path
		if strings.Contains(line, "/") && !strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "module ") && !strings.HasPrefix(line, "go ") && !strings.HasPrefix(line, "require") && !strings.HasPrefix(line, ")") {
			parts := strings.Fields(line)
			if len(parts) >= 2 && isModuleMatch(pkg, parts[0]) {
				return true
			}
		}
	}
	return false
}

// isModuleMatch checks if a package import path belongs to a module.
// e.g. "github.com/labstack/echo/v5" matches module "github.com/labstack/echo/v5"
// and "github.com/labstack/echo/v5/middleware" also matches.
func isModuleMatch(pkg, module string) bool {
	return pkg == module || strings.HasPrefix(pkg, module+"/")
}

func sigToFuncInfo(sig *types.Signature) *FuncInfo {
	info := &FuncInfo{
		Variadic: sig.Variadic(),
	}

	// Extract type parameters for generic functions
	if tparams := sig.TypeParams(); tparams != nil {
		for i := 0; i < tparams.Len(); i++ {
			info.TypeParams = append(info.TypeParams, tparams.At(i).Obj().Name())
		}
	}

	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		p := params.At(i)
		info.Params = append(info.Params, ParamInfo{
			Name: p.Name(),
			Type: p.Type().String(),
		})
	}

	results := sig.Results()
	for i := 0; i < results.Len(); i++ {
		r := results.At(i)
		info.Results = append(info.Results, ParamInfo{
			Name: r.Name(),
			Type: r.Type().String(),
		})
	}

	return info
}
