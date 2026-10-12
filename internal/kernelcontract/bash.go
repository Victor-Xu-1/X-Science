package kernelcontract

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"x-science/internal/executionprep"
)

const (
	BashSourcePrefix = "# x-science-bash-command-v1:"
	BashExitPrefix   = "__X_SCIENCE_BASH_EXIT_V1__:"
	MaxBashBytes     = 256 * 1024
)

// BashPythonWrapper is the single canonical execution envelope for a Bash
// command owned by the confined Python kernel. The model-authored command is
// encoded as data, never interpolated into executable Python source.
func BashPythonWrapper(command string) (string, error) {
	return bashPythonWrapper(command, bashStreamingPump)
}

// BashObservationPythonWrapper is the same canonical envelope with an explicit
// native startup contract. Privileged mode disables imported functions,
// SHELLOPTS and BASH_ENV; no profile runs before the user source's bindings.
// It neither changes languages nor replaces the durable executor.
func BashObservationPythonWrapper(command string, plan *executionprep.Observation) (string, error) {
	if !plan.Matches("bash", command) {
		return "", executionprep.ErrObservationUnproved
	}
	result, err := executionprep.Analyze(context.Background(), executionprep.Request{Language: "bash", Source: command}, nil)
	if err != nil || !result.Observation.Matches("bash", command) {
		return "", executionprep.ErrObservationUnproved
	}
	return bashPythonWrapper(command, bashStreamingPump, true)
}

// The legacy pump is accepted only when decoding immutable historical source;
// new executions always use the streaming pump.
const bashLegacyPump = `def _x_science_pump(_x_science_source, _x_science_target):
    while True:
        _x_science_chunk = _x_science_source.read(8192)
        if not _x_science_chunk:
            break
        _x_science_target.write(_x_science_chunk)
        _x_science_target.flush()
`

const bashStreamingPump = `def _x_science_pump(_x_science_source, _x_science_target):
    import codecs
    _x_science_decoder = codecs.getincrementaldecoder("utf-8")(errors="replace")
    try:
        while True:
            _x_science_raw = _x_science_source.buffer.read1(8192)
            if not _x_science_raw:
                break
            _x_science_chunk = _x_science_decoder.decode(_x_science_raw)
            if _x_science_chunk:
                _x_science_target.write(_x_science_chunk)
                _x_science_target.flush()
        _x_science_tail = _x_science_decoder.decode(b"", final=True)
        if _x_science_tail:
            _x_science_target.write(_x_science_tail)
            _x_science_target.flush()
    finally:
        _x_science_source.close()
`

func bashPythonWrapper(command, pump string, observation ...bool) (string, error) {
	if strings.TrimSpace(command) == "" || len(command) > MaxBashBytes || strings.ContainsRune(command, '\x00') {
		return "", errors.New("bash command must be 1-262144 bytes without NUL characters")
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(command))
	arguments := `["/bin/bash", "-lc", _x_science_script]`
	if len(observation) > 0 && observation[0] {
		arguments = `["/bin/bash", "--noprofile", "--norc", "-p", "-c", _x_science_script]`
	}
	return fmt.Sprintf(`%s%s
import base64 as _x_science_b64
import subprocess as _x_science_subprocess
import sys as _x_science_sys
import threading as _x_science_threading

_x_science_command = _x_science_b64.b64decode(%q, validate=True).decode("utf-8")
_x_science_script = "set -e\nset -o pipefail\n" + _x_science_command
_x_science_process = _x_science_subprocess.Popen(
    %s,
    stdout=_x_science_subprocess.PIPE,
    stderr=_x_science_subprocess.PIPE,
    text=True,
    bufsize=1,
)

%s
_x_science_stdout_thread = _x_science_threading.Thread(target=_x_science_pump, args=(_x_science_process.stdout, _x_science_sys.stdout), daemon=True)
_x_science_stderr_thread = _x_science_threading.Thread(target=_x_science_pump, args=(_x_science_process.stderr, _x_science_sys.stderr), daemon=True)
_x_science_stdout_thread.start()
_x_science_stderr_thread.start()
_x_science_exit_code = _x_science_process.wait()
_x_science_stdout_thread.join()
_x_science_stderr_thread.join()
_x_science_sys.stderr.write("\n%s" + str(_x_science_exit_code) + "\n")
_x_science_sys.stderr.flush()
`, BashSourcePrefix, encoded, encoded, arguments, pump, BashExitPrefix), nil
}

// BashCommandFromWrapper verifies the complete canonical envelope before
// returning its immutable model-authored source identity.
func BashCommandFromWrapper(source string) (string, error) {
	firstLine, _, _ := strings.Cut(source, "\n")
	if !strings.HasPrefix(firstLine, BashSourcePrefix) {
		return "", errors.New("bash durable source envelope is invalid")
	}
	encoded := strings.TrimPrefix(firstLine, BashSourcePrefix)
	if encoded == "" || len(encoded) > base64.StdEncoding.EncodedLen(MaxBashBytes) {
		return "", errors.New("bash durable source envelope is invalid")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > MaxBashBytes || strings.ContainsRune(string(raw), '\x00') {
		return "", errors.New("bash durable source envelope is invalid")
	}
	command := string(raw)
	canonical, err := BashPythonWrapper(command)
	if err != nil {
		return "", err
	}
	if canonical != source {
		observational, observationErr := bashPythonWrapper(command, bashStreamingPump, true)
		if observationErr == nil && observational == source {
			return command, nil
		}
		legacy, legacyErr := bashPythonWrapper(command, bashLegacyPump)
		if legacyErr == nil && legacy == source {
			return command, nil
		}
		return "", errors.New("bash durable source envelope is not canonical")
	}
	return command, nil
}
