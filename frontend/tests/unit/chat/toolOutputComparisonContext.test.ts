import type { NormalizedToolCall } from '@/common/chat/normalizeToolCall';
import { describe, expect, it } from 'vitest';
import {
  isPublicDetailField,
  publicDetailFieldLabel,
} from '@/renderer/pages/conversation/Messages/toolDetails/detailFieldPolicy';
import { buildToolPublicDetailPresentation } from '@/renderer/pages/conversation/Messages/toolDetails/detailProjection';

type TerminalStatus = 'completed' | 'error';

const failureText = 'FileNotFoundError: requested output directory does not exist';
const project = (stdout: string, status: TerminalStatus, extra: Record<string, unknown> = {}, language = 'en-US') => {
  const tool: NormalizedToolCall = {
    key: 'comparison-fixture',
    name: 'python',
    status,
    input: JSON.stringify({ code: 'print(comparison)' }),
    output: JSON.stringify({
      ok: status === 'completed',
      stdout,
      stderr: status === 'error' ? failureText : '',
      files_written: [],
      exit_status: status === 'error' ? 1 : 0,
      ...extra,
    }),
  };
  return buildToolPublicDetailPresentation(tool, language, status === 'completed' ? 'Completed' : 'Incomplete');
};

const ambiguousComparisons = [
  [
    'prefixed Python comparisons',
    "sample_alpha delta_vs_reference: {'MW': 2.5, 'TPSA': -6.25}\nsample_beta delta_vs_reference: {'MW': -4.25}",
  ],
  [
    'prefixed JSON comparisons',
    'sample_alpha delta_vs_reference: {"MW": 2.5, "TPSA": -6.25}\nsample_beta delta_vs_reference: {"MW": -4.25}',
  ],
  ['adjacent JSON records', '{"MW": 2.5}\n{"MW": -4.25}'],
  ['trailing Python text', "{'MW': 2.5, 'TPSA': -6.25} comparison incomplete"],
  ['trailing JSON text', '{"MW": 2.5, "TPSA": -6.25} comparison incomplete'],
  ['incomplete nested comparison', "{'delta_vs_reference': {'MW': 2.5, 'TPSA': -6.25}"],
] as const;

describe.each(['completed', 'error'] as const)('comparison output context after %s execution', (status) => {
  it.each(ambiguousComparisons)('retains %s as raw evidence rather than unlabeled measurements', (_name, stdout) => {
    const detail = project(stdout, status);

    expect(detail.outputBlocks).toContainEqual(expect.objectContaining({ label: 'STDOUT', content: stdout }));
    for (const value of ['2.5', '-6.25', '-4.25']) {
      expect(detail.resultRows.map((row) => row.value)).not.toContain(value);
    }
    expect(detail.resultCollections).toEqual([]);
    expect(detail.resultSummary).toBe(status === 'completed' ? 'Completed' : 'Incomplete');
    if (status === 'error') {
      expect(detail.outputBlocks).toContainEqual(expect.objectContaining({ label: 'STDERR', content: failureText }));
    }
  });

  it.each([
    [
      'JSON',
      '{"n_retrieved": 2, "records": [{"name": "sample_alpha", "score": 0.8}, {"name": "sample_beta", "score": 0.6}]}',
    ],
    [
      'Python',
      "{'n_retrieved': 2, 'records': [{'name': 'sample_alpha', 'score': 0.8}, {'name': 'sample_beta', 'score': 0.6}]}",
    ],
  ])('keeps complete %s record documents available without discarding record identity', (_name, stdout) => {
    const detail = project(stdout, status);

    expect(detail.resultCollections).toContainEqual({
      label: 'Records',
      total: 2,
      items: ['Name: sample_alpha · Score: 0.8', 'Name: sample_beta · Score: 0.6'],
    });
    expect(detail.outputBlocks).toContainEqual(expect.objectContaining({ label: 'STDOUT', content: stdout }));
  });

  it.each([
    ['JSON', '{"name": "sample_alpha", "delta_vs_reference": {"MW": 2.5, "TPSA": -6.25}}'],
    ['Python', "{'name': 'sample_alpha', 'delta_vs_reference': {'MW': 2.5, 'TPSA': -6.25}}"],
  ])('does not flatten nested %s comparison values into absolute measurement rows', (_name, stdout) => {
    const detail = project(stdout, status);

    for (const value of ['2.5', '-6.25']) {
      expect(detail.resultRows.map((row) => row.value)).not.toContain(value);
    }
    expect(detail.outputBlocks).toContainEqual(expect.objectContaining({ label: 'STDOUT', content: stdout }));
  });
});

describe('public measurement labels without weakening private-field policy', () => {
  it.each(['en-US', 'zh-CN'])('retains stable chemistry metadata labels in %s', (language) => {
    const detail = project('', 'completed', { MW: 210.5, cLogP: 1.25, TPSA: 43, HBD: 1, HBA: 3 }, language);

    expect(detail.resultRows).toEqual(
      expect.arrayContaining([
        { label: 'MW', value: '210.5' },
        { label: 'cLogP', value: '1.25' },
        { label: 'TPSA', value: '43' },
        { label: 'HBD', value: '1' },
        { label: 'HBA', value: '3' },
      ])
    );
  });

  it.each(['cLogP', 'c_log_p', 'clogp'])('treats the exact %s chemistry field as metadata, not a log channel', (key) => {
    expect(isPublicDetailField(key, 'summary')).toBe(true);
    expect(publicDetailFieldLabel(key)).toBe('cLogP');
  });

  it.each(['log', 'logs', 'request_log', 'runtime_logs', 'transport_log', 'debug_log'])(
    'keeps %s out of summary measurements',
    (key) => {
      expect(isPublicDetailField(key, 'summary')).toBe(false);
      expect(project('', 'completed', { [key]: 7.25 }).resultRows).not.toContainEqual(
        expect.objectContaining({ value: '7.25' })
      );
    }
  );

  it.each(['api_key', 'authorization', 'runtimeGeneration', 'cLogP_api_key', 'cLogP_secret', 'cLogP_token'])(
    'continues to exclude private %s from summary and expanded data',
    (key) => {
      expect(isPublicDetailField(key, 'summary')).toBe(false);
      expect(isPublicDetailField(key, 'tree')).toBe(false);
      const detail = project('', 'completed', { [key]: 'private-fixture-marker' });
      expect(JSON.stringify(detail.resultRows)).not.toContain('private-fixture-marker');
      expect(JSON.stringify(detail.outputTree)).not.toContain('private-fixture-marker');
    }
  );
});
