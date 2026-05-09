package staticcheck

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// RTASlogMarkerBoxAnalyzer flags slog calls whose variadic args
// include a struct value of a type that bears a project-declared
// marker. The empirical case looks like:
//
//	srv.log.InfoContext(ctx, "mcp.server.starting", "serve_meta", serveMeta, ...)
//	log.Debug("webapp.starting", ..., "channel_meta", webapp.WebMeta{...})
//
// where serveMeta and WebMeta both implement a marker interface or
// declare a marker method (for example IsLivetrackMeta). The bypass
// works because the deadcode analyzer sees the slog formatter call the
// marker method via reflection-shaped any-passthrough, which counts as
// a use of the type's method set.
//
// This is a soft warning: slog fields on real telemetry types are
// legitimate. The diagnostic carries "[RTA001]" and prompts the
// author to confirm intent.
//
// Configurable behaviour:
//
//	-marker_interfaces  comma-separated qualified interface names.
//	-marker_methods     comma-separated method names whose presence
//	                    on the value's type triggers the warning even
//	                    without an interface declaration.
//	-slog_methods       comma-separated slog method names to inspect.
//	                    Default covers Debug, Info, Warn, Error, Log,
//	                    DebugContext, InfoContext, WarnContext,
//	                    ErrorContext, LogAttrs.
//
// When both marker_interfaces and marker_methods are empty the
// analyzer no-ops with a stderr warning.
//
// Document intentional exceptions in the staticcheck-extra baseline,
// not via inline directives.
var RTASlogMarkerBoxAnalyzer = newRTASlogMarkerBoxAnalyzer()

func newRTASlogMarkerBoxAnalyzer() *analysis.Analyzer {
	a := &analysis.Analyzer{
		Name: "rta_slog_marker_box",
		Doc:  "warns when slog field values pass marker-bearing struct values; load-bearing for telemetry, or for deadcode RTA reachability?",
		Run:  runRTASlogMarkerBox,
	}
	a.Flags.String("marker_interfaces", "", "comma-separated qualified interface names")
	a.Flags.String("marker_methods", "", "comma-separated method names whose presence on a slog field value's type triggers the warning")
	a.Flags.String("slog_methods", "", "comma-separated slog method names to inspect; default covers the standard log/slog level methods")
	return a
}

var defaultSlogMethods = map[string]struct{}{
	"Debug":         {},
	"DebugContext":  {},
	"Info":          {},
	"InfoContext":   {},
	"Warn":          {},
	"WarnContext":   {},
	"Error":         {},
	"ErrorContext":  {},
	"Log":           {},
	"LogAttrs":      {},
	"Print":         {},
	"Printf":        {},
}

func runRTASlogMarkerBox(pass *analysis.Pass) (any, error) {
	cfg := loadRTASlogMarkerBoxConfig(pass.Analyzer)
	for _, file := range pass.Files {
		if !shouldAnalyzeFile(pass, file) {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			methodName, ok := slogCallMethodName(call, cfg)
			if !ok {
				return true
			}
			variadicStart := slogVariadicStart(methodName)
			if variadicStart < 0 || variadicStart >= len(call.Args) {
				return true
			}
			for _, arg := range call.Args[variadicStart:] {
				if !cfg.argHasMarker(pass, arg) {
					continue
				}
				argType := pass.TypesInfo.TypeOf(arg)
				reportAtf(pass, file, arg.Pos(),
					"[RTA001] slog field passes a marker-bearing struct value (%s); is this load-bearing for telemetry, or for deadcode RTA reachability?",
					formatType(argType))
				return true
			}
			return true
		})
	}
	return nil, nil
}

type rtaSlogMarkerBoxConfig struct {
	markerInterfaces map[string]struct{}
	markerMethods    map[string]struct{}
	slogMethods      map[string]struct{}
}

func loadRTASlogMarkerBoxConfig(a *analysis.Analyzer) rtaSlogMarkerBoxConfig {
	cfg := rtaSlogMarkerBoxConfig{
		markerInterfaces: csvFlagSet(&a.Flags, "marker_interfaces"),
		markerMethods:    csvFlagSet(&a.Flags, "marker_methods"),
		slogMethods:      csvFlagSet(&a.Flags, "slog_methods"),
	}
	if len(cfg.slogMethods) == 0 {
		cfg.slogMethods = defaultSlogMethods
	}
	return cfg
}

func slogCallMethodName(call *ast.CallExpr, cfg rtaSlogMarkerBoxConfig) (string, bool) {
	receiver, name, ok := selectorName(call.Fun)
	if !ok {
		return "", false
	}
	if !isLikelyLoggerReceiver(receiver) && receiver != "slog" {
		return "", false
	}
	if _, listed := cfg.slogMethods[name]; !listed {
		return "", false
	}
	return name, true
}

// slogVariadicStart returns the index of the first variadic key/value
// argument for the named slog method, or -1 if the method is not
// recognised.
//
//	Debug, Info, Warn, Error                 -> 1 (msg, kvs...)
//	DebugContext, InfoContext, WarnContext,
//	ErrorContext                             -> 2 (ctx, msg, kvs...)
//	Log                                      -> 3 (ctx, level, msg, kvs...)
//	LogAttrs                                 -> 3 (ctx, level, msg, attrs...)
func slogVariadicStart(methodName string) int {
	switch methodName {
	case "Debug", "Info", "Warn", "Error", "Print", "Printf":
		return 1
	case "DebugContext", "InfoContext", "WarnContext", "ErrorContext":
		return 2
	case "Log", "LogAttrs":
		return 3
	}
	return -1
}

func (c rtaSlogMarkerBoxConfig) argHasMarker(pass *analysis.Pass, arg ast.Expr) bool {
	if pass.TypesInfo == nil {
		return false
	}
	t := pass.TypesInfo.TypeOf(arg)
	if t == nil {
		return false
	}
	if !isStructValueOrComposite(t) {
		return false
	}
	if c.typeHasMarkerMethod(t) {
		return true
	}
	for qualified := range c.markerInterfaces {
		if typeImplementsInterfaceByName(t, qualified) {
			return true
		}
	}
	return false
}

func isStructValueOrComposite(t types.Type) bool {
	switch t.(type) {
	case *types.Pointer:
		return false
	case *types.Basic:
		return false
	}
	if isSlogAttrType(t) {
		return false
	}
	if _, ok := underlyingNamed(t); !ok {
		_, isStruct := t.Underlying().(*types.Struct)
		return isStruct
	}
	_, isStruct := t.Underlying().(*types.Struct)
	return isStruct
}

func isSlogAttrType(t types.Type) bool {
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Pkg().Path() == "log/slog" && (obj.Name() == "Attr" || obj.Name() == "Value")
}

func (c rtaSlogMarkerBoxConfig) typeHasMarkerMethod(t types.Type) bool {
	if len(c.markerMethods) == 0 {
		return false
	}
	mset := types.NewMethodSet(types.NewPointer(t))
	for i := 0; i < mset.Len(); i++ {
		method := mset.At(i).Obj()
		if method == nil {
			continue
		}
		if _, ok := c.markerMethods[method.Name()]; ok {
			return true
		}
	}
	value := types.NewMethodSet(t)
	for i := 0; i < value.Len(); i++ {
		method := value.At(i).Obj()
		if method == nil {
			continue
		}
		if _, ok := c.markerMethods[method.Name()]; ok {
			return true
		}
	}
	return false
}

