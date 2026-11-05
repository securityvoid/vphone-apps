package notice

import _ "embed"

// Text is the embedded NOTICE (a copy of the repository NOTICE written by
// scripts/gen-notice.sh; go:embed cannot reach parent directories).
//
//go:embed NOTICE
var Text string
