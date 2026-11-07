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

// Analyzers returns the checks available to staticcheck-extra.
//
// The staticcheck-extra command passes this list to multichecker, which
// registers a command-line flag for each check.
//
// Register the checks with:
//
//	multichecker.Main(staticcheck.Analyzers()...)
func Analyzers() []*analysis.Analyzer {
	return []*analysis.Analyzer{
		DocStructureAnalyzer,
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
		GrpcHandlerWithoutPeerEnrichmentAnalyzer,
		SensitiveFieldInLogAnalyzer,
		NolintBanAnalyzer,
		StringSwitchShouldBeEnumAnalyzer,
		ThinWrapperAnalyzer,
		RTAThrowawayRegistrationAnalyzer,
		RTASyntheticMarkerCallAnalyzer,
		RTASlogFieldBypassAnalyzer,
		LifecycleNoopCloserAnalyzer,
		LifecycleSilentCloseErrAnalyzer,
		GrpcMethodNameLiteralAnalyzer,
		NoTildePathLiteralAnalyzer,
		TestPackageAnalyzer,
		TestSourceFileAnalyzer,
		TestAssertAnalyzer,
		TestDoubleAnalyzer,
		TestSeamAnalyzer,
	}
}
