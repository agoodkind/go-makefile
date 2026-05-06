package staticcheck

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// GrpcHandlerWithoutPeerEnrichmentAnalyzer warns when a gRPC handler
// method does NOT enrich its slog calls with peer information from
// the incoming context (peer.FromContext, metadata.FromIncomingContext).
//
// Heuristic: a method whose receiver is named *Server or *Service
// (canonical gRPC server pattern), takes (context.Context, *Request)
// and returns (*Response, error), should reference at least one of
// peer.FromContext, metadata.FromIncomingContext, or
// grpc.GetPeerCertificates somewhere in its body. If it does any
// slog call without that context enrichment, warn.
//
// This is project-specific to gRPC consumers. Skipped automatically
// for non-gRPC packages (those that do not import google.golang.org/grpc).
var GrpcHandlerWithoutPeerEnrichmentAnalyzer = &analysis.Analyzer{
	Name: "grpc_handler_missing_peer_enrichment",
	Doc:  "warns when a gRPC handler method logs without enriching context with peer info",
	Run:  runGrpcHandlerWithoutPeerEnrichment,
}

func runGrpcHandlerWithoutPeerEnrichment(pass *analysis.Pass) (any, error) {
	if !packageImports(pass, "google.golang.org/grpc") {
		return nil, nil
	}
	for _, file := range pass.Files {
		if !shouldAnalyzeFile(pass, file) {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv == nil {
				continue
			}
			if !isLikelyGrpcServerMethod(fn) {
				continue
			}
			if hasNolintComment(file, pass.Fset, fn.Pos(), "grpc_handler_missing_peer_enrichment") {
				continue
			}
			if funcReferencesPeerHelper(fn.Body) {
				continue
			}
			if !funcContainsAnySlogCall(fn.Body) {
				continue
			}
			reportAtf(pass, file, fn.Pos(), "gRPC handler %s logs without enriching context with peer info; call peer.FromContext or metadata.FromIncomingContext", fn.Name.Name)
		}
	}
	return nil, nil
}

func isLikelyGrpcServerMethod(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return false
	}
	recvName := receiverTypeName(fn.Recv.List[0])
	if !strings.HasSuffix(recvName, "Server") && !strings.HasSuffix(recvName, "Service") {
		return false
	}
	if fn.Type.Params == nil || len(fn.Type.Params.List) < 2 {
		return false
	}
	first := fn.Type.Params.List[0]
	sel, ok := first.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if x.Name != "context" || sel.Sel.Name != "Context" {
		return false
	}
	if fn.Type.Results == nil || len(fn.Type.Results.List) < 2 {
		return false
	}
	return true
}

func receiverTypeName(field *ast.Field) string {
	t := field.Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	id, ok := t.(*ast.Ident)
	if !ok {
		return ""
	}
	return id.Name
}

func funcReferencesPeerHelper(body *ast.BlockStmt) bool {
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
		recv, name, ok := selectorName(call.Fun)
		if !ok {
			return true
		}
		switch {
		case recv == "peer" && name == "FromContext":
			found = true
		case recv == "metadata" && (name == "FromIncomingContext" || name == "FromOutgoingContext"):
			found = true
		case recv == "grpc" && name == "GetPeerCertificates":
			found = true
		}
		return !found
	})
	return found
}

func funcContainsAnySlogCall(body *ast.BlockStmt) bool {
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
		if isAnyLevelSlogCall(call) {
			found = true
			return false
		}
		return true
	})
	return found
}

// SensitiveFieldInLogAnalyzer warns when slog keyvals include keys
// that look like they carry sensitive material (passwords, tokens,
// keys, secrets). Project teams can extend the deny-list via
// //nolint:sensitive_field_in_log on the call line for known-safe
// uses (e.g. logging the LENGTH of a token, not the token).
//
// Heuristic only. False positives expected. The intent is to make
// secret-leakage paths visible in code review.
var SensitiveFieldInLogAnalyzer = &analysis.Analyzer{
	Name: "sensitive_field_in_log",
	Doc:  "warns when slog calls reference keys that look like sensitive material",
	Run:  runSensitiveFieldInLog,
}

// sensitiveKeyPrefixes is intentionally broad. The heuristic looks at
// keyval names in slog calls and warns on anything that looks like
// secret-bearing material. The list is biased toward false-positive
// over false-negative because the cost of a fake match is a
// //nolint annotation, while the cost of a missed match is leaking
// secrets in production logs.
//
// The tokens are matched as substrings AND as prefixes against the
// lowercased keyval name. An LLM trying to launder by renaming
// `password` to `pwd`, `passwd`, `pass`, `secret_value`, etc. lands
// on a different entry in this list. To genuinely launder you would
// need to invent a name that does not contain any sensitive token
// substring, which is itself suspicious in code review.
var sensitiveKeyPrefixes = []string{
	"password", "passwd", "pwd", "pass",
	"secret", "secrets",
	"token", "tokens", "access_token", "refresh_token", "id_token",
	"apikey", "api_key", "api-key",
	"private_key", "privatekey", "private-key",
	"key_pem", "keypem", "pem",
	"bearer", "auth", "authorization", "authn", "authentication",
	"credential", "credentials", "cred", "creds",
	"jwt", "session", "session_id", "cookie", "csrf",
	"oauth", "client_secret", "clientsecret",
	"signature", "signing_key", "hmac",
	"ssh_key", "sshkey", "private", "privkey",
	"otp", "mfa", "totp", "passcode", "pin",
}

func runSensitiveFieldInLog(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if !shouldAnalyzeFile(pass, file) {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || !isAnyLevelSlogCall(call) {
				return true
			}
			if hasNolintComment(file, pass.Fset, call.Pos(), "sensitive_field_in_log") {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := stringLiteral(arg)
				if !ok {
					continue
				}
				lower := strings.ToLower(lit)
				for _, prefix := range sensitiveKeyPrefixes {
					if strings.Contains(lower, prefix) {
						reportAtf(pass, file, arg.Pos(), "slog key %q looks sensitive; redact, log a summary, or //nolint:sensitive_field_in_log", lit)
						break
					}
				}
			}
			return true
		})
	}
	return nil, nil
}

func packageImports(pass *analysis.Pass, importPath string) bool {
	if pass.Pkg == nil {
		return false
	}
	for _, imp := range pass.Pkg.Imports() {
		if imp.Path() == importPath || strings.HasPrefix(imp.Path(), importPath+"/") {
			return true
		}
	}
	return false
}
