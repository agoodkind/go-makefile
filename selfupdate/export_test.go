package selfupdate

// ValidateReleaseTagBinding exposes validateReleaseTagBinding to the external
// test package. A real bundle for a mismatched tag requires a real signed
// archive, and that archive is too large and too platform-specific to commit.
var ValidateReleaseTagBinding = validateReleaseTagBinding
