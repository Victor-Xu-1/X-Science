"""X-Science scientific worker entry point."""
import sys
from pathlib import Path

# Provider entrypoints execute this file with their own original sys.path.
sys.path.insert(0, str(Path(__file__).resolve().parent))
from x_science_runtime.worker_execution import run
from x_science_runtime.worker_startup import startup_policy

if __name__ == "__main__":
    run(startup_policy(sys.argv[1:]))
