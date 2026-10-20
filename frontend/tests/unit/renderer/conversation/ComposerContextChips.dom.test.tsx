import { act, cleanup, fireEvent, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ComposerContextChips from '@/renderer/components/chat/SendBox/ComposerContextChips';
import type { ComposerContextItem } from '@/renderer/components/chat/SendBox/composerCompositionModel';
import { renderWithI18n } from '../../i18nTestUtils';

const items: ComposerContextItem[] = [
  { kind: 'artifact', artifactId: 'artifact-1', versionId: 'version-7', label: 'sample <&>.csv' },
  { kind: 'skill', name: 'table-cleaning', label: 'Table cleaning' },
  { kind: 'mcp', serverId: 'connector-3', label: 'Literature connector' },
];

afterEach(cleanup);

describe('ComposerContextChips localized removal', () => {
  it.each([
    ['en-US', 'Remove'],
    ['zh-CN', '移除'],
  ] as const)('names every context kind in %s without changing item identities', async (language, action) => {
    const onRemove = vi.fn();
    await renderWithI18n(<ComposerContextChips items={items} onRemove={onRemove} />, language);

    for (const item of items) {
      expect(screen.getByText(item.label, { exact: true })).toBeInTheDocument();
      fireEvent.click(screen.getByRole('button', { name: `${action} ${item.label}`, exact: true }));
    }

    expect(onRemove.mock.calls).toEqual([
      ['artifact:artifact-1:version-7'],
      ['skill:table-cleaning'],
      ['mcp:connector-3'],
    ]);
  });

  it('updates accessible names after language switching without removing draft context', async () => {
    const onRemove = vi.fn();
    const { i18n } = await renderWithI18n(<ComposerContextChips items={items} onRemove={onRemove} />, 'en-US');
    expect(screen.getByRole('button', { name: 'Remove sample <&>.csv', exact: true })).toBeInTheDocument();

    await act(async () => {
      await i18n.changeLanguage('zh-CN');
    });

    expect(screen.queryByRole('button', { name: 'Remove sample <&>.csv', exact: true })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '移除 sample <&>.csv', exact: true })).toBeInTheDocument();
    expect(screen.getAllByRole('button')).toHaveLength(3);
    expect(onRemove).not.toHaveBeenCalled();
  });

  it('removes the chip surface when the owning draft clears its context', async () => {
    const onRemove = vi.fn();
    const { rerender } = await renderWithI18n(<ComposerContextChips items={items} onRemove={onRemove} />, 'zh-CN');
    rerender(<ComposerContextChips items={[]} onRemove={onRemove} />);
    expect(screen.queryByTestId('composer-context-chips')).not.toBeInTheDocument();
    expect(onRemove).not.toHaveBeenCalled();
  });
});
