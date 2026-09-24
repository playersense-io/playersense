package query

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoStringInThisPackageCanJoin is §32.1 for every statement, not only the ones a matrix
// compiles.
//
// The builder has no join to call and its only source is a Fact, which is the structure. What
// structure cannot cover is a hand-written expression, and every expression in this package is a
// string literal in its source. So this reads the source itself: every string literal in every
// file, and none may contain a join in any form ClickHouse accepts. A player-cardinality join is
// what takes a node's memory (§29.1), and it has to be impossible to write here, not merely
// absent from the shapes somebody thought to test.
func TestNoStringInThisPackageCanJoin(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			checked++
			if join.MatchString(value) {
				t.Errorf("%s: a string literal can join: %q", fset.Position(lit.Pos()), value)
			}
			return true
		})
	}
	if checked < 100 {
		t.Fatalf("only %d string literals were checked; the scan is not reading the package", checked)
	}
}
