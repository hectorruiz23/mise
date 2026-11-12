package arch

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestDomainPackagesImportOnlyStandardLibrary(t *testing.T) {
	root := apiRoot(t)

	dirs, err := filepath.Glob(filepath.Join(root, "internal", "*", "domain"))
	if err != nil {
		t.Fatalf("glob domain packages: %v", err)
	}
	if len(dirs) == 0 {
		t.Fatal("no domain packages found under api/internal/*/domain")
	}

	violations, err := domainImportViolations(root)
	if err != nil {
		t.Fatalf("scan domain packages: %v", err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

func TestForbiddenImportDetector(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "fmt", want: false},
		{path: "net/http", want: false},
		{path: "go/ast", want: false},
		{path: "mise/api/internal/kernel", want: false},
		{path: "mise/api/internal/platform/db", want: true},
		{path: "mise/api/internal/platform", want: true},
		{path: "mise/api/internal/tasks/app", want: true},
		{path: "mise/api/internal/tasks/adapters/http", want: true},
		{path: "mise/api/internal/users/domain", want: true},
		{path: "modernc.org/sqlite", want: true},
		{path: "github.com/google/uuid", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			_, got := forbiddenImport(tt.path)
			if got != tt.want {
				t.Errorf("forbiddenImport(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func domainImportViolations(apiRoot string) ([]string, error) {
	dirs, err := filepath.Glob(filepath.Join(apiRoot, "internal", "*", "domain"))
	if err != nil {
		return nil, err
	}

	var violations []string
	for _, dir := range dirs {
		walkErr := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}

			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return fmt.Errorf("unquote import in %s: %w", path, err)
				}
				reason, forbidden := forbiddenImport(importPath)
				if !forbidden {
					continue
				}
				rel, err := filepath.Rel(apiRoot, path)
				if err != nil {
					rel = path
				}
				violations = append(violations,
					fmt.Sprintf("%s imports %q: %s", rel, importPath, reason))
			}
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	return violations, nil
}

const (
	modulePath   = "mise/api"
	kernelImport = modulePath + "/internal/kernel"
)

func forbiddenImport(path string) (reason string, forbidden bool) {
	for _, layer := range []string{"app", "adapters", "platform"} {
		if path == layer ||
			strings.HasPrefix(path, layer+"/") ||
			strings.HasSuffix(path, "/"+layer) ||
			strings.Contains(path, "/"+layer+"/") {
			return "domain may not import the " + layer + " layer", true
		}
	}

	if path == kernelImport || strings.HasPrefix(path, kernelImport+"/") {
		return "", false
	}
	if path == modulePath || strings.HasPrefix(path, modulePath+"/") {
		return "domain may import only the kernel from this module", true
	}

	first := path
	if i := strings.IndexByte(first, '/'); i >= 0 {
		first = first[:i]
	}
	if path == "C" || strings.Contains(first, ".") {
		return "domain must import only the standard library", true
	}

	return "", false
}

func apiRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate arch_test.go")
	}
	file, err := filepath.Abs(file)
	if err != nil {
		t.Fatalf("absolute path of arch_test.go: %v", err)
	}

	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("api root %q does not contain go.mod: %v", root, err)
	}
	return root
}
