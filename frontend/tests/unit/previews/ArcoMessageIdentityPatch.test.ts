/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { patchArcoMessageSource, sourceSha256 } from '../../../scripts/arcoMessageIdentityPatch.mjs';
import { prepareArcoMessage } from '../../../scripts/prepareArcoMessage.mjs';

const require = createRequire(import.meta.url);
const formats = ['es', 'lib'] as const;
const expected = {
  es: 'a515b8732aad1a3a1237cf26d9c66b81a29972530603ab31ba3904323595e758',
  lib: '1702a87e096f90da336a805078a80ad7f5f197309e6b90e371123abaf68090f5',
};
const installed = Object.fromEntries(
  formats.map((format) => [
    format,
    readFileSync(require.resolve(`@arco-design/web-react/${format}/Message/useMessage.js`), 'utf8'),
  ])
) as Record<(typeof formats)[number], string>;
const owned: string[] = [];

function installation() {
  const root = mkdtempSync(join(tmpdir(), 'arco-message-identity-'));
  owned.push(root);
  const arco = join(root, 'node_modules', '@arco-design', 'web-react');
  mkdirSync(arco, { recursive: true });
  writeFileSync(join(arco, 'package.json'), JSON.stringify({ name: '@arco-design/web-react', version: '2.66.8' }));
  for (const format of formats) {
    const path = join(arco, format, 'Message', 'useMessage.js');
    mkdirSync(dirname(path), { recursive: true });
    writeFileSync(path, installed[format]);
  }
  return { root, arco };
}

afterEach(() => {
  for (const path of owned.splice(0)) rmSync(path, { recursive: true });
});

describe('locked Arco message preparation', () => {
  it.each(formats)('prepares locked %s bytes deterministically and accepts the exact result idempotently', (format) => {
    const output = patchArcoMessageSource(installed[format], format);
    expect(sourceSha256(output)).toBe(expected[format]);
    expect(patchArcoMessageSource(output, format)).toBe(output);
    expect(() => patchArcoMessageSource(installed[format] + '\n', format)).toThrow('source changed');
  });

  it('prepares and verifies only this installation, without changing either prepared module again', () => {
    const { root } = installation();
    const first = prepareArcoMessage(root);
    expect(first.files.map((file) => file.after)).toEqual(formats.map((format) => expected[format]));
    const second = prepareArcoMessage(root, true);
    expect(second.files.every((file) => file.before === file.after)).toBe(true);
  });

  it('rejects unknown package versions and an unknown module before writing', () => {
    const { root, arco } = installation();
    const es = join(arco, 'es', 'Message', 'useMessage.js');
    const before = readFileSync(es, 'utf8');
    writeFileSync(join(arco, 'lib', 'Message', 'useMessage.js'), 'unexpected upstream bytes');
    expect(() => prepareArcoMessage(root)).toThrow('source changed');
    expect(readFileSync(es, 'utf8')).toBe(before);
    writeFileSync(join(arco, 'package.json'), JSON.stringify({ name: '@arco-design/web-react', version: '2.66.17' }));
    expect(() => prepareArcoMessage(root)).toThrow('package identity changed');
  });

  it('rejects a node_modules link into another installation', () => {
    const shared = installation();
    const root = mkdtempSync(join(tmpdir(), 'arco-message-identity-'));
    owned.push(root);
    symlinkSync(join(shared.root, 'node_modules'), join(root, 'node_modules'), 'junction');
    expect(() => prepareArcoMessage(root)).toThrow('linked/shared node_modules');
  });

  it('rejects a package or module link before touching the shared target', () => {
    const shared = installation();
    const local = installation();
    rmSync(local.arco, { recursive: true });
    symlinkSync(shared.arco, local.arco, 'junction');
    expect(() => prepareArcoMessage(local.root)).toThrow('linked/shared Arco package');

    const ownModule = join(shared.arco, 'es', 'Message', 'useMessage.js');
    const target = join(shared.root, 'protected.js');
    writeFileSync(target, installed.es);
    rmSync(ownModule);
    symlinkSync(target, ownModule, 'file');
    expect(() => prepareArcoMessage(shared.root)).toThrow('linked/shared Arco Message source');
    expect(readFileSync(target, 'utf8')).toBe(installed.es);
  });
});
