package main

import (
	"embed"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ArcaPackage describes a built-in Arca package.
// Arca packages are conceptually Arca-native, but their implementation may be
// in Go (like stdlib). The arca compiler handles resolution and build, hiding
// the Go module mechanics from the user.
type ArcaPackage struct {
	// Name is the Arca-side name used in import (e.g. "stdlib")
	Name string
	// GoModPath is the underlying Go module path (e.g. "github.com/tmiyamon/arca/stdlib")
	GoModPath string
	// SourceDir is the directory in the embed FS containing the Go source
	SourceDir string
}

//go:embed stdlib/*.go
var arcaPackageFS embed.FS

// arcaPackages is the registry of built-in Arca packages.
var arcaPackages = map[string]ArcaPackage{
	"stdlib": {
		Name:      "stdlib",
		GoModPath: "github.com/tmiyamon/arca/stdlib",
		SourceDir: "stdlib",
	},
}

// lookupArcaPackage returns the package for a given import name, or nil.
func lookupArcaPackage(name string) *ArcaPackage {
	if pkg, ok := arcaPackages[name]; ok {
		return &pkg
	}
	return nil
}

// loadArcaPackageTypes parses the package's embedded Go source files and
// type-checks them, returning a *types.Package and its *token.FileSet usable
// by GoTypeResolver. No filesystem extraction needed.
func loadArcaPackageTypes(pkg *ArcaPackage) (*types.Package, *token.FileSet) {
	fset := token.NewFileSet()
	var files []*ast.File

	err := fs.WalkDir(arcaPackageFS, pkg.SourceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, err := arcaPackageFS.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, data, parser.ParseComments)
		if err != nil {
			return err
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		return nil, nil
	}

	conf := types.Config{Importer: importer.Default()}
	tp, err := conf.Check(pkg.GoModPath, fset, files, nil)
	if err != nil {
		return nil, nil
	}
	return tp, fset
}

// extractTo extracts the package's source files to dir/<package>/ along with
// a generated go.mod making it a self-contained Go module.
func (p *ArcaPackage) extractTo(dir string) error {
	pkgDir := filepath.Join(dir, p.Name)
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		return err
	}
	err := fs.WalkDir(arcaPackageFS, p.SourceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := arcaPackageFS.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(pkgDir, filepath.Base(path)), data, 0644)
	})
	if err != nil {
		return err
	}
	// Generate go.mod for the package
	modContent := fmt.Sprintf("module %s\n\ngo 1.21\n", p.GoModPath)
	return os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte(modContent), 0644)
}

// usedArcaPackages returns the Arca packages referenced by the transpile result.
func usedArcaPackages(result *transpileResult) []*ArcaPackage {
	var pkgs []*ArcaPackage
	for _, imp := range result.goImports {
		for _, pkg := range arcaPackages {
			if imp.path == pkg.GoModPath {
				p := pkg
				pkgs = append(pkgs, &p)
				break
			}
		}
	}
	return pkgs
}

// addArcaPackagesToGoMod adds require + replace directives for arca packages.
func addArcaPackagesToGoMod(modPath string, pkgs []*ArcaPackage) error {
	if len(pkgs) == 0 {
		return nil
	}
	data, err := os.ReadFile(modPath)
	if err != nil {
		return err
	}
	content := string(data)
	for _, pkg := range pkgs {
		if strings.Contains(content, pkg.GoModPath) {
			continue
		}
		content += fmt.Sprintf("\nrequire %s v0.0.0\n\nreplace %s => ./%s\n",
			pkg.GoModPath, pkg.GoModPath, pkg.Name)
	}
	return os.WriteFile(modPath, []byte(content), 0644)
}
