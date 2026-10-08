"""Recognize only the exact build-default correction, never recipe changes."""
from pathlib import Path
import subprocess

if __package__:
    from .pr_metadata_scope import snapshot
else:
    from pr_metadata_scope import snapshot

DEFAULT = b".DEFAULT_GOAL := build\n\n"


def default_goal_only(repo: Path, base: str, head: str) -> bool:
    try:
        before, after = snapshot(repo, base, "Makefile"), snapshot(repo, head, "Makefile")
        return b".DEFAULT_GOAL" not in before and after == DEFAULT + before
    except (OSError, ValueError, KeyError, subprocess.SubprocessError):
        return False
