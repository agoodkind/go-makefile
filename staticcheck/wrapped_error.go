package staticcheck

import (
	"go/ast"
	"go/token"
	"go/types"
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
// Fix paths:
//   - Add a slog.Error or slog.Warn call in the same function body
//     before returning the wrapped error.
//   - If the function is a pure encoder/decoder, rename it to
//     match a stdlib codec or io interface (Marshal*/Unmarshal* prefix,
//     or exact Read/Write/ReadAt/WriteAt/WriteTo/ReadFrom/ReadByte/
//     WriteByte/ReadRune/WriteRune). Such functions are silently
//     exempted because their callers are responsible for logging.
//
// Nolint comments are NOT a fix path. The nolint_ban analyzer
// rejects them in production code; baseline the finding instead if
// the call is intentionally silent and a rename does not apply.
var WrappedErrorWithoutSlogAnalyzer = &analysis.Analyzer{
	Name: "wrapped_error_without_slog",
	Doc:  "rejects functions that return a wrapped error without an accompanying slog call",
	Run:  runWrappedErrorWithoutSlog,
}

func runWrappedErrorWithoutSlog(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if !shouldAnalyzeFile(pass, file) {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if isPureCodecOrIOFunc(pass, fn) {
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
		reportAtf(pass, file, ret.Pos(), "function %s returns a wrapped error without an accompanying slog.Error/Warn; log before returning, or rename to a stdlib codec/io shape (Marshal*/Unmarshal*/Read/Write/etc.) if this is a pure encoder", fn.Name.Name)
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

// isPureCodecOrIOFunc carves out functions whose name AND signature
// both conform to a stdlib codec or standard I/O method shape. Both checks are
// required so an LLM cannot rename a high-level orchestrator to
// MarshalSomething or Read and slip past the rule. The call site of
// a real codec/io method is the layer responsible for logging the
// error, so the method itself returning a wrapped error without slog
// is correct.
//
// Recognised shapes (name match AND signature match required):
//
//	MarshalJSON, MarshalBinary, MarshalText, etc. with signature
//	() ([]byte, error). Marshal* prefix.
//
//	UnmarshalJSON, UnmarshalBinary, UnmarshalText, etc. with
//	signature ([]byte) error. Unmarshal* prefix.
//
//	[io.Reader].Read and [io.Writer].Write: ([]byte) (int, error).
//	[io.ReaderAt].ReadAt and [io.WriterAt].WriteAt: ([]byte, int64) (int, error).
//	io.WriterTo.WriteTo: (io.Writer) (int64, error).
//	[io.ReaderFrom].ReadFrom: ([io.Reader]) (int64, error).
//	io.ByteReader.ReadByte: () (byte, error).
//	io.ByteWriter.WriteByte: (byte) error.
//	io.RuneReader.ReadRune: () (rune, int, error).
//	WriteRune: (rune) (int, error).
//
// A method named Read whose signature is `Read() error` is NOT
// exempted because it does not satisfy [io.Reader]. A function named
// MarshalAndSendOrder is NOT exempted because its signature does not
// match the stdlib codec convention.
func isPureCodecOrIOFunc(pass *analysis.Pass, fn *ast.FuncDecl) bool {
	if fn == nil || fn.Name == nil || pass.TypesInfo == nil {
		return false
	}
	obj := pass.TypesInfo.Defs[fn.Name]
	if obj == nil {
		return false
	}
	sig, ok := obj.Type().(*types.Signature)
	if !ok {
		return false
	}
	name := fn.Name.Name
	if strings.HasPrefix(name, "Marshal") {
		return matchesMarshalShape(sig)
	}
	if strings.HasPrefix(name, "Unmarshal") {
		return matchesUnmarshalShape(sig)
	}
	switch name {
	case "Read", "Write":
		return matchesReadWriteShape(sig)
	case "ReadAt", "WriteAt":
		return matchesReadWriteAtShape(sig)
	case "WriteTo":
		return matchesWriteToShape(sig)
	case "ReadFrom":
		return matchesReadFromShape(sig)
	case "ReadByte":
		return matchesReadByteShape(sig)
	case "WriteByte":
		return matchesWriteByteShape(sig)
	case "ReadRune":
		return matchesReadRuneShape(sig)
	case "WriteRune":
		return matchesWriteRuneShape(sig)
	}
	return false
}

// matchesMarshalShape verifies signature: () ([]byte, error).
func matchesMarshalShape(sig *types.Signature) bool {
	return sig.Params().Len() == 0 &&
		sig.Results().Len() == 2 &&
		isByteSlice(sig.Results().At(0).Type()) &&
		isErrorType(sig.Results().At(1).Type())
}

// matchesUnmarshalShape verifies signature: ([]byte) error.
func matchesUnmarshalShape(sig *types.Signature) bool {
	return sig.Params().Len() == 1 &&
		isByteSlice(sig.Params().At(0).Type()) &&
		sig.Results().Len() == 1 &&
		isErrorType(sig.Results().At(0).Type())
}

// matchesReadWriteShape verifies [io.Reader].Read or [io.Writer].Write
// signature: ([]byte) (int, error).
func matchesReadWriteShape(sig *types.Signature) bool {
	return sig.Params().Len() == 1 &&
		isByteSlice(sig.Params().At(0).Type()) &&
		sig.Results().Len() == 2 &&
		isBasicKind(sig.Results().At(0).Type(), types.Int) &&
		isErrorType(sig.Results().At(1).Type())
}

// matchesReadWriteAtShape verifies [io.ReaderAt] or [io.WriterAt]
// signature: ([]byte, int64) (int, error).
func matchesReadWriteAtShape(sig *types.Signature) bool {
	return sig.Params().Len() == 2 &&
		isByteSlice(sig.Params().At(0).Type()) &&
		isBasicKind(sig.Params().At(1).Type(), types.Int64) &&
		sig.Results().Len() == 2 &&
		isBasicKind(sig.Results().At(0).Type(), types.Int) &&
		isErrorType(sig.Results().At(1).Type())
}

// matchesWriteToShape verifies io.WriterTo.WriteTo
// signature: (io.Writer) (int64, error).
func matchesWriteToShape(sig *types.Signature) bool {
	return sig.Params().Len() == 1 &&
		isNamedType(sig.Params().At(0).Type(), "io", "Writer") &&
		sig.Results().Len() == 2 &&
		isBasicKind(sig.Results().At(0).Type(), types.Int64) &&
		isErrorType(sig.Results().At(1).Type())
}

// matchesReadFromShape verifies [io.ReaderFrom].ReadFrom
// signature: ([io.Reader]) (int64, error).
func matchesReadFromShape(sig *types.Signature) bool {
	return sig.Params().Len() == 1 &&
		isNamedType(sig.Params().At(0).Type(), "io", "Reader") &&
		sig.Results().Len() == 2 &&
		isBasicKind(sig.Results().At(0).Type(), types.Int64) &&
		isErrorType(sig.Results().At(1).Type())
}

// matchesReadByteShape verifies io.ByteReader.ReadByte
// signature: () (byte, error).
func matchesReadByteShape(sig *types.Signature) bool {
	return sig.Params().Len() == 0 &&
		sig.Results().Len() == 2 &&
		isBasicKind(sig.Results().At(0).Type(), types.Byte) &&
		isErrorType(sig.Results().At(1).Type())
}

// matchesWriteByteShape verifies io.ByteWriter.WriteByte
// signature: (byte) error.
func matchesWriteByteShape(sig *types.Signature) bool {
	return sig.Params().Len() == 1 &&
		isBasicKind(sig.Params().At(0).Type(), types.Byte) &&
		sig.Results().Len() == 1 &&
		isErrorType(sig.Results().At(0).Type())
}

// matchesReadRuneShape verifies io.RuneReader.ReadRune
// signature: () (rune, int, error).
func matchesReadRuneShape(sig *types.Signature) bool {
	return sig.Params().Len() == 0 &&
		sig.Results().Len() == 3 &&
		isBasicKind(sig.Results().At(0).Type(), types.Rune) &&
		isBasicKind(sig.Results().At(1).Type(), types.Int) &&
		isErrorType(sig.Results().At(2).Type())
}

// matchesWriteRuneShape verifies bytes/strings.Builder.WriteRune
// signature: (rune) (int, error).
func matchesWriteRuneShape(sig *types.Signature) bool {
	return sig.Params().Len() == 1 &&
		isBasicKind(sig.Params().At(0).Type(), types.Rune) &&
		sig.Results().Len() == 2 &&
		isBasicKind(sig.Results().At(0).Type(), types.Int) &&
		isErrorType(sig.Results().At(1).Type())
}

func isByteSlice(t types.Type) bool {
	s, ok := t.(*types.Slice)
	if !ok {
		return false
	}
	return isBasicKind(s.Elem(), types.Byte) || isBasicKind(s.Elem(), types.Uint8)
}

func isBasicKind(t types.Type, kind types.BasicKind) bool {
	b, ok := t.(*types.Basic)
	if !ok {
		return false
	}
	return b.Kind() == kind
}

func isErrorType(t types.Type) bool {
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj != nil && obj.Pkg() == nil && obj.Name() == "error"
}

func isNamedType(t types.Type, pkgName, typeName string) bool {
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Name() != typeName {
		return false
	}
	return obj.Pkg() != nil && obj.Pkg().Name() == pkgName
}
