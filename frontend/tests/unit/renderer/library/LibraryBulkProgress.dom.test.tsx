import React from 'react';
import { cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { LibraryBulkProgress } from '@/renderer/pages/library/LibraryBulkProgress';
import type { LiteratureMutationView } from '@/renderer/services/runtime/LiteratureMutationController';
import {
  createLiteratureReadingBulk,
  decodeLiteratureReadingBulk,
  claimLiteratureReadingBulk,
} from '@/renderer/services/runtime/literatureReadingBulkIntent';
import { renderWithI18n } from '../../i18nTestUtils';

let sequence = 0;
const ready = createLiteratureReadingBulk(
  ['a', 'b'],
  'read',
  'original-scope',
  () => `00000000-0000-4000-8000-${String(++sequence).padStart(12, '0')}`
);
// Synthetic display snapshots: these tests do not constitute execution receipts.
const completed = decodeLiteratureReadingBulk({ ...ready, confirmed_batches: ready.batches.length });
const sending = claimLiteratureReadingBulk(ready);
const view = (patch: Partial<LiteratureMutationView>): LiteratureMutationView => ({
  pending: null,
  mutating: false,
  error: null,
  uploadState: null,
  canDiscardRejectedUpload: false,
  ...patch,
});
type Props = React.ComponentProps<typeof LibraryBulkProgress>;
const props = (state: LiteratureMutationView, message?: Props['message']): Props => ({
  view: state,
  scopeKey: ready.scope_key,
  message,
  resume: vi.fn(() => Promise.resolve()),
  pause: vi.fn(() => Promise.resolve()),
  discard: vi.fn(() => Promise.resolve()),
});

describe('Library bulk progress describes the current attempt and exact active owner', () => {
  afterEach(() => cleanup());

  it.each([
    ['resolveFailed', completed, 'The complete selection could not be loaded. No batch was started.'],
    ['emptyResolved', ready, 'No matching references remain in this selection.'],
  ] as const)('shows %s instead of a retained historical terminal action', async (message, history, text) => {
    await renderWithI18n(<LibraryBulkProgress {...props(view({ action: history }), message)} />, 'en-US');
    expect(screen.getByText(text)).toBeVisible();
    expect(screen.queryByText('2 references saved.')).toBeNull();
    expect(screen.queryByText(/^Stopped after/)).toBeNull();
  });

  it('keeps an actual pending request and its recovery controls visible beside the current action error', async () => {
    const input = props(view({ pending: sending, action: sending, error: 'uncertain' }), 'operationFailed');
    await renderWithI18n(<LibraryBulkProgress {...input} />, 'en-US');
    expect(
      screen.getByText('The batch action could not be completed. Your original work and selection are kept.')
    ).toHaveAttribute('role', 'alert');
    expect(screen.getByText('0 of 2 confirmed. The current batch needs confirmation.')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Resume batch' }));
    expect(input.resume).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('button', { name: 'Stop after current batch' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'Discard remaining work' })).toBeNull();
  });

  it('shows preparation before durable admission instead of claiming that work was discarded', async () => {
    await renderWithI18n(<LibraryBulkProgress {...props(view({ action: ready, mutating: true }))} />, 'en-US');
    expect(screen.getByText('Preparing 2 references…')).toBeVisible();
    expect(screen.getByRole('region', { name: 'Reading status for selection' })).toHaveAttribute('aria-busy', 'true');
    expect(screen.queryByText(/^Stopped after/)).toBeNull();
    expect(screen.queryByRole('button', { name: 'Discard remaining work' })).toBeNull();
  });

  it('does not label another action as executing the pending bulk or offer a no-op recovery click', async () => {
    await renderWithI18n(
      <LibraryBulkProgress {...props(view({ pending: sending, action: undefined, mutating: true }))} />,
      'en-US'
    );
    expect(screen.getByText('0 of 2 confirmed. The current batch needs confirmation.')).toBeVisible();
    expect(screen.queryByText(/^Saving /)).toBeNull();
    expect(screen.getByRole('button', { name: 'Resume batch' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Stop after current batch' })).toBeDisabled();
  });

  it('retains storage recovery when an attempt message exists without a current pending plan', async () => {
    const input = props(view({ action: completed, error: 'storage' }), 'operationFailed');
    await renderWithI18n(<LibraryBulkProgress {...input} />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(input.resume).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('2 references saved.')).toBeNull();
  });

  it('still displays the actual retained completion when no newer attempt supersedes it', async () => {
    await renderWithI18n(<LibraryBulkProgress {...props(view({ action: completed }))} />, 'en-US');
    expect(screen.getByText('2 references saved.')).toBeVisible();
  });
});
