package readiness

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// docs/acceptance.md names, for each end-to-end scenario, the tests that exercise
// it as `package.TestName`. A name that no longer exists would let the document
// claim a result nothing produces, so every name must be a real test function in
// that package, and every scenario of the plan must be present.
func TestAcceptanceDocumentNamesRealTests(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "acceptance.md"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile("`([a-z][a-z0-9/]*)\\.(Test[A-Za-z0-9_]+)`")
	refs := re.FindAllStringSubmatch(string(doc), -1)
	if len(refs) < 40 {
		t.Fatalf("only %d test references: the scenario table is missing", len(refs))
	}
	dirs := map[string]string{"cmd": "cmd/stoxicity", "recon": "broker/recon"}
	cache := map[string]string{}
	source := func(pkg string) string {
		if s, ok := cache[pkg]; ok {
			return s
		}
		dir := pkg
		if d, ok := dirs[pkg]; ok {
			dir = d
		}
		files, _ := filepath.Glob(filepath.Join("..", dir, "*_test.go"))
		var b strings.Builder
		for _, f := range files {
			if data, err := os.ReadFile(f); err == nil {
				b.Write(data)
			}
		}
		cache[pkg] = b.String()
		return cache[pkg]
	}
	for _, r := range refs {
		pkg, name := r[1], r[2]
		src := source(pkg)
		if src == "" {
			t.Errorf("%s.%s: no test files in package %s", pkg, name, pkg)
			continue
		}
		if !strings.Contains(src, "func "+name+"(t *testing.T)") {
			t.Errorf("%s.%s is not a test function", pkg, name)
		}
	}
	for n := 1; n <= 16; n++ {
		if !regexp.MustCompile(`(?m)^\| ` + strconv.Itoa(n) + ` \|`).Match(doc) {
			t.Errorf("scenario %d is not in the table", n)
		}
	}
}
