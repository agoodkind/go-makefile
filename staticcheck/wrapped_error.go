package staticcheck

import (
	"go/ast"
	"go/token"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// WrappedErrorWithoutSlogAnalyzer flags any function that returns a
// wrapped error (typically `fmt.Errorf("...: %w", err)`) without
// also emitting a structured slog event for the same error in the
// same function body.
//
// The rule encodes the project's "log at every boundary" discipline.
// A function that wraps an external-resource error and returns it
// silently is invisible in production logs unless every caller
// logs the wrapped error themselves, which they routinely don't.
//
// Heuristics:
//   - Find every return statement whose error value is a wrap
//     expression (`fmt.Errorf` with %w, `errors.Join` with multiple
//     args, etc.).
//   - Walk back up to the enclosing function body and look for any
//     slog.Error / slog.Warn / log.Error / log.Warn call within it.
//   - If none, report the return statement.
//
// Allowed escape hatches:
//   - `_test.go` files (test helpers do not need to log)
//   - `//nolint:wrapped_error_without_slog` line comment on or before
//     the offending return statement
//   - Functions whose only behaviour is parsing or validation:
//     names starting with `parse`, `validate`, `decode`, `marshal`,
//     `unmarshal`. Callers of these are expected to log.
var WrappedErrorWithoutSlogAnalyzer = &analysis.Analyzer{
	Name: "wrapped_error_without_slog",
	Doc:  "rejects functions that return a wrapped error without an accompanying slog call",
	Run:  runWrappedErrorWithoutSlog,
}

func runWrappedErrorWithoutSlog(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		path := fileName(pass, file.Pos())
		if isTestFile(path) || isGeneratedFile(file) || isProtobufGeneratedPath(path) || isStaticcheckPath(path) {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if isPureValidatorByName(fn.Name.Name) {
				continue
			}
			analyzeFuncForWrappedReturns(pass, file, fn)
		}
	}
	return nil, nil
}

func analyzeFuncForWrappedReturns(pass *analysis.Pass, file *ast.File, fn *ast.FuncDecl) {
	hasSlog := funcContainsSlogErrorOrWarn(fn.Body)
	if hasSlog {
		return
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		ret, ok := node.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		if !returnWrapsError(ret) {
			return true
		}
		if hasNolintComment(file, pass.Fset, ret.Pos(), "wrapped_error_without_slog") {
			return true
		}
		pass.Reportf(ret.Pos(), "function %s returns a wrapped error without an accompanying slog.Error/Warn; either log before returning or add //nolint:wrapped_error_without_slog", fn.Name.Name)
		return true
	})
}

// returnWrapsError returns true if any return value in `ret` is a
// wrap expression. Patterns:
//
//	return ..., fmt.Errorf("...: %w", err)
//	return ..., errors.Join(a, b)
//	return ..., fmt.Errorf("...", err)   // also flagged: still wrapping
func returnWrapsError(ret *ast.ReturnStmt) bool {
	for _, expr := range ret.Results {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			continue
		}
		recv, name, ok := selectorName(call.Fun)
		if !ok {
			continue
		}
		switch {
		case recv == "fmt" && name == "Errorf":
			if errorfWrapsError(call) {
				return true
			}
		case recv == "errors" && (name == "Join" || name == "Wrap"):
			return true
		}
	}
	return false
}

func errorfWrapsError(call *ast.CallExpr) bool {
	if len(call.Args) < 2 {
		return false
	}
	format, ok := stringLiteral(call.Args[0])
	if !ok {
		return false
	}
	return strings.Contains(format, "%w") || strings.Contains(format, "%v")
}

func funcContainsSlogErrorOrWarn(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isErrorOrWarnSlogCall(call) {
			found = true
			return false
		}
		return true
	})
	return found
}

func isErrorOrWarnSlogCall(call *ast.CallExpr) bool {
	receiver, name, ok := selectorName(call.Fun)
	if !ok {
		return false
	}
	switch name {
	case "Error", "ErrorContext", "Warn", "WarnContext":
		return isLikelyLoggerReceiver(receiver)
	}
	if name == "LogAttrs" && isLikelyLoggerReceiver(receiver) && len(call.Args) >= 2 {
		return exprContains(call.Args[1], "LevelError") || exprContains(call.Args[1], "LevelWarn")
	}
	if receiver == "slog" && name == "Log" && len(call.Args) >= 2 {
		return exprContains(call.Args[1], "LevelError") || exprContains(call.Args[1], "LevelWarn")
	}
	return false
}

func hasNolintComment(file *ast.File, fset *token.FileSet, pos token.Pos, name string) bool {
	if file == nil {
		return false
	}
	target := fset.Position(pos).Line
	needle := "nolint:" + name
	for _, group := range file.Comments {
		for _, c := range group.List {
			if !strings.Contains(c.Text, needle) {
				continue
			}
			line := fset.Position(c.Pos()).Line
			if line == target || line == target-1 {
				return true
			}
		}
	}
	return false
}

func isPureValidatorByName(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range []string{"parse", "validate", "decode", "marshal", "unmarshal"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}
