# X-Science Browser Connector

`source/` is the sole current connector source. The v0.6.11 ZIP is generated
from these reviewed files, not patched in place. The exact earlier archive,
revision and digest remain historical provenance in `manifest.json` and the
builder; they do not supply a competing current protocol.

From the repository root:

```sh
python3 -B -m scripts.packaging.build_browser_extension --repo . --check --fetch-source
python3 -B -m unittest scripts.packaging.test_browser_extension_branding
node --test scripts/packaging/test_connector_namespace.mjs
```

The fetch is explicit and obtains only a missing pinned public Git object. The
builder verifies the original permission/origin/extension-identity settings and
the approved unmodified orange artwork. Product patch changes do not change
this independently versioned connector archive.

## Existing browser profiles

Replace the files in the **same installed extension directory** and reload that
extension. Removing/reinstalling into a differently named unpacked directory can
change its extension ID. The software never copies capabilities or authentication
between extension origins or browser profiles. A new identity requires normal
explicit folder authorization and login.

The one-time capability migration atomically copies bounded original folder
records to the canonical database without overwriting a conflicting inventory.
It retains the original store only for recovery; after the durable marker is
written, it never reads that store as a live fallback. Operations still query
real browser permissions; copied records do not manufacture a grant. Errors
preserve both inventories.

The application owns page-origin preference migration. The content script reads
only canonical session/preference keys, and the server independently validates
the session. An expired or previous-domain session reconnects through normal
login; moving a token string never proves it is valid.

The default application/connector endpoint is `http://127.0.0.1:8765`, also
reachable as `http://localhost:8765`. A saved explicitly configured endpoint is
preserved. Only the native extension UI can select a different server. Page
bridge requests and status broadcasts must match the configured server's origin
(including scheme and port); subframes, opaque origins and other local ports
cannot connect, change settings, decide approvals or receive local-file status.
Approved commands from the configured authenticated server may intentionally
download HTTP(S) resources on a local or private network. The connector is not a
public-only server fetch proxy; URL scheme checks, existing approval and transfer
limits still apply, and the application server independently validates sessions.

Packaging and controlled API-contract tests are not acceptance evidence for an
installed browser profile. The builder never installs or grants an extension.
