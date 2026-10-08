import ArchiveArtifactPreview from '@/renderer/pages/artifact/ArchiveArtifactPreview';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import React from 'react';
import { I18nextProvider } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createTestI18n } from '../i18nTestUtils';

vi.mock('@/common', () => ({ ipcBridge: { fs: {} } }));
vi.mock('@/renderer/utils/emitter', () => ({ emitter: { on: vi.fn(), off: vi.fn() } }));
vi.mock('@/renderer/pages/conversation/Preview/components/editors/CodeEditor', () => ({
  default: ({ value }: { value: string }) => <pre>{value}</pre>,
}));

const listing = {
  filename: 'package.zip',
  containers: [],
  entries: [{ path: 'notes.md', name: 'notes.md', size: 42, directory: false, archive: false }],
};
beforeEach(() => {
  localStorage.clear();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) =>
      String(input).includes('/archive/content')
        ? new Response('# Archive content', { status: 200 })
        : Response.json(listing)
    )
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

async function view() {
  const i18n = await createTestI18n('en-US');
  const node = (contentUrl: string, filename = 'package.zip') => (
    <I18nextProvider i18n={i18n}>
      <ArchiveArtifactPreview filename={filename} contentUrl={contentUrl} />
    </I18nextProvider>
  );
  const rendered = render(node('/api/artifacts/package/versions/v1'));
  return { ...rendered, change: (uri: string, name?: string) => rendered.rerender(node(uri, name)) };
}
async function openNotes() {
  fireEvent.click(await screen.findByRole('button', { name: /notes\.md.*42 B/ }));
  return screen.findByTestId('preview-board');
}

describe('standalone archive preview', () => {
  it('opens native readonly entries beside its still-available archive directory', async () => {
    await view();
    const board = await openNotes();
    expect(await within(board).findByText('Archive content')).toBeInTheDocument();
    expect(screen.getByTestId('archive-viewer')).toBeInTheDocument();
    expect(within(board).queryByRole('button', { name: 'Edit file' })).not.toBeInTheDocument();
    expect(within(board).queryByRole('button', { name: 'Add to message' })).not.toBeInTheDocument();
    expect(within(board).queryByRole('button', { name: /region comment/i })).not.toBeInTheDocument();
  });

  it('closes only the embedded entries and retains the directory', async () => {
    await view();
    const board = await openNotes();
    fireEvent.click(within(board).getByRole('button', { name: 'Close file board' }));
    expect(screen.queryByTestId('preview-board')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /notes\.md.*42 B/ })).toBeInTheDocument();
  });

  it('does not persist entry tabs or overwrite the workspace board layout', async () => {
    await view();
    const set = vi.spyOn(Storage.prototype, 'setItem');
    const remove = vi.spyOn(Storage.prototype, 'removeItem');
    const board = await openNotes();
    fireEvent.click(within(board).getByRole('button', { name: 'Up to two columns' }));
    await waitFor(() => expect(board).toHaveAttribute('data-columns', '2'));
    expect(set).not.toHaveBeenCalled();
    expect(remove).not.toHaveBeenCalled();
  });

  it('disposes entries when the viewed source changes but retains them on a rename', async () => {
    const current = await view();
    await openNotes();
    current.change('/api/artifacts/package/versions/v1', 'renamed.zip');
    expect(screen.getByTestId('preview-board')).toBeInTheDocument();
    current.change('/api/artifacts/package/versions/v2');
    expect(screen.queryByTestId('preview-board')).not.toBeInTheDocument();
    await screen.findByRole('button', { name: /notes\.md.*42 B/ });
  });

  it('cannot open a late entry from a disposed version in the replacement board', async () => {
    let finish!: (value: Response) => void;
    const pending = new Promise<Response>((resolve) => {
      finish = resolve;
    });
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL) =>
        String(input).includes('/archive/content') ? pending : Promise.resolve(Response.json(listing))
      )
    );
    const current = await view();
    fireEvent.click(await screen.findByRole('button', { name: /notes\.md.*42 B/ }));
    current.change('/api/artifacts/package/versions/v2');
    await act(async () => finish(new Response('# Obsolete', { status: 200 })));
    expect(screen.queryByTestId('preview-board')).not.toBeInTheDocument();
  });
});
