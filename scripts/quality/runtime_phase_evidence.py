"""Best-effort CI phase observations, never test/reuse or compilation authority.

Function phases report completion/error, not an inferred OS exit status. Only
the existing executor supplies actual process wait codes. No command, payload,
environment or package/test name is copied into this separate JSONL stream.
"""
from contextlib import contextmanager
from dataclasses import dataclass
from datetime import datetime, timezone
import json
from pathlib import Path
import sys
import time

PHASES = {"change_scope", "package_scope", "planning_inputs", "discovery", "plan", "batch"}
COUNTS = {"package_count", "batch_index", "batch_count"}


@dataclass
class PhaseOutcome:
    exit_code: int | None = None


class RuntimePhaseEvidence:
    def __init__(self, log_dir: Path | None):
        self.path = log_dir / "runtime-phases.jsonl" if log_dir is not None else None
        self.failed = False
        self.sequence = 0
        if self.path is not None:
            try:
                self.path.write_text("", encoding="utf-8")
            except OSError:
                self._unavailable()

    def _unavailable(self):
        first = not self.failed
        self.failed = True
        if first:
            try:
                print("WARN: runtime phase evidence unavailable; execution results remain authoritative",
                      file=sys.stderr, flush=True)
            except (OSError, ValueError):
                pass  # The existing summary also records timing_complete=false.

    def _record(self, value):
        line = json.dumps({"schema": "x-science.ci-phase-observation.v1", **value})
        if not self.failed:
            try:
                with self.path.open("a", encoding="utf-8") as stream:
                    stream.write(line + "\n")
            except OSError:
                self._unavailable()
        try:
            print("CI phase " + line, file=sys.stderr, flush=True)
        except (OSError, ValueError):
            self._unavailable()

    @contextmanager
    def measure(self, phase: str, **counts):
        if phase not in PHASES or not counts.keys() <= COUNTS or any(
                type(value) is not int or value < 0 for value in counts.values()):
            raise ValueError("invalid internal phase observation")
        outcome = PhaseOutcome()
        if self.path is None:
            yield outcome
            return
        self.sequence += 1
        identity = {"phase_id": self.sequence, "phase": phase, **counts}
        started_at = datetime.now(timezone.utc).isoformat()
        started = time.monotonic()
        self._record({**identity, "event": "start", "started_at": started_at})
        status = "success"
        try:
            yield outcome
        except BaseException:
            status = "error"
            raise
        finally:
            if status == "success" and outcome.exit_code not in (None, 0):
                status = "failed"
            self._record({**identity, "event": "finish", "status": status,
                          "started_at": started_at, "finished_at": datetime.now(timezone.utc).isoformat(),
                          "elapsed_seconds": max(0.0, time.monotonic() - started),
                          "exit_code": outcome.exit_code})
