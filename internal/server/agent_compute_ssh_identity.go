package server

// PID alone is not a process incarnation. Bind it to boot ID and the kernel's
// start tick before observing or signalling it. These functions are shared by
// launch, probe and cancellation so they cannot disagree about ownership.
const agentSSHProcessIdentityShell = `
synon_process_identity() {
  local pid="$1" stat boot
  case "$pid" in ''|*[!0-9]*) return 1;; esac
  read -r stat < "/proc/$pid/stat" 2>/dev/null || return 1
  read -r boot < /proc/sys/kernel/random/boot_id || return 1
  stat=${stat##*) }
  set -- $stat
  [ "$#" -ge 20 ] || return 1
  [ "$1" != Z ] || return 1
  printf '%s:%s:%s' "$pid" "$boot" "${20}"
}
synon_process_alive() {
  local expected pid actual
  [ -s .wrapper_identity ] || return 1
  read -r expected < .wrapper_identity || [ -n "$expected" ] || return 1
  pid=${expected%%:*}
  actual=$(synon_process_identity "$pid") || return 1
  [ "$actual" = "$expected" ] && kill -0 "$pid" 2>/dev/null
}
synon_publish_identity() {
  local identity temporary
  identity=$(synon_process_identity "$1") || return 1
  printf '%s\n' "$1" > .wrapper_pid
  temporary=$(mktemp .wrapper_identity.XXXXXXXX) || return 1
  printf '%s\n' "$identity" > "$temporary"
  mv -f -- "$temporary" .wrapper_identity
}
`

// The intention is written before sbatch. If its response is lost, discover
// the accepted job by the confined workdir's stable tag, never submit again.
// A scheduler inventory error or empty ambiguous receipt is not permission to
// repeat a side effect. A completed wrapper's .phase remains authoritative.
func agentSSHSlurmRecoveryShell(tag string) string {
	quoted := shellSingleQuote(tag)
	return `
if [ -s .submit_intent ] && [ ! -s .scheduler_id ]; then
  inventory=$(squeue --noheader --user="$(id -un)" --name=` + quoted + ` --format='%i|%k') || exit 75
  scheduler_id=
  while IFS='|' read -r candidate comment; do
    [ "$comment" = ` + quoted + ` ] || continue
    case "$candidate" in ''|*[!0-9]*) exit 65;; esac
    [ -z "$scheduler_id" ] || exit 65
    scheduler_id=$candidate
  done <<< "$inventory"
  if [ -n "$scheduler_id" ]; then
    printf '%s\n' "$scheduler_id" > .scheduler_id
  elif [ -s .phase ]; then
    exit 0
  else
    exit 75
  fi
fi
if [ -s .scheduler_id ]; then
  scheduler_id=$(cat .scheduler_id)
  case "$scheduler_id" in ''|*[!0-9]*) exit 65;; esac
  # The accepted scheduler identity must never authorize another submission,
  # even when it has already left squeue and its terminal receipt is delayed.
  exit 0
fi
`
}

func agentSSHSlurmObservationShell(tag string) string {
	return `
if [ -s .scheduler_id ]; then
  scheduler_id=$(cat .scheduler_id)
  case "$scheduler_id" in ''|*[!0-9]*) printf unknown; exit 0;; esac
  inventory=$(squeue --noheader --user="$(id -un)" --jobs="$scheduler_id" --format='%i|%k') || { printf unknown; exit 0; }
  observed=
  while IFS='|' read -r candidate comment; do
    [ "$candidate" = "$scheduler_id" ] || continue
    [ "$comment" = ` + shellSingleQuote(tag) + ` ] || { printf unknown; exit 0; }
    observed=1
  done <<< "$inventory"
  if [ -n "$observed" ]; then printf running; exit 0; fi
  if [ -s .phase ]; then cat .phase; else printf unknown; fi
  exit 0
fi
`
}
