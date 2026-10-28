package kernelcontract

import (
	"x-science/internal/executionprep"
	"x-science/internal/kernelcontract/bashsource"
)

const (
	BashSourcePrefix = bashsource.BashSourcePrefix
	BashExitPrefix   = bashsource.BashExitPrefix
	MaxBashBytes     = bashsource.MaxBashBytes
)

// All consumers share the same canonical source envelope. This facade keeps
// tool callers independent of the worker's lower-level startup contract.
func BashPythonWrapper(command string) (string, error) {
	return bashsource.BashPythonWrapper(command)
}

func BashObservationPythonWrapper(command string, plan *executionprep.Observation) (string, error) {
	return bashsource.BashObservationPythonWrapper(command, plan)
}

func BashCommandFromWrapper(source string) (string, error) {
	return bashsource.BashCommandFromWrapper(source)
}
