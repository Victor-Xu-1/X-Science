import { describe, expect, it } from 'vitest';
import {
  collectEmbeddedTaskStructures,
  parseTaskStructuredText,
} from '@/renderer/pages/conversation/Messages/components/publicTaskOutputParser';

const ambiguousDocuments = [
  ['prefixed Python object', "sample_alpha delta: {'MW': 2.5, 'TPSA': -6.25}"],
  ['prefixed JSON object', 'sample_alpha delta: {"MW": 2.5, "TPSA": -6.25}'],
  ['multiple prefixed Python objects', "sample_alpha delta: {'MW': 2.5}\nsample_beta delta: {'MW': -4.25}"],
  ['multiple prefixed JSON objects', 'sample_alpha delta: {"MW": 2.5}\nsample_beta delta: {"MW": -4.25}'],
  ['adjacent JSON documents', '{"MW": 2.5}\n{"MW": -4.25}'],
  ['mixed literal documents', '{\'MW\': 2.5}\n{"MW": -4.25}'],
  ['Python object with trailing text', "{'MW': 2.5} comparison incomplete"],
  ['JSON object with trailing text', '{"MW": 2.5} comparison incomplete'],
  ['incomplete outer context', "{'delta_vs_reference': {'MW': 2.5}"],
  ['prefixed record collection', 'comparison: {"records": [{"name": "sample_alpha", "score": 0.8}]}'],
] as const;

describe('public task structured-output document boundary', () => {
  it.each([
    {
      name: 'JSON record',
      source: ' \n{"n_retrieved": 1, "records": [{"name": "sample_alpha", "score": 0.8}]}\n ',
      expected: { n_retrieved: 1, records: [{ name: 'sample_alpha', score: 0.8 }] },
    },
    {
      name: 'Python record',
      source: " \n{'selected': True, 'note': None, 'records': [{'name': 'sample_alpha', 'score': 0.8}],}\n ",
      expected: { selected: true, note: null, records: [{ name: 'sample_alpha', score: 0.8 }] },
    },
    { name: 'JSON array', source: '[{"name": "sample_alpha"}]', expected: [{ name: 'sample_alpha' }] },
    { name: 'Python tuple', source: "({'name': 'sample_alpha'},)", expected: [{ name: 'sample_alpha' }] },
  ])('accepts a complete $name without losing record structure', ({ source, expected }) => {
    expect(parseTaskStructuredText(source)).toEqual(expected);
  });

  it.each(ambiguousDocuments)('does not promote a fragment from %s', (_name, source) => {
    expect(parseTaskStructuredText(source)).toBeNull();
    expect(collectEmbeddedTaskStructures({ stdout: source })).toEqual([]);
  });

  it.each(['stdout', 'preview', 'content', 'output'])('applies the same complete-document rule to %s', (key) => {
    const source = 'comparison: {"MW": 2.5}';
    expect(collectEmbeddedTaskStructures({ [key]: source })).toEqual([]);
    expect(collectEmbeddedTaskStructures({ [key]: '{"MW": 2.5}' })).toEqual([{ MW: 2.5 }]);
  });

  it.each([
    ['complete prefix before over-budget trailing text', '{"score": 0.8}' + ' '.repeat(512_000) + 'incomplete'],
    ['over-budget complete document', '{"note": "' + 'x'.repeat(512_000) + '"}'],
  ])('does not turn %s into a shorter promotable document', (_name, source) => {
    expect(parseTaskStructuredText(source)).toBeNull();
    expect(collectEmbeddedTaskStructures({ stdout: source })).toEqual([]);
  });

  it.each([
    '{"score": 1e309}',
    '{"records": [{"score": -1e309}]}',
    "{'score': 1e309}",
    "{'records': [{'score': -1e309}]}",
  ])('does not promote a literal with numeric overflow: %s', (source) => {
    expect(parseTaskStructuredText(source)).toBeNull();
    expect(collectEmbeddedTaskStructures({ stdout: source })).toEqual([]);
  });

  it.each([
    ['JSON', '{"__proto__": {"sample_marker": true}, "constructor": "record", "toString": 7}'],
    ['Python', "{'__proto__': {'sample_marker': True}, 'constructor': 'record', 'toString': 7}"],
  ])('preserves special %s dictionary keys as own data properties', (_name, source) => {
    const expected = JSON.parse('{"__proto__": {"sample_marker": true}, "constructor": "record", "toString": 7}');
    const parsed = parseTaskStructuredText(source) as Record<string, unknown>;

    expect(parsed).toEqual(expected);
    expect(Object.getPrototypeOf(parsed)).toBe(Object.prototype);
    expect(Object.hasOwn(parsed, '__proto__')).toBe(true);
    expect(parsed.__proto__).toEqual({ sample_marker: true });
    expect(Object.hasOwn(parsed, 'constructor')).toBe(true);
    expect(Object.hasOwn(parsed, 'toString')).toBe(true);
    expect(parsed.sample_marker).toBeUndefined();
  });
});
