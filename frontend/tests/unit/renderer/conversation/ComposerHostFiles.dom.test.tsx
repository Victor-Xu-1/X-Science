import FileAttachButton from '@/renderer/components/media/FileAttachButton';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../../i18nTestUtils';

vi.mock('@/renderer/utils/platform', () => ({ isElectronDesktop: () => false }));

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function fixtureResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

describe('composer granted folder snapshots', () => {
  it('selects an owned granted file without granting access or changing the workspace', async () => {
    const onLocalFilesAdded = vi.fn();
    const onSelectProjectFiles = vi.fn();
    const fetchMock = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost');
      if (url.pathname === '/api/preferences/host-grants') {
        return fixtureResponse({
          grants: [
            {
              id: '/fixture/grant',
              hostPath: '/fixture/grant',
              mountName: 'Fixture',
              guestPath: '/fixture/grant',
              mode: 'ro',
            },
          ],
        });
      }
      if (url.pathname === '/api/preferences/host-browse')
        return fixtureResponse([{ name: 'sample + 测量.csv', isDirectory: false }]);
      throw new Error(`unexpected fixture route: ${url.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    await renderWithI18n(
      <FileAttachButton
        openFileSelector={vi.fn()}
        onSelectProjectFiles={onSelectProjectFiles}
        onLocalFilesAdded={onLocalFilesAdded}
      />,
      'en-US',
      { wrapper: MemoryRouter }
    );
    fireEvent.click(screen.getByRole('button', { name: 'Add to message' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Your files' }));
    await waitFor(() => expect(screen.getByRole('menu', { name: 'Your files' })).toBeVisible());
    expect(screen.queryByRole('dialog', { name: 'Your files' })).not.toBeInTheDocument();
    fireEvent.click(await screen.findByRole('button', { name: 'Expand Fixture' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Reference sample + 测量.csv' }));
    expect(onLocalFilesAdded).toHaveBeenCalledTimes(1);
    const files = onLocalFilesAdded.mock.calls[0][0] as Array<{ path: string; name: string }>;
    expect(files).toHaveLength(1);
    const reference = new URL(files[0].path);
    expect(reference.protocol).toBe('x-science-host-grant:');
    expect(reference.host).toBe('file');
    expect(reference.searchParams.get('grant')).toBe('/fixture/grant');
    expect(reference.searchParams.get('path')).toBe('sample + 测量.csv');
    expect(files[0].name).toBe('sample + 测量.csv');
    expect(onSelectProjectFiles).not.toHaveBeenCalled();
    expect(fetchMock.mock.calls.every(([, init]) => !init || !('method' in init) || init.method === 'GET')).toBe(true);
  });

  it('keeps a recoverable grants error distinct from an empty directory', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(fixtureResponse({ message: 'unavailable' }, 503))
      .mockResolvedValueOnce(fixtureResponse({ grants: [] }));
    vi.stubGlobal('fetch', fetchMock);
    await renderWithI18n(<FileAttachButton openFileSelector={vi.fn()} onLocalFilesAdded={vi.fn()} />, 'en-US', {
      wrapper: MemoryRouter,
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add to message' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Your files' }));
    expect(await screen.findByText('Could not load granted folders.')).toBeVisible();
    expect(screen.queryByText('No granted folders.')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('No granted folders.')).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('keeps normal peers usable when a listed name cannot be represented by a file reference', async () => {
    const onLocalFilesAdded = vi.fn();
    const unsupportedFile = 'unsupported\\name.txt';
    const unsupportedFolder = 'unsupported\nfolder';
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), 'http://localhost');
      if (url.pathname === '/api/preferences/host-grants') {
        return fixtureResponse({
          grants: [
            {
              id: '/fixture/grant',
              hostPath: '/fixture/grant',
              mountName: 'Fixture',
              guestPath: '/fixture/grant',
              mode: 'ro',
            },
          ],
        });
      }
      if (url.pathname === '/api/preferences/host-browse')
        return fixtureResponse([
          { name: unsupportedFile, isDirectory: false },
          { name: unsupportedFolder, isDirectory: true },
          { name: 'normal.csv', isDirectory: false },
        ]);
      throw new Error(`unexpected fixture route: ${url.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    await renderWithI18n(
      <FileAttachButton openFileSelector={vi.fn()} onLocalFilesAdded={onLocalFilesAdded} />,
      'en-US',
      { wrapper: MemoryRouter }
    );
    fireEvent.click(screen.getByRole('button', { name: 'Add to message' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Your files' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Expand Fixture' }));
    const normal = await screen.findByRole('button', { name: 'Reference normal.csv' });
    const unsupportedFileButton = screen.getByRole('button', { name: `Reference ${unsupportedFile}` });
    const unsupportedFolderButton = screen
      .getByText((_content, element) => element?.tagName === 'SPAN' && element.textContent === unsupportedFolder)
      .closest('button');
    expect(unsupportedFileButton).toBeDisabled();
    expect(unsupportedFolderButton).toBeDisabled();
    expect(screen.getAllByText('This name cannot be opened or referenced here.')).toHaveLength(2);
    fireEvent.click(unsupportedFileButton);
    fireEvent.click(unsupportedFolderButton!);
    expect(onLocalFilesAdded).not.toHaveBeenCalled();
    expect(fetchMock).toHaveBeenCalledTimes(2);
    fireEvent.click(normal);
    expect(onLocalFilesAdded).toHaveBeenCalledTimes(1);
    expect(new URL(onLocalFilesAdded.mock.calls[0][0][0].path).searchParams.get('path')).toBe('normal.csv');
  });
});
