package staticcheck

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// OsExitOutsideMainAnalyzer flags [os.Exit] calls outside main() and
// init(). Production code should return an error up to a single
// process-level boundary that decides whether to exit.
//
// Allowed escapes:
//   - inside func main()
//   - inside func init()
//   - inside _test.go files (TestMain, etc.)
//   - //nolint:os_exit_outside_main on the call line
var OsExitOutsideMainAnalyzer = &analysis.Analyzer{
	Name: "os_exit_outside_main",
	Doc:  "rejects os.Exit calls outside main()/init() so failure modes return up the stack",
	Run:  runOsExitOutsideMain,
}

func runOsExitOutsideMain(pass *analysis.Pass) (any, error) {
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
			if fn.Name.Name == "main" || fn.Name.Name == "init" {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				recv, name, ok := selectorName(call.Fun)
				if !ok {
					return true
				}
				if recv == "os" && name == "Exit" && !hasNolintComment(file, pass.Fset, call.Pos(), "os_exit_outside_main") {
					pass.Reportf(call.Pos(), "os.Exit called outside main()/init(); return error to caller instead")
				}
				return true
			})
		}
	}
	return nil, nil
}

// ContextTODOAnalyzer flags [context.TODO] in production code.
// Use [context.Background] at top-level entry points, otherwise thread
// [context.Context] through function signatures.
var ContextTODOAnalyzer = &analysis.Analyzer{
	Name: "context_todo_in_production",
	Doc:  "rejects context.TODO() in production code; use context.Background() or thread context",
	Run:  runContextTODO,
}

func runContextTODO(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		path := fileName(pass, file.Pos())
		if isTestFile(path) || isGeneratedFile(file) || isProtobufGeneratedPath(path) || isStaticcheckPath(path) {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			recv, name, ok := selectorName(call.Fun)
			if !ok {
				return true
			}
			if recv == "context" && name == "TODO" && !hasNolintComment(file, pass.Fset, call.Pos(), "context_todo_in_production") {
				pass.Reportf(call.Pos(), "context.TODO() in production code; use context.Background() or thread context from caller")
			}
			return true
		})
	}
	return nil, nil
}

// TimeSleepInProductionAnalyzer flags [time.Sleep] in production code.
// Use [time.NewTimer] with select{} for cancellation, or
// [time.AfterFunc].
//
// Allowed in main packages (CLI ergonomics), in _test.go files, and
// behind //nolint:time_sleep_in_production.
var TimeSleepInProductionAnalyzer = &analysis.Analyzer{
	Name: "time_sleep_in_production",
	Doc:  "rejects time.Sleep in production library code; use time.NewTimer + select for cancellation",
	Run:  runTimeSleepInProduction,
}

func runTimeSleepInProduction(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		path := fileName(pass, file.Pos())
		if isTestFile(path) || isGeneratedFile(file) || isProtobufGeneratedPath(path) || isStaticcheckPath(path) {
			continue
		}
		if pass.Pkg != nil && pass.Pkg.Name() == "main" {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			recv, name, ok := selectorName(call.Fun)
			if !ok {
				return true
			}
			if recv == "time" && name == "Sleep" && !hasNolintComment(file, pass.Fset, call.Pos(), "time_sleep_in_production") {
				pass.Reportf(call.Pos(), "time.Sleep in production library code; use time.NewTimer + select for cancellation")
			}
			return true
		})
	}
	return nil, nil
}

// PanicInProductionAnalyzer flags panic() outside init(), test files,
// and recovery handlers.
//
// Allowed escapes:
//   - inside func init()
//   - inside _test.go files
//   - inside functions whose name starts with `Must`. This mirrors the
//     stdlib convention (regexp.MustCompile, template.Must). Such
//     functions document via their name that they panic on error.
//     They are typically called from startup contexts where panic is
//     the right behaviour because there is no recovery path.
//   - //nolint:panic_in_production on the call line
var PanicInProductionAnalyzer = &analysis.Analyzer{
	Name: "panic_in_production",
	Doc:  "rejects panic() in production code outside init(); return error up the stack",
	Run:  runPanicInProduction,
}

func runPanicInProduction(pass *analysis.Pass) (any, error) {
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
			if fn.Name.Name == "init" {
				continue
			}
			if strings.HasPrefix(fn.Name.Name, "Must") {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				ident, ok := call.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				if ident.Name == "panic" && !hasNolintComment(file, pass.Fset, call.Pos(), "panic_in_production") {
					pass.Reportf(call.Pos(), "panic() called in production code; return error up the stack")
				}
				return true
			})
		}
	}
	return nil, nil
}

// TimeNowOutsideClockAnalyzer flags [time.Now] calls outside an
// allowed clock-injection point. Real-time wall clock makes code
// untestable for time-sensitive logic. Acceptable patterns:
//   - inside files matching `clock.go` (the project's own clock helpers)
//   - inside _test.go
//   - inside main packages (CLI startup logging)
//   - //nolint:time_now_outside_clock on the call line
var TimeNowOutsideClockAnalyzer = &analysis.Analyzer{
	Name: "time_now_outside_clock",
	Doc:  "rejects time.Now() outside designated clock-injection points; pass clock.Clock for testability",
	Run:  runTimeNowOutsideClock,
}

func runTimeNowOutsideClock(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		path := fileName(pass, file.Pos())
		if isTestFile(path) || isGeneratedFile(file) || isProtobufGeneratedPath(path) || isStaticcheckPath(path) {
			continue
		}
		if strings.HasSuffix(path, "/clock.go") || strings.HasSuffix(path, "/clock/clock.go") {
			continue
		}
		if pass.Pkg != nil && pass.Pkg.Name() == "main" {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			recv, name, ok := selectorName(call.Fun)
			if !ok {
				return true
			}
			if recv == "time" && name == "Now" && !hasNolintComment(file, pass.Fset, call.Pos(), "time_now_outside_clock") {
				pass.Reportf(call.Pos(), "time.Now() outside clock helper; inject a clock.Clock for testability or //nolint:time_now_outside_clock")
			}
			return true
		})
	}
	return nil, nil
}
