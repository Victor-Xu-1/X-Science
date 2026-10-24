import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SkillDetailModal } from '@/renderer/pages/settings/skills/XScienceSkillLibraryModals';
import { renderWithSettingsI18n } from './settingsI18nTestUtils';

const mocks = vi.hoisted(() => ({
  files: vi.fn(),
  content: vi.fn(),
  snapshot: vi.fn(),
  save: vi.fn(),
  publish: vi.fn(),
}));

vi.mock('@arco-design/web-react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@arco-design/web-react')>();
  return {
    ...actual,
    Message: { useMessage: () => [{ success: vi.fn(), warning: vi.fn(), error: vi.fn() }, null] },
  };
});
vi.mock('@/renderer/services/skills/xScienceSkillLibrary', () => ({
  loadXScienceSkillFiles: mocks.files,
  loadXScienceSkillFileContent: mocks.content,
  loadXScienceSkillFileSnapshot: mocks.snapshot,
  saveXScienceSkillDraftFile: mocks.save,
  publishXScienceSkillDraft: mocks.publish,
  deleteXScienceSkillDraft: vi.fn(),
  duplicateXScienceSkill: vi.fn(),
  importXScienceSkillFile: vi.fn(),
  importXScienceRepositorySkills: vi.fn(),
  previewXScienceSkillRepository: vi.fn(),
}));

const revisionA = 'a'.repeat(64);
const revisionB = 'b'.repeat(64);
const original = '# Reviewed workflow';
const skill = {
  name: 'reviewed-workflow',
  displayName: 'Reviewed workflow',
  description: '',
  source: 'personal-draft',
};
const snapshot = (content = original, draftRevision: string | null = revisionA) => ({
  content,
  origin: draftRevision ? 'draft' : 'personal',
  draftRevision,
  files: ['SKILL.md'],
});

beforeEach(() => {
  vi.resetAllMocks();
  mocks.files.mockResolvedValue(['SKILL.md']);
  mocks.content.mockResolvedValue(original);
  mocks.snapshot.mockResolvedValue(snapshot());
  mocks.save.mockResolvedValue({});
  mocks.publish.mockResolvedValue({});
});

describe('reviewed Skill draft publication', () => {
  it('uses one snapshot inventory and revision when the package changes between list and content', async () => {
    mocks.files.mockResolvedValue(['SKILL.md', 'references/stale.md']);
    mocks.snapshot.mockResolvedValue({
      ...snapshot(original, revisionB),
      files: ['SKILL.md', 'references/current.md'],
    });
    await renderDraft();
    await editor();
    fireEvent.click(screen.getByRole('combobox', { name: 'Skill 文件' }));
    expect(screen.queryByText('references/stale.md', { exact: true })).not.toBeInTheDocument();
    fireEvent.click(await screen.findByText('references/current.md', { exact: true }));
    await waitFor(() =>
      expect(mocks.snapshot).toHaveBeenLastCalledWith(skill.name, 'references/current.md', { source: 'draft' })
    );
    await waitFor(() => expect(screen.getByRole('button', { name: '发布' })).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: '发布' }));
    await waitFor(() =>
      expect(mocks.publish).toHaveBeenCalledWith(skill.name, false, { expectedDraftRevision: revisionB })
    );
    expect(mocks.files).toHaveBeenCalledTimes(1);
  });

  it('uses the displayed draft identity and never reads again inside publish', async () => {
    const changed = vi.fn();
    await renderDraft({ onChanged: changed });
    await editor();
    fireEvent.click(screen.getByRole('button', { name: '发布' }));
    await waitFor(() =>
      expect(mocks.publish).toHaveBeenCalledWith(skill.name, false, { expectedDraftRevision: revisionA })
    );
    expect(mocks.snapshot).toHaveBeenCalledTimes(1);
    expect(mocks.snapshot).toHaveBeenCalledWith(skill.name, 'SKILL.md', { source: 'draft' });
    await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
  });

  it('does not authorize publication from published content or an absent draft token', async () => {
    mocks.snapshot.mockResolvedValue(snapshot(original, null));
    await renderDraft();
    await editor();
    expect(screen.getByRole('button', { name: '发布' })).toBeDisabled();
    expect(screen.getByRole('alert')).toHaveTextContent('重新载入');
    expect(mocks.publish).not.toHaveBeenCalled();
  });

  it('does not publish unsaved editor bytes under an older loaded identity', async () => {
    await renderDraft();
    const field = await editor();
    fireEvent.change(field, { target: { value: '# Unsaved local edit' } });
    expect(screen.getByRole('button', { name: '发布' })).toBeDisabled();
    expect(mocks.publish).not.toHaveBeenCalled();
  });

  it.each([
    ['zh-CN', 409, 'draft_revision_conflict'],
    ['en-US', 428, 'draft_revision_required'],
  ] as const)('requires an explicit source reload after %s publication conflict', async (language, status, code) => {
    mocks.publish.mockRejectedValueOnce(Object.assign(new Error('private server detail'), { status, code }));
    const changed = vi.fn();
    await renderDraft({ language, onChanged: changed });
    await editor();
    const publishName = language === 'zh-CN' ? '发布' : 'Publish';
    fireEvent.click(screen.getByRole('button', { name: publishName }));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(language === 'zh-CN' ? '重新载入' : 'Reload');
    expect(alert).not.toHaveTextContent('private server detail');
    expect(mocks.snapshot).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('button', { name: publishName })).toBeDisabled();
    expect(changed).not.toHaveBeenCalled();

    mocks.snapshot.mockResolvedValueOnce(snapshot('# Newly reviewed workflow', revisionB));
    fireEvent.click(screen.getByRole('button', { name: language === 'zh-CN' ? '重新加载' : 'Reload' }));
    await screen.findByDisplayValue('# Newly reviewed workflow');
    expect(mocks.publish).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: publishName }));
    await waitFor(() =>
      expect(mocks.publish).toHaveBeenLastCalledWith(skill.name, false, { expectedDraftRevision: revisionB })
    );
  });

  it('shows the saved snapshot before enabling a subsequent explicit publish', async () => {
    const readback = deferred<ReturnType<typeof snapshot>>();
    mocks.snapshot.mockResolvedValueOnce(snapshot()).mockReturnValueOnce(readback.promise);
    await renderDraft();
    const field = await editor();
    fireEvent.change(field, { target: { value: '# Submitted edit' } });
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    await waitFor(() => expect(mocks.snapshot).toHaveBeenCalledTimes(2));
    expect(screen.getByRole('button', { name: '发布' })).toBeDisabled();
    await act(async () => readback.resolve(snapshot('# Stored and reviewed edit', revisionB)));
    await screen.findByDisplayValue('# Stored and reviewed edit');
    await waitFor(() => expect(screen.getByRole('button', { name: '发布' })).toBeEnabled());
    expect(mocks.publish).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: '发布' }));
    await waitFor(() =>
      expect(mocks.publish).toHaveBeenCalledWith(skill.name, false, { expectedDraftRevision: revisionB })
    );
  });

  it('keeps publish unavailable after failed saved-draft readback and retries only that read', async () => {
    mocks.snapshot
      .mockResolvedValueOnce(snapshot())
      .mockRejectedValueOnce(new Error('readback unavailable'))
      .mockResolvedValueOnce(snapshot('# Recovered stored draft', revisionB));
    await renderDraft();
    await editor();
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('无法读取 Skill 文件');
    expect(screen.getByRole('button', { name: '发布' })).toBeDisabled();
    expect(mocks.save).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    await screen.findByDisplayValue('# Recovered stored draft');
    await waitFor(() => expect(screen.getByRole('button', { name: '发布' })).toBeEnabled());
    expect(mocks.save).toHaveBeenCalledTimes(1);
    expect(mocks.publish).not.toHaveBeenCalled();
  });

  it('does not restore an old read identity after the same detail is closed and reopened', async () => {
    const oldRead = deferred<ReturnType<typeof snapshot>>();
    mocks.snapshot
      .mockResolvedValueOnce(snapshot())
      .mockReturnValueOnce(oldRead.promise)
      .mockResolvedValueOnce(snapshot(original, null));
    const changed = vi.fn();
    const view = await renderDraft({ onChanged: changed });
    await editor();
    fireEvent.click(screen.getByRole('button', { name: '重新加载' }));
    await waitFor(() => expect(mocks.snapshot).toHaveBeenCalledTimes(2));
    view.rerender(
      <SkillDetailModal
        visible={false}
        skill={null}
        draft={false}
        editable={false}
        onClose={vi.fn()}
        onChanged={changed}
      />
    );
    view.rerender(<SkillDetailModal visible skill={skill} draft editable onClose={vi.fn()} onChanged={changed} />);
    await editor();
    await act(async () => oldRead.resolve(snapshot('# Old opening source', revisionB)));
    expect(screen.getByRole('textbox', { name: 'Skill 文件内容' })).toHaveValue(original);
    expect(screen.getByRole('button', { name: '发布' })).toBeDisabled();
    expect(mocks.publish).not.toHaveBeenCalled();
  });
});

async function editor() {
  const field = await screen.findByRole('textbox', { name: /Skill 文件内容|Skill file content/ });
  await waitFor(() => expect(field).toHaveValue(original));
  return field;
}

async function renderDraft({
  language = 'zh-CN',
  onChanged = vi.fn(),
}: { language?: 'zh-CN' | 'en-US'; onChanged?: () => void | Promise<void> } = {}) {
  let view!: Awaited<ReturnType<typeof renderWithSettingsI18n>>;
  await act(async () => {
    view = await renderWithSettingsI18n(
      <SkillDetailModal visible skill={skill} draft editable onClose={vi.fn()} onChanged={onChanged} />,
      language
    );
  });
  return view;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
