# Root Memory preference admission

For a new ordinary root `user_message` without a `turn_intent` or an explicit
Memory override in either input data or runtime configuration, admission may
capture an explicit persisted root `_original_input.memory_mode` (`on`/`off`).
Absence does not manufacture a default; malformed persisted mode is an error.
Child messages, controls, input responses and explicit overrides are unchanged.

The capture is created inside the existing owner/stream admission transaction,
after resolving the actual root and project owner. It is stored only as this
server-produced top-level immutable event field:

```json
{"memoryPreferenceCapture":{"schema":1,"source":"root_original_input","rootFrameId":"<actual root>","mode":"off"}}
```

The same mode is frozen into `inputData.memory_mode` and
`runtimeConfig.memory_mode`. Capture is not accepted from caller maps. Exact
retries replay the persisted capture without rereading mutable preferences;
changing between explicit and derived input under the same identity conflicts.
Historical events without a capture retain their original exact comparison.

Runtime configuration treats camel/snake Memory aliases as one choice across
successive patches; the latest patch replaces the prior alias. Within a single
patch, existing snake-first precedence is preserved. Other configuration fields
are not normalized by this contract.

Canonical Memory-tool access requires an explicit ordinary-input `on` visible within the live
claim's admitted input revision and actual branch snapshot. Unknown or invalid
permission cannot be upgraded by global defaults or a newer queued input. Fresh
global/user/session/root opt-outs may still revoke that permission. Existing
receipt replay remains independent of granting a fresh write. Noncanonical
legacy access retains its existing compatibility path, including legacy Frame-ref
streams with a real Transcript claim. The same live-claim snapshot reads existing
FrameAuthority: valid payload authority uses the new claimed-permission check,
valid legacy authority uses the original guard, and unknown/contradictory authority
fails closed. In the claimed Memory projection only, explicit ordinary InputData
Memory takes precedence over the same event's runtimeConfig, including when no
runtimeConfig is present. Present-invalid values are not inherited as `on`.
A control-classified response patch
cannot create claimed permission, though a fresh control opt-out can revoke it.

This backend contract does not claim completion of frontend ACK/readback,
uncertain preference presentation, or the full Memory interaction workflow.
