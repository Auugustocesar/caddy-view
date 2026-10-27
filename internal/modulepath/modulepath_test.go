// Package modulepath guards the Go module path, so `go install` from the public repository keeps working.
package modulepath

import (
	"bufio"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const wantModule = "github.com/Auugustocesar/caddy-view"

const repoRoot = "../.."

func TestModuleDeclaration(t *testing.T) {
	f, err := os.Open(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var got string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "module "); ok {
			got = strings.TrimSpace(rest)
			break
		}
	}
	if got != wantModule {
		t.Errorf("go.mod module = %q, want %q", got, wantModule)
	}
}

func TestInternalImportsUseTheModulePath(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			return nil
		}
		for _, imp := range file.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(p, "caddy-view/") {
				t.Errorf("%s imports %q, want the %s prefix", path, p, wantModule)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
