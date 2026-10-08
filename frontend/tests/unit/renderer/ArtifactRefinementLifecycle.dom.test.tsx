/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ArtifactEditRefinementPanel } from '@/renderer/pages/artifact/ArtifactEditRefinementPanel';
import type { SynonBiomedAppliedArtifactEdit } from '@/renderer/services/synonBiomedAnnotations';
import { renderWithI18n } from '../i18nTestUtils';

const service = vi.hoisted(() => ({ suggest: vi.fn(), apply: vi.fn() }));
vi.mock('@/renderer/services/synonBiomedAnnotations', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/synonBiomedAnnotations')>()),
  suggestSynonBiomedArtifactEdit: service.suggest,
  applySynonBiomedArtifactEdit: service.apply,
}));

const receipt: SynonBiomedAppliedArtifactEdit = {
  artifactId: 'artifact-1',
  versionId: 'version-2',
  versionNumber: 2,
  parentVersionId: 'version-1',
  carriedAnnotations: [],
};
const deferred = <T,>() => {
  let resolve!: (result: T) => void;
  const promise = new Promise<T>((complete) => {
    resolve = complete;
  });
  return { promise, resolve };
};
const props = {
  artifactId: 'artifact-1',
  versionId: 'version-1',
  selectedText: 'Original passage',
  initialInstruction: 'Clarify the passage',
};
const renderEditor = async (ui: React.ReactElement) => {
  let view!: Awaited<ReturnType<typeof renderWithI18n>>;
  await act(async () => {
    view = await renderWithI18n(ui, 'en-US');
  });
  return view;
};

beforeEach(() => {
  service.suggest.mockReset().mockResolvedValue('Revised passage');
  service.apply.mockReset().mockResolvedValue(receipt);
});

describe('Artifact refinement request ownership', () => {
  it('resets material selection state and rejects a late suggestion from the previous version', async () => {
    const pending = deferred<string>();
    service.suggest.mockReturnValueOnce(pending.promise);
    const view = await renderEditor(<ArtifactEditRefinementPanel {...props} onClose={vi.fn()} onApplied={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Generate revision' }));
    view.rerender(
      <ArtifactEditRefinementPanel
        {...props}
        versionId='version-3'
        selectedText='New passage'
        initialInstruction='New request'
        onClose={vi.fn()}
        onApplied={vi.fn()}
      />
    );
    await act(async () => {
      pending.resolve('Obsolete revision');
      await pending.promise;
    });
    expect(screen.getByRole('textbox', { name: 'Revision request or question' })).toHaveValue('New request');
    expect(screen.queryByText('Obsolete revision')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Generate revision' })).toBeEnabled();
  });

  it('does not forward an old immutable-write receipt after unmount', async () => {
    const pending = deferred<SynonBiomedAppliedArtifactEdit>();
    service.apply.mockReturnValueOnce(pending.promise);
    const onApplied = vi.fn();
    const view = await renderEditor(<ArtifactEditRefinementPanel {...props} onClose={vi.fn()} onApplied={onApplied} />);
    fireEvent.click(screen.getByRole('button', { name: 'Generate revision' }));
    await screen.findByText('Revision suggestion');
    fireEvent.click(screen.getByRole('button', { name: 'Apply as new version' }));
    expect(service.apply).toHaveBeenCalledWith('artifact-1', 'version-1', {
      selectedText: 'Original passage',
      replacementText: 'Revised passage',
    });
    view.unmount();
    await act(async () => {
      pending.resolve(receipt);
      await pending.promise;
    });
    expect(onApplied).not.toHaveBeenCalled();
  });

  it('retains a saved version when display refresh fails and retries only its presentation', async () => {
    const onApplied = vi
      .fn()
      .mockRejectedValueOnce(new Error('fixture display refresh failure'))
      .mockResolvedValue(undefined);
    await renderEditor(<ArtifactEditRefinementPanel {...props} onClose={vi.fn()} onApplied={onApplied} />);
    fireEvent.click(screen.getByRole('button', { name: 'Generate revision' }));
    await screen.findByText('Revision suggestion');
    fireEvent.click(screen.getByRole('button', { name: 'Apply as new version' }));
    await screen.findByRole('alert');
    expect(screen.getByText('Revision applied and a new version created')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Apply as new version' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Show saved version' }));
    await waitFor(() => expect(onApplied).toHaveBeenCalledTimes(2));
    expect(service.apply).toHaveBeenCalledTimes(1);
    expect(onApplied).toHaveBeenLastCalledWith(receipt);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('can close a pending view without claiming the server operation was cancelled', async () => {
    const pending = deferred<string>();
    service.suggest.mockReturnValueOnce(pending.promise);
    const onClose = vi.fn();
    const view = await renderEditor(<ArtifactEditRefinementPanel {...props} onClose={onClose} onApplied={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Generate revision' }));
    fireEvent.click(within(screen.getByRole('dialog').querySelector('footer')!).getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalledTimes(1);
    view.unmount();
    await act(async () => {
      pending.resolve('Late revision');
      await pending.promise;
    });
    expect(service.suggest).toHaveBeenCalledTimes(1);
    expect(service.apply).not.toHaveBeenCalled();
  });

  it('exposes one pressed diff/full choice and preserves manual text across the two views', async () => {
    await renderEditor(<ArtifactEditRefinementPanel {...props} onClose={vi.fn()} onApplied={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Generate revision' }));
    await screen.findByText('Revision suggestion');
    expect(screen.getByRole('button', { name: 'Diff', pressed: true })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Full text' }));
    expect(screen.getByRole('button', { name: 'Full text', pressed: true })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Diff', pressed: false })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Edit suggestion manually' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Edited suggestion text' }), {
      target: { value: 'Manual revision' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Diff' }));
    expect(screen.getByRole('textbox', { name: 'Edited suggestion text' })).toHaveValue('Manual revision');
  });

  it('never relabels a previous answer as an editable revision when a later revision request fails', async () => {
    service.suggest
      .mockResolvedValueOnce('Read-only answer')
      .mockRejectedValueOnce(new Error('fixture revision failure'));
    await renderEditor(<ArtifactEditRefinementPanel {...props} onClose={vi.fn()} onApplied={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Ask' }));
    await screen.findByText('Answer');
    fireEvent.click(screen.getByRole('button', { name: /Generate revision|Regenerate/ }));
    await screen.findByRole('alert');
    expect(screen.getByText('Answer')).toBeInTheDocument();
    expect(screen.getByText('Read-only answer')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Apply as new version' })).not.toBeInTheDocument();
    expect(service.apply).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Generate revision' })).toBeInTheDocument();
  });

  it('retains the successful revision and manual draft when a later question fails', async () => {
    service.suggest
      .mockResolvedValueOnce('Revised passage')
      .mockRejectedValueOnce(new Error('fixture question failure'));
    await renderEditor(<ArtifactEditRefinementPanel {...props} onClose={vi.fn()} onApplied={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Generate revision' }));
    await screen.findByText('Revision suggestion');
    fireEvent.click(screen.getByRole('button', { name: 'Edit suggestion manually' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Edited suggestion text' }), {
      target: { value: 'Kept manual revision' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Ask' }));
    await screen.findByRole('alert');
    expect(screen.getByText('Revision suggestion')).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Edited suggestion text' })).toHaveValue('Kept manual revision');
    expect(screen.getByRole('button', { name: 'Apply as new version' })).toBeEnabled();
  });
});
