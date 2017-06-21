package core

const (
	// NoError implies that we exited without any lint or runtime errors.
	NoError = iota

	// LintError implies that we exited with at least one lint error, but no
	// runtime errors.
	LintError

	// DependencyError implies that we were unable to locate a dependency and
	// had to fall back to plain text.
	DependencyError

	// GlobError implies that we were unable to compile a user-supplied glob
	// pattern.
	GlobError

	// RegexError implies that we were unable to compile a user-supplied regex
	// pattern.
	RegexError

	// RuleError implies that we were unable to load an external rule.
	RuleError

	// BinError implies that we were unable to load our go-bindata.
	BinError

	// StyleError implies that we were unable to load an external style.
	StyleError

	// CmdError implies that we were unable to run an external command.
	CmdError

	// IOError implies that we were unable to open or close a file.
	IOError
)

// Verbose indicates if we should print error messages or not.
var Verbose bool

// ValeError is our application-wide return code.
var ValeError = NoError

// SetError sets the application-wide return code to `code` if it's greater
// than the current value.
func SetError(code int) {
	if code > ValeError {
		ValeError = code
	}
}
