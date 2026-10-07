"""Neutral native checkpoint fixture; no scientific calculation or network."""
import hashlib
import json
import os
from pathlib import Path
import signal
import sys
import time

output = Path("out")
output.mkdir(exist_ok=True)
counter = 16


def committed_checkpoint(*_):
    value = str(counter + 1).encode()
    state = output / "state.bin"
    state.write_bytes(value)
    with state.open("rb") as source:
        os.fsync(source.fileno())
    records = [
        {"schema": "synon.compute-checkpoint.v1", "generation": int(os.environ["OPERON_CHECKPOINT_GENERATION"]),
         "source_input_sha256": os.environ["OPERON_INPUT_SHA256"],
         "resume_command_sha256": os.environ["OPERON_RESUME_COMMAND_SHA256"]},
        {"path": "out/state.bin", "sha256": hashlib.sha256(value).hexdigest(), "bytes": len(value)},
        {"commit": "complete", "file_count": 1, "bytes": len(value)},
    ]
    manifest = Path(os.environ["OPERON_CHECKPOINT_MANIFEST"])
    temporary = manifest.with_suffix(".next")
    with temporary.open("w", encoding="utf-8") as target:
        for record in records:
            target.write(json.dumps(record) + "\n")
        target.flush()
        os.fsync(target.fileno())
    temporary.replace(manifest)
    print("native checkpoint committed", flush=True)
    raise SystemExit(0)


if len(sys.argv) == 3 and sys.argv[1] == "--restore":
    counter = int(Path(sys.argv[2]).read_text())
    (output / "result.txt").write_text(str(counter + 1), encoding="ascii")
    committed_checkpoint()

signal.signal(signal.SIGUSR1, committed_checkpoint)
pid = os.getpid()
if len(sys.argv) == 3 and sys.argv[1] == "--pid-target":
    pid = int(sys.argv[2])
stat = Path(f"/proc/{pid}/stat").read_text().rsplit(") ", 1)[1].split()
boot = Path("/proc/sys/kernel/random/boot_id").read_text().strip()
(output / "native.pid").write_text(f"{pid}:{boot}:{stat[19]}\n", encoding="ascii")
print("native worker ready", flush=True)
while True:
    time.sleep(1)
