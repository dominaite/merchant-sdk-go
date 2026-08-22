package dominaite

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
	"testing"
)

// The MAC comparison has to run in constant time. A byte-by-byte compare that
// returns early leaks, through timing, how many leading bytes of a guess were
// right, which turns forging a signature into a few thousand probes instead of
// an impossible search.
//
// Go gives no way to observe this at runtime - hmac.Equal cannot be replaced
// from a test, and timing measurements are far too noisy to assert on - so this
// reads the source instead. It is a real guard: rewriting the compare to == or
// bytes.Compare fails it.
//
// Kept separate from the behavioural webhook tests on purpose. Those pass just
// as happily with a leaky compare, because the answer is the same either way.
func TestWebhookSignatureCompareIsConstantTime(t *testing.T) {
	const (
		file     = "verify.go"
		function = "VerifyWebhook"
	)

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", file, err)
	}

	body := findFunctionBody(parsed, function)
	if body == nil {
		t.Fatalf("%s not found in %s - if it moved, move this test with it", function, file)
	}

	// These are the primitives whose whole purpose is to compare without
	// branching on the first difference.
	constantTimePrimitives := []string{"hmac.Equal", "subtle.ConstantTimeCompare"}

	var found string
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		rendered := renderNode(t, fset, call.Fun)
		for _, primitive := range constantTimePrimitives {
			if rendered == primitive {
				found = primitive
			}
		}
		return true
	})

	if found == "" {
		t.Fatalf("%s must compare the MAC with one of %s; none is called in its body",
			function, strings.Join(constantTimePrimitives, " or "))
	}

	// A constant-time call somewhere in the function is not enough on its own:
	// a short-circuit == on the MAC alongside it would leak just the same.
	// These are the names the MAC and the candidate signature go by.
	macNames := []string{"signature", "expected", "mac", "digest", "computed"}

	ast.Inspect(body, func(node ast.Node) bool {
		binary, ok := node.(*ast.BinaryExpr)
		if !ok || (binary.Op != token.EQL && binary.Op != token.NEQ) {
			return true
		}
		operands := renderNode(t, fset, binary.X) + " " + renderNode(t, fset, binary.Y)
		for _, name := range macNames {
			if strings.Contains(operands, name) {
				t.Errorf("%s compares MAC material with %s: %q. Use %s.",
					function, binary.Op, renderNode(t, fset, binary), found)
				break
			}
		}
		return true
	})
}

func findFunctionBody(file *ast.File, name string) *ast.BlockStmt {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name && fn.Recv == nil {
			return fn.Body
		}
	}
	return nil
}

func renderNode(t *testing.T, fset *token.FileSet, node ast.Node) string {
	t.Helper()
	var out strings.Builder
	if err := printer.Fprint(&out, fset, node); err != nil {
		t.Fatalf("rendering node: %v", err)
	}
	return out.String()
}
