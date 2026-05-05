// Package staticcheck exposes the custom analyzer set that ships alongside
// go-makefile's `staticcheck-extra` system. The analyzers enforce
// boundary logging, structured slog hygiene, and type discipline, all as
// AST passes (no SSA, no whole-program analysis).
//
// Upstream Honnef checks (SA*, S*, ST*, QF*) are intentionally NOT bundled
// here because golangci-lint already runs them via go-makefile's shared
// golangci config. Keeping this set minimal makes the build cheap and the
// findings easy to reason about.
package staticcheck

import "golang.org/x/tools/go/analysis"

// Analyzers returns all custom AST analyzers in this set.
func Analyzers() []*analysis.Analyzer {
	return []*analysis.Analyzer{
		SlogErrorWithoutErrAnalyzer,
		BannedDirectOutputAnalyzer,
		HotLoopInfoLogAnalyzer,
		MissingBoundaryLogAnalyzer,
		NoAnyOrEmptyInterfaceAnalyzer,
		WrappedErrorWithoutSlogAnalyzer,
		OsExitOutsideMainAnalyzer,
		ContextTODOAnalyzer,
		TimeSleepInProductionAnalyzer,
		PanicInProductionAnalyzer,
		TimeNowOutsideClockAnalyzer,
		GoroutineWithoutRecoverAnalyzer,
		SilentDeferCloseAnalyzer,
		SlogMissingTraceIDAnalyzer,
		GrpcHandlerWithoutPeerEnrichmentAnalyzer,
		SensitiveFieldInLogAnalyzer,
		NolintBanAnalyzer,
		StringSwitchShouldBeEnumAnalyzer,
		ThinWrapperAnalyzer,
	}
}
