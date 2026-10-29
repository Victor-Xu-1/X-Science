"""Closed supervisor mode; scientific subprocesses keep their normal startup."""
import os
from pathlib import Path
import sys

CANONICAL_BASH_STDLIB_V1 = "canonical-bash-stdlib-v1"
_POLICY_ARGUMENT = "--x-science-startup-policy="


def startup_policy(arguments):
    candidates = [value for value in arguments if value.startswith("--x-science-startup-policy")]
    if not candidates:
        return ""
    if candidates != [_POLICY_ARGUMENT + CANONICAL_BASH_STDLIB_V1]:
        raise RuntimeError("unknown supervisor startup policy")
    if not sys.flags.isolated or not sys.flags.no_site:
        raise RuntimeError("Bash supervisor requires isolated no-site startup")
    return CANONICAL_BASH_STDLIB_V1


def prepare_scientific_child_imports():
    # Isolated/no-site flags belong to the supervisor argv, not child processes.
    # Keep the existing product hook available to normally initialized children.
    root = str(Path(__file__).resolve().parents[1])
    paths = [root]
    paths.extend(value for value in os.environ.get("PYTHONPATH", "").split(os.pathsep)
                 if value and value != root)
    os.environ["PYTHONPATH"] = os.pathsep.join(paths)
