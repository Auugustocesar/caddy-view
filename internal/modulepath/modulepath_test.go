// Package modulepath guards repository-level invariants that are easy to break by accident: the module path (so
// `go install` from the public repository keeps working), a patched Go release in go.mod, and the quality gates
// in CI.
package modulepath

import (
	"bufio"
	"encoding/json"
	"go/parser"
	"go/token"
	"go/version"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const wantModule = "github.com/Auugustocesar/caddy-view"

// minGo is the oldest Go release without known standard library vulnerabilities that this code reaches
// (govulncheck, 2026-10-27). Raise it when govulncheck reports newer fixes.
const minGo = "go1.26.10"

const repoRoot = "../.."

func TestGoDirectiveIsPatched(t *testing.T) {
	f, err := os.Open(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var got string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "go "); ok {
			got = "go" + strings.TrimSpace(rest)
			break
		}
	}
	if !version.IsValid(got) {
		t.Fatalf("go.mod has no valid go directive (found %q)", got)
	}
	if version.Compare(got, minGo) < 0 {
		t.Errorf("go.mod requires %s, want at least %s (older releases have known vulnerabilities)", got, minGo)
	}
}

func TestCIRunsTheQualityGates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	for _, gate := range []string{"staticcheck", "govulncheck", "go mod tidy -diff", "go vet", "-race", "-shuffle=on", "schedule:",
		"npm ci", "npm test", "npm run build", "git diff --exit-code"} {
		if !strings.Contains(workflow, gate) {
			t.Errorf("ci.yml does not run or mention %q", gate)
		}
	}
}

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

func TestFrontendDependenciesArePinned(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "web", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	for _, deps := range []map[string]string{pkg.Dependencies, pkg.DevDependencies} {
		for name, version := range deps {
			if strings.ContainsAny(version, "^~*<>|") || version == "latest" {
				t.Errorf("%s is %q, want an exact version so builds are reproducible", name, version)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "web", "package-lock.json")); err != nil {
		t.Errorf("web/package-lock.json is missing: %v", err)
	}
}
