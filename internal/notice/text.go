// Package notice owns the user-facing legal and attribution texts.
package notice

// Prerequisites is printed once by the invoking `vphone-apps setup` process
// before the sudo prompt, only with --verbose; EnrollHint follows a failed
// enrollment.
const (
	EnrollHint    = `sign in to the Mac App Store with the same Apple Account you used for vphone-apps login, then re-run vphone-apps setup`
	Prerequisites = `Before setup: sign in to the Mac App Store with the same Apple Account you used for vphone-apps login.`
)

// Progress lines of auto-setup (stderr), in the order of REQ-SETUP-007.
// SetupDone is the completion text shown in Box on success.
const (
	SetupIntro    = `Setting up Unfairplay: it needs one App Store app from your account; Organic Maps (open source) is used.`
	SetupKept     = `Organic Maps is already installed on this Mac and will be kept.`
	SetupNew      = `Organic Maps is not installed on this Mac; it will be installed, then removed after enrollment.`
	SetupRemoving = `Removing Organic Maps…`
	SetupDone     = `Setup is complete. Please remember to only use vphone-apps for legal and lawful purposes allowed by the laws of your locality. Consult your attorney if you have any questions on what this does or does not include.`
)
