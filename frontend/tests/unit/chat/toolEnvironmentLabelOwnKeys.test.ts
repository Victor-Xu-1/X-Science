import type { NormalizedToolCall } from '@/common/chat/normalizeToolCall';
import { describe, expect, it } from 'vitest';
import {
  formatEnvironmentBlocker,
  publicEnvironmentOutputLabel,
  publicModeLabel,
} from '@/renderer/pages/conversation/Messages/toolDetails/detailFormatting';
import { buildToolPublicDetailPresentation } from '@/renderer/pages/conversation/Messages/toolDetails/detailProjection';

const locales = [
  {
    language: 'en-US',
    chinese: false,
    inputMode: 'Mode',
    outputMode: 'Check mode',
    operation: 'Operation',
    blockers: 'New environment limits',
    list: 'List available',
    readOnly: 'Read-only access',
    preflight: 'Environment preflight',
    reuse: 'Reuse environment',
    disk: 'Insufficient storage',
    packages: 'Required packages are missing',
  },
  {
    language: 'zh-CN',
    chinese: true,
    inputMode: '方式',
    outputMode: '检查方式',
    operation: '建议操作',
    blockers: '新建环境限制',
    list: '查看可用内容',
    readOnly: '只读访问',
    preflight: '环境预检',
    reuse: '复用已有环境',
    disk: '存储空间不足',
    packages: '缺少所需分析组件',
  },
] as const;

function projectEnvironment(
  language: string,
  input: Record<string, unknown>,
  output: Record<string, unknown>,
  status: NormalizedToolCall['status'] = 'completed'
) {
  const tool: NormalizedToolCall = {
    key: 'environment-label-record',
    name: 'manage_environments',
    status,
    input: JSON.stringify(input),
    output: JSON.stringify(output),
  };
  const original = { ...tool };
  const detail = buildToolPublicDetailPresentation(tool, language, null);
  expect(tool).toEqual(original);
  expect(detail.detailKind).toBe('environment');
  return detail;
}

describe.each(locales)('public environment labels in $language', (locale) => {
  // These are ordinary JSON string values, not inherited input properties or
  // prototype mutations. They must not select inherited translation entries.
  describe.each(['constructor', '__proto__'])('ordinary value %s', (value) => {
    it('does not treat an inherited name as a registered input mode', () => {
      expect(publicModeLabel(value, locale.chinese)).toBeNull();
    });

    it('uses the existing public scalar fallback for an unknown output operation', () => {
      expect(publicEnvironmentOutputLabel(value, locale.chinese)).toBe(value);
    });

    it('uses the existing public scalar fallback for an unknown blocker', () => {
      expect(formatEnvironmentBlocker(value, locale.chinese)).toBe(value);
    });

    it('renders rejected input without inventing a mode or changing its raw record', () => {
      const detail = projectEnvironment(locale.language, { mode: value }, {}, 'error');
      expect(detail.inputRows).toEqual([]);
      expect(JSON.stringify(detail.inputTree)).toContain(value);
    });

    it.each(['mode', 'operation'] as const)('projects the real output %s field without crashing', (key) => {
      const detail = projectEnvironment(locale.language, {}, { [key]: value });
      expect(detail.resultRows).toContainEqual({
        label: key === 'mode' ? locale.outputMode : locale.operation,
        value,
      });
      expect(JSON.stringify(detail.outputTree)).toContain(value);
    });

    it('projects the supported mutation_blockers collection without crashing', () => {
      const detail = projectEnvironment(locale.language, {}, { mutation_blockers: [value] }, 'blocked');
      expect(detail.resultCollections).toContainEqual({ label: locale.blockers, items: [value], total: 1 });
    });
  });

  it('keeps registered labels localized with their existing normalization', () => {
    expect(publicModeLabel('LIST', locale.chinese)).toBe(locale.list);
    expect(publicModeLabel('ro', locale.chinese)).toBe(locale.readOnly);
    expect(publicEnvironmentOutputLabel(' PREFLIGHT ', locale.chinese)).toBe(locale.preflight);
    expect(publicEnvironmentOutputLabel('reuse', locale.chinese)).toBe(locale.reuse);
    expect(formatEnvironmentBlocker(' INSUFFICIENT_DISK ', locale.chinese)).toBe(locale.disk);
    expect(formatEnvironmentBlocker('missing_packages', locale.chinese)).toBe(locale.packages);
  });

  it('preserves unknown public values and rejects private fallback text', () => {
    expect(publicModeLabel('custom check', locale.chinese)).toBeNull();
    expect(publicEnvironmentOutputLabel(' custom check ', locale.chinese)).toBe('custom check');
    expect(formatEnvironmentBlocker(' custom blocker ', locale.chinese)).toBe('custom blocker');
    for (const value of ['runtime policy detail', '/home/private/result']) {
      expect(publicModeLabel(value, locale.chinese)).toBeNull();
      expect(publicEnvironmentOutputLabel(value, locale.chinese)).toBeNull();
      expect(formatEnvironmentBlocker(value, locale.chinese)).toBeNull();
    }
    expect(formatEnvironmentBlocker(null, locale.chinese)).toBeNull();
  });

  it('projects registered input, output and blocker labels through the real detail builder', () => {
    const detail = projectEnvironment(
      locale.language,
      { mode: 'list' },
      { mode: 'preflight', operation: 'reuse', mutation_blockers: ['insufficient_disk'] }
    );
    expect(detail.inputRows).toEqual([{ label: locale.inputMode, value: locale.list }]);
    expect(detail.resultRows).toEqual([
      { label: locale.outputMode, value: locale.preflight },
      { label: locale.operation, value: locale.reuse },
    ]);
    expect(detail.resultCollections).toEqual([{ label: locale.blockers, items: [locale.disk], total: 1 }]);
  });

  it('keeps the public fallback and private filters intact in the real detail builder', () => {
    const detail = projectEnvironment(
      locale.language,
      { mode: 'runtime policy detail' },
      {
        mode: 'custom check',
        operation: 'runtime policy detail',
        mutation_blockers: ['custom blocker', 'runtime policy detail'],
        environment_generation: 'HIDDEN_GENERATION_VALUE',
      },
      'error'
    );
    expect(detail.inputRows).toEqual([]);
    expect(detail.resultRows).toEqual([{ label: locale.outputMode, value: 'custom check' }]);
    expect(detail.resultCollections).toEqual([{ label: locale.blockers, items: ['custom blocker'], total: 1 }]);
    const rendered = JSON.stringify(detail);
    expect(rendered).not.toContain('runtime policy detail');
    expect(rendered).not.toContain('HIDDEN_GENERATION_VALUE');
  });
});
