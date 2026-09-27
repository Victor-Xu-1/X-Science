package server

import (
	"strings"

	transcriptstore "synon-go/internal/persistence/transcript"
)

// sessionRunnerREPLRecoveryContext separates durable task evidence from
// process-local REPL memory whenever a turn resumes. A healthy same-process
// kernel retains its namespace. A resumed model turn is not evidence of a
// worker restart; actual execution outcomes determine which state is missing.
func sessionRunnerREPLRecoveryContext(source transcriptstore.ResumeSource, attempt int) string {
	if (source == transcriptstore.ResumeSourceFresh || strings.TrimSpace(string(source)) == "") && attempt <= 1 {
		return ""
	}
	return "REPL recovery contract: this turn resumed from a durable checkpoint, which does not by itself mean the Python worker restarted. A healthy persistent worker retains its variables and imports. Process-local variables are not durable across an actual worker restart; transcript events, immutable tool results, saved files, and artifacts remain authoritative. If the worker reports lost state or a NameError, reconstruct only the missing state from an exact durable file/result, or reissue a necessary read-only source call. Inspect any completed side effects before recovery; do not replay a side-effecting call or repeat completed computation merely because this turn resumed."
}
