// Regenerate the tiny synthetic metadata template. Tests require only h5wasm;
// this optional regeneration step also needs Python and a system HDF5 library.
import { execFileSync } from 'node:child_process';
import { writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import h5wasm from 'h5wasm';

const { FS } = await h5wasm.ready;
const filename = '/categorical-template.h5ad';
const file = new h5wasm.File(filename, 'w');
function encoding(entity, type, version) {
  entity.create_attribute('encoding-type', type);
  entity.create_attribute('encoding-version', version);
}
encoding(file, 'anndata', '0.1.0');
for (const name of ['obs', 'var']) {
  const group = file.create_group(name);
  encoding(group, 'dataframe', '0.2.0');
  group.create_attribute('_index', '_index');
  group.create_attribute('column-order', name === 'obs' ? ['cell_type'] : [], null, 'S32');
}
for (const name of ['obsm', 'varm', 'layers', 'uns', 'obsp', 'varp']) {
  encoding(file.create_group(name), 'dict', '0.1.0');
}
const categorical = file.get('/obs').create_group('cell_type');
encoding(categorical, 'categorical', '0.2.0');
categorical.create_attribute('ordered', new Uint8Array([0]), null, '<B');
file.close();
const destination = fileURLToPath(new URL('categorical-template.h5ad', import.meta.url));
writeFileSync(destination, FS.readFile(filename));
FS.unlink(filename);
execFileSync('python3', [fileURLToPath(new URL('conform-ordered-bool.py', import.meta.url)), destination]);
