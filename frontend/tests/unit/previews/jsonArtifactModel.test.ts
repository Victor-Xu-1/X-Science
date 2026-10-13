import {
  MAX_STRUCTURED_JSON_CHARS,
  MAX_STRUCTURED_JSON_NODES,
  collectJsonMatchPaths,
  collectJsonContainerPaths,
  jsonValueMatches,
  parseJsonDocument,
} from '@/renderer/pages/conversation/Preview/components/viewers/jsonDocumentModel';
import { describe, expect, it } from 'vitest';

describe('jsonArtifactModel', () => {
  it('parses JSON and counts nested nodes', () => {
    expect(parseJsonDocument('{"name":"screen","phases":[{"title":"Plan"}]}')).toEqual({
      status: 'ready',
      value: { name: 'screen', phases: [{ title: 'Plan' }] },
      nodeCount: 5,
    });
  });

  it('rejects invalid and oversized structured previews without hiding the source', () => {
    expect(parseJsonDocument('{broken')).toMatchObject({ status: 'invalid' });
    expect(parseJsonDocument(' '.repeat(MAX_STRUCTURED_JSON_CHARS + 1))).toMatchObject({ status: 'too-large' });
  });

  it.each([
    '1760000000000000001',
    '9007199254740993',
    '18446744073709551615',
    '-9007199254740993',
    '9007199254740992',
    '1e400',
    '-1e400',
    '1e-400',
    '-1e-400',
    '0.100000000000000005',
    '9007199254740991.1',
    '1.0000000000000001',
    '1.23456789012345678',
    '4e-324',
    '-0',
    '-0.000e+400',
  ])('uses exact source instead of a misleading numeric tree for %s', (token) => {
    expect(parseJsonDocument(`{"measurement":${token}}`)).toEqual({ status: 'imprecise-numbers' });
  });

  it.each([
    '0.1',
    '1.25',
    '-12.5',
    '1e-300',
    '5e-324',
    '2.5e3',
    '1.2300e+003',
    '1.00e-2',
    '9007199254740991.0',
    '0e400',
  ])('keeps the tree for a faithfully displayed finite decimal %s', (token) => {
    expect(parseJsonDocument(`[${token}]`)).toMatchObject({ status: 'ready', nodeCount: 2 });
  });

  it('skips numeric strings, escaped quotes and backslashes, and numeric keys', () => {
    const content = JSON.stringify({
      '9007199254740993': '1e400',
      quoted: '"1e-400" and \\9007199254740993',
      nested: ['-0', '0.100000000000000005'],
      safe: 0.1,
    });
    expect(parseJsonDocument(content)).toMatchObject({ status: 'ready' });
    expect(parseJsonDocument('{"escaped":"\\\"1e400\\\"","value":1e400}')).toEqual({ status: 'imprecise-numbers' });
  });

  it('normalizes zero without expanding or calculating an unbounded exponent', () => {
    expect(parseJsonDocument(`0e${'9'.repeat(10_000)}`)).toMatchObject({ status: 'ready', value: 0, nodeCount: 1 });
    expect(parseJsonDocument(`1e${'9'.repeat(10_000)}`)).toEqual({ status: 'imprecise-numbers' });
    expect(parseJsonDocument(`1e${'0'.repeat(10_000)}0`)).toMatchObject({ status: 'ready', value: 1 });
    expect(parseJsonDocument(`0.${'0'.repeat(10_000)}1e10001`)).toMatchObject({ status: 'ready', value: 1 });
  });

  it('normalizes a full-budget zero run followed by a nonzero digit without backtracking', () => {
    const content = `0.1${'0'.repeat(MAX_STRUCTURED_JSON_CHARS - 4)}1`;
    expect(content.length).toBe(MAX_STRUCTURED_JSON_CHARS);
    expect(parseJsonDocument(content)).toEqual({ status: 'imprecise-numbers' });
    expect(parseJsonDocument(`${content}0`)).toEqual({
      status: 'too-large',
      reason: 'characters',
      limit: MAX_STRUCTURED_JSON_CHARS,
    });
  }, 5_000);

  it('matches nested keys and values and collects expandable paths', () => {
    const value = { phases: [{ title: 'Download matrix' }], enabled: true };
    expect(jsonValueMatches('root', value, 'download')).toBe(true);
    expect(jsonValueMatches('root', value, 'missing')).toBe(false);
    expect([...collectJsonMatchPaths(value, 'download')!]).toEqual([
      '$.phases[0].title',
      '$.phases[0]',
      '$.phases',
      '$',
    ]);
    expect([...collectJsonContainerPaths(value)]).toEqual(['$', '$.phases', '$.phases[0]']);
  });

  it('enforces the exact node boundary for arrays', () => {
    expect(parseJsonDocument(JSON.stringify(Array(MAX_STRUCTURED_JSON_NODES - 1).fill(0)))).toMatchObject({
      status: 'ready',
      nodeCount: MAX_STRUCTURED_JSON_NODES,
    });
    expect(parseJsonDocument(JSON.stringify(Array(MAX_STRUCTURED_JSON_NODES).fill(0)))).toEqual({
      status: 'too-large',
      reason: 'nodes',
      limit: MAX_STRUCTURED_JSON_NODES,
    });
  });

  it('returns the bounded node fallback for a 150000-element array without argument spreading', () => {
    const content = `[${'0,'.repeat(149_999)}0]`;
    expect(content.length).toBe(300_001);
    expect(parseJsonDocument(content)).toEqual({
      status: 'too-large',
      reason: 'nodes',
      limit: MAX_STRUCTURED_JSON_NODES,
    });
  });

  it('bounds wide objects before materializing all values as function arguments', () => {
    const content = `{${Array.from({ length: 130_000 }, (_, index) => `"${index}":0`).join(',')}}`;
    expect(content.length).toBeLessThan(MAX_STRUCTURED_JSON_CHARS);
    expect(parseJsonDocument(content)).toEqual({
      status: 'too-large',
      reason: 'nodes',
      limit: MAX_STRUCTURED_JSON_NODES,
    });
  });

  it('accounts for both visited and pending nodes in nested array and object containers', () => {
    const boundary = { first: Array(5_998).fill(0), second: Array(5_999).fill(0) };
    expect(parseJsonDocument(JSON.stringify(boundary))).toMatchObject({
      status: 'ready',
      nodeCount: MAX_STRUCTURED_JSON_NODES,
    });
    boundary.first.push(0);
    expect(parseJsonDocument(JSON.stringify(boundary))).toEqual({
      status: 'too-large',
      reason: 'nodes',
      limit: MAX_STRUCTURED_JSON_NODES,
    });
  });

  it('counts own JSON fields including prototype-like names', () => {
    expect(
      parseJsonDocument('{"__proto__":{"inherited":0},"constructor":0,"hasOwnProperty":0,"toString":0}')
    ).toMatchObject({
      status: 'ready',
      nodeCount: 6,
    });
  });
});
