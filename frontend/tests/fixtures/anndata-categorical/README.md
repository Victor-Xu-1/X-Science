# Synthetic AnnData categorical metadata

`categorical-template.h5ad` is a 7.3 KB first-party test scaffold, not a complete
AnnData dataset. It contains only root/group encoding metadata, dataframe
column metadata, and `/obs/cell_type` categorical metadata. In particular,
`ordered=false` is a genuine scalar HDF5 boolean enum (`FALSE=0`, `TRUE=1`).
There are no user records or scientific results in it.

`XScienceHdf5.worker.test.ts` clones this template into the locked browser
h5wasm filesystem and creates a complete tiny dense X, matching obs/var indices,
UTF-8 categories and signed categorical codes for each test. It then invokes the
production worker's message listener and checks actual parser output and raw
reads. Invalid-code cases intentionally violate categorical encoding to retain
existing filtering behavior. Routine tests use only the existing locked npm
dependencies, with no native HDF5 or Python runtime requirement.

To regenerate the metadata scaffold, from the frontend directory run:

```sh
node tests/fixtures/anndata-categorical/create-template.mjs
```

This optional generation step uses h5wasm 0.10.1 for metadata and the small
`conform-ordered-bool.py` helper with Python's standard-library ctypes and a
system `hdf5_serial` library for the scalar boolean enum. The high-level
h5wasm writer does not support writing enum attributes. The helper modifies
only the generated fixture, which is then read by the production h5wasm parser.
This is not external Python AnnData-reader compatibility certification.
