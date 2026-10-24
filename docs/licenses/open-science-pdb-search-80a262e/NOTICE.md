# Open Science PDB search adapters

Copyright 2026 AIPOCH. The original Apache License 2.0 is retained in
[LICENSE](LICENSE), SHA-256
`b038148f912bdd6e61e33e56214d02c4d8a9640f7a637e21dfcf939332816979`.

The PDB sequence and deposited-chain structure-similarity contracts and result
projections were adapted from [aipoch/open-science](https://github.com/aipoch/open-science)
at commit `80a262e89f1dfa3d0d686c6c52833b91584f2db8`:

- `packages/connector-builtins/src/structures-pdb.ts`
- `packages/connector-builtins/src/structures-pdb-similarity.ts`
- The two compact method descriptions in
  `src/renderer/src/pages/settings/connector-copy.ts`

X-Science changes implement the methods in the existing Python Structures MCP
connector, keep one SDK input/permission/execution/result authority, and use a
native asynchronous fixed-endpoint RCSB reader. Sequence paging, metadata and
two total transient retries share a 50-second budget to fit the current outer
MCP call; the reference sequence default is 120 seconds. Structure similarity
retains the 25-second/500000-byte/no-retry boundary. Additional validation rejects
invalid JSON, duplicate keys, non-finite metrics and mismatched entity identity;
actual missing metadata remains unknown rather than a fabricated empty success.

RCSB PDB data terms remain separate from source-code licensing:
[RCSB usage policy](https://www.rcsb.org/pages/usage-policy). No PDB coordinate
dataset, external model, browser credential or operator research data is
redistributed by these adapters. This notice covers this increment only; it does
not relicense all historical vendored biomedical tools or alter their notices.
