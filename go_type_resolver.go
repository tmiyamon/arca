package main

import (
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"
)

// GoTypeResolver implements TypeResolver using go/types.
// It loads Go package type information and caches it.
type GoTypeResolver struct {
	mu    sync.Mutex
	cache map[string]*types.Package // import path → loaded package
	dir   string                    // project directory (for go.mod resolution)
}

func NewGoTypeResolver(dir string) *GoTypeResolver {
	return &GoTypeResolver{
		cache: make(map[string]*types.Package),
		dir:   dir,
	}
}

func (r *GoTypeResolver) loadPackage(pkg string) *types.Package {
	r.mu.Lock()
	defer r.mu.Unlock()

	if cached, ok := r.cache[pkg]; ok {
		return cached
	}

	cfg := &packages.Config{
		Mode: packages.NeedTypes | packages.NeedName,
		Dir:  r.dir,
	}
	pkgs, err := packages.Load(cfg, pkg)
	if err != nil || len(pkgs) == 0 || pkgs[0].Types == nil || len(pkgs[0].Errors) > 0 {
		r.cache[pkg] = nil
		return nil
	}

	r.cache[pkg] = pkgs[0].Types
	return pkgs[0].Types
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

func (r *GoTypeResolver) CanLoadPackage(pkg string) bool {
	if isStdLib(pkg) {
		return r.loadPackage(pkg) != nil
	}
	// Non-stdlib: check go.mod require entries, not just module cache
	return r.isInGoMod(pkg)
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
