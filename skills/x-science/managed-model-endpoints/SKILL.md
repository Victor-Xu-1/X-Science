---
name: managed-model-endpoints
description: Register an explicitly approved, durably owned local model service or a remote HTTPS inference API. Local registrations declare a typed native-unit or Docker owner and bounded output. Use the model runbook for image, cache, internal port and readiness details; never guess ownership or repeat an accepted script after an unknown outcome.
license: Apache-2.0
tools:
  - compute_provider
  - compute_provider_inference
---

# Managed model endpoints

Use this Skill to register a service. Use `using-model-endpoint` to call a
registered service through `compute_provider(provider="<registered-name>",
code="...")`. The daemon supplies `BASE_URL`; an endpoint is not a kernel
environment. Never run a second service, poll readiness in a compute cell, or
print the family credential to work around a lifecycle error.

The model's runbook supplies the image, registry login, internal port, cache
mount target, compute requirements and readiness route. Read it first. Local
ownership is a runtime contract, not inferred by parsing the approved script.

## Enablement and credentials

The user connects the Model endpoints family in Compute settings and selects
Local or remote/URL mode. Register with `credential="NVIDIA_API_KEY"`, the
credential name, never a value. Approved bootstrap/native scripts receive only
their explicit approved credential, `HOST_PORT`, `SERVICE_DIR`, an allowlisted
OS base environment and generated owner labels. Runtime-owned Docker controls
use the allowlisted OS base environment, not a replay of that script environment.
Arbitrary Web process environment variables are not inherited. The credential
must not appear in argv, logs,
unrelated generated files or a kernel environment; approved registry login may
store auth only in the private service-scoped Docker configuration. A remote credential is provided only
to its isolated inference cell as `INFER_API_KEY`.

## Local Docker registration

Allocate a local port once, compose the scripts from the runbook, and register
the scripts, readiness route, exact Docker context/container locator and finite
output policy together:

```python
port = host.model_endpoints.free_port()
host.model_endpoints.register(
    name="model-service",
    url=f"http://127.0.0.1:{port}",
    skill="<model-runbook-skill>",
    credential="NVIDIA_API_KEY",
    start=START_SCRIPT,  # First fresh Docker bootstrap only.
    stop=":",  # Required approval field; owned Docker Stop is runtime control.
    live="<readiness-route-from-runbook>",
    owner={
        "version": 1,
        "kind": "docker",
        "docker": {"context": "default", "container": "model-service"},
        "output": {"segmentBytes": 1048576, "segments": 4},
    },
)
```

Use literal `127.0.0.1`, not `localhost`. The allocated port is in 20000–29999.
Reuse the existing URL when proposing a repair; do not allocate a different port
to hide an existing service. Keep endpoint and container names descriptive and
identical to the approved locator. `default` here is an explicit Docker context,
not permission to use a different ambient context.

The first create must include immutable ownership labels and a positively
configured finite Docker logging policy. An illustrative creation fragment is:

```bash
mkdir -p "$SERVICE_DIR/cache" "$SERVICE_DIR/.docker"
chmod 700 "$SERVICE_DIR/.docker"
export DOCKER_CONFIG="$SERVICE_DIR/.docker"
# Perform only the registry login/pull required by the runbook. Use password
# stdin; never interpolate the credential value into argv or echo it to logs.
docker --context=default run -d --name model-service --restart=no \
  --label "ai.x-science.endpoint-instance=$X_SCIENCE_ENDPOINT_INSTANCE" \
  --label "ai.x-science.endpoint-user=$X_SCIENCE_ENDPOINT_USER" \
  --label "ai.x-science.endpoint-approval=$X_SCIENCE_ENDPOINT_APPROVAL" \
  --log-driver=local --log-opt=max-size=4m --log-opt=max-file=2 \
  -p "127.0.0.1:${HOST_PORT}:<internal-port>" \
  -e NVIDIA_API_KEY \
  -v "$SERVICE_DIR/cache:<runbook-cache-target>" \
  <runbook-image>
```

The approved start script is used only for the first fresh, unadopted Docker
bootstrap, before an owner identity has been established. After positive owner
identification, warm Start and Stop are runtime-owned Docker operations using
the explicit context and full immutable container ID as typed arguments, not
shell commands or a replay of the old name-based start/stop scripts. Failed-start
cleanup also requires its original durable cleanup authority and positive owner
identity before the runtime controls that exact ID. A same-name replacement is
never a substitute for the original owner.

The shared registration/approval contract still requires both script fields.
For a new typed Docker registration, `stop=":"` above is an approved descriptor
placeholder, not the teardown implementation. Existing registration bytes and
hashes must remain unchanged unless the normal approval flow authorizes a
change; do not rewrite old stop scripts merely to match this example. Native
registrations retain their approved native lifecycle scripts.

Establishing a live owner still requires actual daemon/ID, applicable ownership
labels, loopback binding and effective bounded logging. A daemon default of
`json-file` alone is not finite log retention. Do not remove/recreate a foreign
or mismatched container, disable logging, use an unbounded driver, or guess
ownership from an open port. A successful Docker control is not proof of HTTP
readiness.

Scripts must use the same explicit context as the owner descriptor. Keep their
bytes stable and readable. `-e NVIDIA_API_KEY` passes the environment name rather
than exposing its value in argv. Use cache permissions appropriate to the image's
documented UID; do not use world-writable caches or unapproved recursive chown.
Stop parks the original container; caches are not removed. Warm Start after a
positive Stop uses a new action/ref and admission CAS, then controls the original
container ID rather than rerunning bootstrap. The original physical container
identity remains authoritative.

## Native process registration

For a service which remains in the product's systemd user unit, declare:

```python
owner={"version": 1, "kind": "native-unit",
       "output": {"segmentBytes": 1048576, "segments": 4}}
```

The approved shell action must finish after launching its service in that unit;
the service may keep writing to the durable worker's bounded output sink. The
worker/cgroup, not the Web request or shell stdout EOF, owns its lifetime.
`setsid` does not move a child out of the owned cgroup. Readiness also verifies
that the literal loopback listener belongs to that exact physical owner. A
native script must not delegate its actual service to another unit/container
and then claim the native owner descriptor covers it. When execution is needed,
native Stop retains its approved script and exact unit/cgroup stopped proof;
Docker's typed controls do not grant authority over an unrelated native daemon.

## Approval, recovery and historical registrations

Changed scripts or owner descriptors require the existing approval flow. An
identical registration does not reset live/starting/stopping ownership, clear a
claim or cause another start. Unresolved owners must be stopped with positive
evidence before a configuration can be replaced or deleted.

Historical registrations remain readable with their original serialized bytes
and approval hash. They are not silently executed through an unowned fallback.
A pre-existing Docker service can be explicitly adopted by supplying its full
observed `owner.adopt` identity (`kind`, daemon ID and immutable 64-hex container
ID) with the approved Docker locator. The server binds original registration
bytes/hash into the new approval. It first records `owner_unverified`; only
current actual ID, binding, bounded logging and readiness proof can make it live.
No start script is run for the initial adoption. An arbitrary historical native
daemon without a provable durable owner remains explicitly unknown: do not
claim it was adopted, move it, kill it by port/name or fabricate a successful
exit. Report the exact missing ownership evidence.

A Web restart observes the original accepted action. A durable intent which has
not been accepted may be dispatched once through its original admission CAS;
accepted actions with missing/contradictory results are never blindly repeated.
An ordinary Web disconnect/drain is not a user cancellation. Script exit,
output EOF, readiness, owner identity and stopped proof are separate facts.

Local output recovery is also separate from owner authority. A clean validated
durable cursor restores the recorded active ring segment, offset and counters;
segment sizes or timestamps alone do not establish output order. Missing,
malformed or pending historical cursor state is not silently repaired. The
worker preserves the old files and emits an explicit `OutputWarning`: prior
continuity and cumulative counters are unknown, and new pipe output is drained
but not persisted. Its bounded preview and sequence/dropped-byte counts describe
only that reader, not reconstructed historical output. This mode does not waive
owner or private-directory checks, block an otherwise authorized exact Stop, or
turn actual new disk-write failures into successful persistence. Even a clean
cursor does not reconstruct output produced while its worker was absent.

Failure preserves retained output, the original owner and known/unknown execution
facts. Do not loop over re-registration, `exec_peek`, Start or Stop to make a
failure disappear. Known physical ownership with a failed stop is retained for
an explicit retry. A positively stopped owner can be removed through its owned
teardown path; unknown/live owners cannot be erased for convenience.
Local actions retain the existing finite policy (Start/readiness up to two
minutes, Stop up to one minute); this is not a claim that arbitrary image or
weight preparation will finish inside that policy.

## Remote HTTPS endpoints

For an upstream API, use `url="https://<configured-upstream>/<path>"` and omit
`start`, `stop`, `live` and `owner`. The configured host must match exactly.
Remote registration/inference/teardown does not require local systemd or Docker
and never runs a local lifecycle script. Calls use the existing exact-host TLS
and credential-FD boundary. Disconnecting the family stops owned local services
and removes eligible registrations; a failed/unknown stop retains its record,
while caches and unrelated services remain untouched.
