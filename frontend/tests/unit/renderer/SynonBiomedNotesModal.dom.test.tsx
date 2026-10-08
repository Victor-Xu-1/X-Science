import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import SynonBiomedNotesModal from '@/renderer/components/synonBiomed/notes/SynonBiomedNotesModal';
import { renderWithI18n } from '../i18nTestUtils';

const renderNotes = async (ui: React.ReactElement, language: 'zh-CN' | 'en-US' = 'zh-CN') => {
  let view!: Awaited<ReturnType<typeof renderWithI18n>>;
  await act(async () => {
    view = await renderWithI18n(ui, language);
  });
  return view;
};

const notes = vi.hoisted(() => ({
  load: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
}));

vi.mock('@/renderer/components/Markdown', () => ({
  default: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));

vi.mock('@/renderer/services/synonBiomedNotes', () => ({
  loadSynonBiomedNotes: notes.load,
  createSynonBiomedNote: notes.create,
  updateSynonBiomedNote: notes.update,
  deleteSynonBiomedNote: notes.remove,
}));

const target = {
  projectId: 'project-1',
  targetType: 'artifact' as const,
  targetFrameId: 'frame-1',
  targetArtifactId: 'artifact-1',
};

const savedNote = {
  id: 'note-1',
  projectId: 'project-1',
  targetType: 'artifact' as const,
  targetFrameId: 'frame-1',
  targetMessageIndex: null,
  targetArtifactId: 'artifact-1',
  content: 'Review the assay controls.',
  createdAt: '2026-07-15T08:00:00Z',
  updatedAt: '2026-07-15T08:00:00Z',
  targetName: null,
  messagePreview: null,
};

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((complete, fail) => {
    resolve = complete;
    reject = fail;
  });
  return { promise, resolve, reject };
};

describe('SynonBiomedNotesModal', () => {
  beforeEach(() => {
    for (const mock of Object.values(notes)) mock.mockReset();
    notes.load.mockResolvedValue([]);
    notes.create.mockResolvedValue(savedNote);
    notes.update.mockResolvedValue(savedNote);
    notes.remove.mockResolvedValue(undefined);
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('creates a note through the localized Chinese workflow', async () => {
    await renderNotes(<SynonBiomedNotesModal visible target={target} onClose={vi.fn()} />);

    fireEvent.change(await screen.findByRole('textbox', { name: '笔记内容' }), {
      target: { value: savedNote.content },
    });
    fireEvent.click(screen.getByRole('button', { name: '添加笔记' }));

    await waitFor(() => expect(notes.create).toHaveBeenCalledWith(target, savedNote.content));
    expect(await screen.findByText(savedNote.content)).toBeInTheDocument();
  });

  it('renders the empty notes workflow in English', async () => {
    await renderNotes(<SynonBiomedNotesModal visible target={target} onClose={vi.fn()} />, 'en-US');

    expect(await screen.findByText('Notes')).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Note content' })).toHaveAttribute(
      'placeholder',
      'Add a note linked to this content'
    );
    expect(await screen.findByText('No notes')).toBeInTheDocument();
  });

  it('keeps a draft and does not reload when its owner passes an equivalent target object', async () => {
    const onClose = vi.fn();
    const view = await renderNotes(<SynonBiomedNotesModal visible target={target} onClose={onClose} />);
    await screen.findByText('暂无笔记');
    fireEvent.change(screen.getByRole('textbox', { name: '笔记内容' }), { target: { value: 'Unsaved review' } });

    view.rerender(<SynonBiomedNotesModal visible target={{ ...target }} onClose={onClose} />);
    expect(screen.getByRole('textbox', { name: '笔记内容' })).toHaveValue('Unsaved review');
    expect(notes.load).toHaveBeenCalledTimes(1);
  });

  it('keeps an unsaved draft when the current language changes', async () => {
    const view = await renderNotes(<SynonBiomedNotesModal visible target={target} onClose={vi.fn()} />);
    await screen.findByText('暂无笔记');
    fireEvent.change(screen.getByRole('textbox', { name: '笔记内容' }), { target: { value: 'Unsaved review' } });
    await act(async () => {
      await view.i18n.changeLanguage('en-US');
    });

    expect(screen.getByRole('textbox', { name: 'Note content' })).toHaveValue('Unsaved review');
    expect(notes.load).toHaveBeenCalledTimes(1);
  });

  it('does not let a save from a closed session clear a newly opened draft', async () => {
    const pending = deferred<typeof savedNote>();
    notes.create.mockReturnValueOnce(pending.promise);
    const onClose = vi.fn();
    const view = await renderNotes(<SynonBiomedNotesModal visible target={target} onClose={onClose} />);
    await screen.findByText('暂无笔记');
    fireEvent.change(screen.getByRole('textbox', { name: '笔记内容' }), { target: { value: savedNote.content } });
    fireEvent.click(screen.getByRole('button', { name: '添加笔记' }));
    expect(notes.create).toHaveBeenCalledWith(target, savedNote.content);

    view.rerender(<SynonBiomedNotesModal visible={false} target={target} onClose={onClose} />);
    view.rerender(<SynonBiomedNotesModal visible target={target} onClose={onClose} />);
    await screen.findByText('暂无笔记');
    fireEvent.change(screen.getByRole('textbox', { name: '笔记内容' }), { target: { value: 'Next draft' } });
    await act(async () => {
      pending.resolve(savedNote);
      await pending.promise;
    });

    expect(screen.getByRole('textbox', { name: '笔记内容' })).toHaveValue('Next draft');
    expect(screen.queryByText(savedNote.content)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '添加笔记' })).toBeEnabled();
  });

  it('does not display the previous file or clear the new file draft when an old save arrives', async () => {
    const pending = deferred<typeof savedNote>();
    notes.load.mockResolvedValueOnce([savedNote]).mockResolvedValueOnce([]);
    notes.update.mockReturnValueOnce(pending.promise);
    const onClose = vi.fn();
    const view = await renderNotes(<SynonBiomedNotesModal visible target={target} onClose={onClose} />);
    await screen.findByText(savedNote.content);
    fireEvent.click(screen.getByRole('button', { name: '编辑笔记' }));
    fireEvent.change(screen.getByRole('textbox', { name: '笔记内容' }), { target: { value: 'Edited first file' } });
    fireEvent.click(screen.getByRole('button', { name: '保存修改' }));
    expect(notes.update).toHaveBeenCalledWith(savedNote.id, 'Edited first file');

    const nextTarget = { ...target, targetArtifactId: 'artifact-2' };
    view.rerender(<SynonBiomedNotesModal visible target={nextTarget} onClose={onClose} />);
    await screen.findByText('暂无笔记');
    fireEvent.change(screen.getByRole('textbox', { name: '笔记内容' }), { target: { value: 'Second file draft' } });
    await act(async () => {
      pending.resolve({ ...savedNote, content: 'Edited first file' });
      await pending.promise;
    });

    expect(screen.getByRole('textbox', { name: '笔记内容' })).toHaveValue('Second file draft');
    expect(screen.queryByText('Edited first file')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '添加笔记' })).toBeEnabled();
    expect(notes.load).toHaveBeenLastCalledWith(nextTarget);
  });

  it('locks the submitted editor until the write settles and retains a failed draft with a durable error', async () => {
    const pending = deferred<typeof savedNote>();
    notes.create.mockReturnValueOnce(pending.promise);
    await renderNotes(<SynonBiomedNotesModal visible target={target} onClose={vi.fn()} />);
    await screen.findByText('暂无笔记');
    const editor = screen.getByRole('textbox', { name: '笔记内容' });
    fireEvent.change(editor, { target: { value: 'Retry this draft' } });
    const add = screen.getByRole('button', { name: '添加笔记' });
    fireEvent.click(add);
    fireEvent.click(add);
    expect(notes.create).toHaveBeenCalledTimes(1);
    expect(editor).toBeDisabled();
    await act(async () => {
      pending.reject(new Error('fixture save failure'));
      await pending.promise.catch(() => {});
    });

    expect(editor).toBeEnabled();
    expect(editor).toHaveValue('Retry this draft');
    expect(screen.getByRole('alert')).toHaveTextContent('笔记保存失败');
    fireEvent.click(add);
    await waitFor(() => expect(notes.create).toHaveBeenCalledTimes(2));
    expect(await screen.findByText(savedNote.content)).toBeInTheDocument();
  });

  it('moves focus to the existing editor when a note is selected for editing', async () => {
    notes.load.mockResolvedValueOnce([savedNote]);
    await renderNotes(<SynonBiomedNotesModal visible target={target} onClose={vi.fn()} />);
    await screen.findByText(savedNote.content);
    const edit = screen.getByRole('button', { name: '编辑笔记' });
    act(() => edit.focus());
    fireEvent.click(edit);

    expect(screen.getByRole('textbox', { name: '笔记内容' })).toHaveValue(savedNote.content);
    expect(screen.getByRole('textbox', { name: '笔记内容' })).toHaveFocus();
  });

  it('isolates a pending delete from a later visible session and prevents duplicate writes', async () => {
    const pending = deferred<void>();
    notes.load.mockResolvedValue([savedNote]);
    notes.remove.mockReturnValueOnce(pending.promise);
    const onClose = vi.fn();
    const view = await renderNotes(<SynonBiomedNotesModal visible target={target} onClose={onClose} />);
    fireEvent.click(screen.getByRole('button', { name: '删除笔记' }));
    fireEvent.click(await screen.findByRole('button', { name: '确定' }));
    expect(notes.remove).toHaveBeenCalledWith(savedNote.id);
    expect(screen.getByRole('textbox', { name: '笔记内容' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '编辑笔记' })).toBeDisabled();

    view.rerender(<SynonBiomedNotesModal visible={false} target={target} onClose={onClose} />);
    view.rerender(<SynonBiomedNotesModal visible target={target} onClose={onClose} />);
    await screen.findByText(savedNote.content);
    fireEvent.click(screen.getByRole('button', { name: '编辑笔记' }));
    fireEvent.change(screen.getByRole('textbox', { name: '笔记内容' }), { target: { value: 'New edit session' } });
    await act(async () => {
      pending.resolve();
      await pending.promise;
    });

    expect(screen.getByRole('textbox', { name: '笔记内容' })).toHaveValue('New edit session');
    expect(screen.getByRole('button', { name: '保存修改' })).toBeEnabled();
    expect(screen.getByText(savedNote.content)).toBeInTheDocument();
    expect(notes.remove).toHaveBeenCalledTimes(1);
  });

  it('rejects late reads after an owner change and retries a current read error without dropping the draft', async () => {
    const pending = deferred<Array<typeof savedNote>>();
    notes.load
      .mockReturnValueOnce(pending.promise)
      .mockRejectedValueOnce(new Error('fixture read failure'))
      .mockResolvedValueOnce([]);
    const onClose = vi.fn();
    const view = await renderNotes(<SynonBiomedNotesModal visible target={target} onClose={onClose} />);
    const nextTarget = { ...target, targetArtifactId: 'artifact-2' };
    view.rerender(<SynonBiomedNotesModal visible target={nextTarget} onClose={onClose} />);
    const editor = screen.getByRole('textbox', { name: '笔记内容' });
    fireEvent.change(editor, { target: { value: 'Kept while retrying' } });
    await screen.findByRole('alert');
    await act(async () => {
      pending.resolve([savedNote]);
      await pending.promise;
    });
    expect(screen.queryByText(savedNote.content)).not.toBeInTheDocument();
    expect(editor).toHaveValue('Kept while retrying');
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    await screen.findByText('暂无笔记');
    expect(notes.load).toHaveBeenLastCalledWith(nextTarget);
    expect(editor).toHaveValue('Kept while retrying');
  });

  it('does not hide a successful new note behind an earlier read error or claim that the list is empty', async () => {
    notes.load.mockRejectedValueOnce(new Error('fixture initial read failure'));
    await renderNotes(<SynonBiomedNotesModal visible target={target} onClose={vi.fn()} />);
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(screen.queryByText('暂无笔记')).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole('textbox', { name: '笔记内容' }), { target: { value: savedNote.content } });
    fireEvent.click(screen.getByRole('button', { name: '添加笔记' }));
    expect(await screen.findByText(savedNote.content)).toBeInTheDocument();
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(notes.create).toHaveBeenCalledWith(target, savedNote.content);
    expect(notes.load).toHaveBeenCalledTimes(1);
  });
});
