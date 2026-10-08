import { ConfigProvider } from '@arco-design/web-react';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SkillDetailModal, type SkillModalItem } from '@/renderer/pages/settings/skills/SynonBiomedSkillLibraryModals';
import { renderWithSettingsI18n } from './settingsI18nTestUtils';

const mocks = vi.hoisted(() => ({
  loadFiles: vi.fn(),
  loadContent: vi.fn(),
  saveFile: vi.fn(),
  duplicate: vi.fn(),
  publish: vi.fn(),
  deleteDraft: vi.fn(),
}));

vi.mock('@arco-design/web-react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@arco-design/web-react')>();
  return {
    ...actual,
    Message: {
      useMessage: () => [{ success: vi.fn(), warning: vi.fn(), error: vi.fn() }, null],
    },
  };
});

vi.mock('@/renderer/components/Markdown', () => ({
  default: ({ children }: { children: React.ReactNode }) => <div data-testid='skill-markdown'>{children}</div>,
}));

vi.mock('@/renderer/services/skills/synonBiomedSkillLibrary', () => ({
  loadSynonBiomedSkillFiles: mocks.loadFiles,
  loadSynonBiomedSkillFileContent: mocks.loadContent,
  saveSynonBiomedSkillDraftFile: mocks.saveFile,
  duplicateSynonBiomedSkill: mocks.duplicate,
  publishSynonBiomedSkillDraft: mocks.publish,
  deleteSynonBiomedSkillDraft: mocks.deleteDraft,
  importSynonBiomedSkillFile: vi.fn(),
  importSynonBiomedRepositorySkills: vi.fn(),
  previewSynonBiomedSkillRepository: vi.fn(),
}));

const bundledSkill = {
  name: 'alphafold2',
  displayName: 'AlphaFold2',
  description: 'Predict protein structures with ColabFold.',
  source: 'synon_llm',
  category: 'biomodels',
  license: 'Apache-2.0',
  attachedAgents: ['OPERON'],
  thirdParty: [
    {
      kind: 'weights',
      name: 'AlphaFold2',
      provider: 'Google DeepMind',
      license: 'CC-BY-4.0',
      termsUrl: 'https://example.test/terms',
    },
  ],
};

describe('X-Science skill library detail modal', () => {
  it('retains read-only detail content during exit rather than flashing a missing-file error', async () => {
    function ClosingFixture() {
      const [open, setOpen] = React.useState(true);
      const [selected, setSelected] = React.useState<SkillModalItem | null>(bundledSkill);
      return (
        <SkillDetailModal
          visible={open}
          skill={selected}
          draft={false}
          editable={false}
          onClose={() => {
            setOpen(false);
            setSelected(null);
          }}
          onChanged={vi.fn()}
        />
      );
    }
    await renderDetail(<ClosingFixture />);
    expect(await screen.findByTestId('skill-markdown')).toHaveTextContent('Detailed workflow.');
    const close = screen.getAllByRole('button', { name: '关闭' }).find((node) => node.classList.contains('arco-btn'))!;
    fireEvent.click(close);
    expect(screen.getByTestId('skill-markdown')).toHaveTextContent('Detailed workflow.');
    expect(screen.queryByText('此 Skill 的源文件未由后端提供')).not.toBeInTheDocument();
  });
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.loadFiles.mockResolvedValue(['SKILL.md']);
    mocks.loadContent.mockResolvedValue('# AlphaFold2\n\nDetailed workflow.');
    mocks.duplicate.mockResolvedValue({});
    mocks.saveFile.mockResolvedValue({});
    mocks.publish.mockResolvedValue({});
    mocks.deleteDraft.mockResolvedValue({});
  });

  it('loads once, renders the v1.1-style detail, and does not loop on Message identity changes', async () => {
    mocks.loadContent.mockResolvedValue(
      '\n---\nname: alphafold2\ndescription: Internal metadata\n---\n\n# AlphaFold2\n\nDetailed workflow.'
    );
    await renderModal({ skill: bundledSkill, draft: false, editable: false });

    expect(await screen.findByTestId('skill-markdown')).toHaveTextContent('Detailed workflow.');
    expect(screen.queryByText('Internal metadata')).not.toBeInTheDocument();
    expect(screen.getByText('Predict protein structures with ColabFold.')).toBeInTheDocument();
    expect(screen.getByText('内置')).toBeInTheDocument();
    expect(screen.getByText('Apache-2.0')).toBeInTheDocument();
    expect(screen.getByText('OPERON')).toBeInTheDocument();
    expect(screen.getByText(/AlphaFold2 · Google DeepMind/)).toBeInTheDocument();
    expect(screen.queryByText('第三方 LLM')).not.toBeInTheDocument();

    await waitFor(() => expect(mocks.loadFiles).toHaveBeenCalledTimes(1));
    expect(mocks.loadContent).toHaveBeenCalledTimes(1);
  });

  it('creates an editable personal draft from a read-only bundled skill', async () => {
    const onChanged = vi.fn();
    const onClose = vi.fn();
    await renderModal({ skill: bundledSkill, draft: false, editable: false, onChanged, onClose });
    await screen.findByTestId('skill-markdown');

    fireEvent.click(screen.getByRole('button', { name: '创建可编辑副本' }));
    fireEvent.change(screen.getByRole('textbox', { name: '副本 Skill 名称' }), {
      target: { value: 'alphafold2-lab' },
    });
    fireEvent.click(screen.getByRole('button', { name: '创建副本' }));

    await waitFor(() => expect(mocks.duplicate).toHaveBeenCalledWith('alphafold2', 'alphafold2-lab'));
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('uses localized catalog copy and category without inventing authorship for imported skills', async () => {
    mocks.loadContent.mockResolvedValue('<!-- provenance metadata -->\n# Workflow\n\nOriginal instructions.');
    await renderModal({
      skill: {
        ...bundledSkill,
        source: 'github',
        category: 'structural-biology',
        description_i18n: { 'zh-CN': '预测蛋白质结构。' },
      } as SkillModalItem,
      draft: false,
      editable: false,
    });
    expect(await screen.findByTestId('skill-markdown')).toHaveTextContent('Original instructions.');
    expect(screen.getByText('预测蛋白质结构。')).toBeInTheDocument();
    expect(screen.getByText('结构生物学与蛋白质工程')).toBeInTheDocument();
    expect(screen.queryByText('structural-biology')).not.toBeInTheDocument();
    expect(screen.queryByText('X-Science')).not.toBeInTheDocument();
    expect(screen.queryByText(/provenance metadata/)).not.toBeInTheDocument();
  });

  it('edits and saves a draft file with the backend old/new content contract', async () => {
    mocks.saveFile.mockResolvedValue({});
    await renderModal({ skill: { ...bundledSkill, source: 'personal-draft' }, draft: true, editable: true });

    const editor = await screen.findByRole('textbox', { name: 'Skill 文件内容' });
    fireEvent.change(editor, { target: { value: '# Updated workflow' } });
    fireEvent.click(screen.getByRole('button', { name: '保存' }));

    await waitFor(() =>
      expect(mocks.saveFile).toHaveBeenCalledWith(
        'alphafold2',
        'SKILL.md',
        '# AlphaFold2\n\nDetailed workflow.',
        '# Updated workflow'
      )
    );
  });

  it('keeps the selected file content when earlier reads finish out of order', async () => {
    let finishEarlier!: (value: string) => void;
    mocks.loadFiles.mockResolvedValue(['SKILL.md', 'notes.md', 'scripts/run.py']);
    mocks.loadContent.mockImplementation(async (_name: string, path: string) => {
      if (path === 'notes.md')
        return new Promise<string>((resolve) => {
          finishEarlier = resolve;
        });
      return path === 'SKILL.md' ? '# Workflow' : 'print("current source")';
    });
    await renderModal({ skill: bundledSkill, draft: false, editable: false });
    await screen.findByRole('heading', { name: 'Workflow' });
    fireEvent.click(screen.getByRole('combobox', { name: 'Skill 文件' }));
    fireEvent.click(await screen.findByText('notes.md', { exact: true }));
    await waitFor(() => expect(mocks.loadContent).toHaveBeenCalledWith('alphafold2', 'notes.md'));
    fireEvent.click(screen.getByRole('combobox', { name: 'Skill 文件' }));
    fireEvent.click(await screen.findByText('scripts/run.py', { exact: true }));
    expect(await screen.findByTestId('skill-source-preview')).toHaveTextContent('current source');
    await act(async () => {
      finishEarlier('# Wrong earlier file');
    });
    expect(screen.getByTestId('skill-source-preview')).not.toHaveTextContent('Wrong earlier file');
    expect(screen.getByTestId('skill-source-preview')).toHaveTextContent('current source');
  });

  it('does not allow a failed initial file read to be saved or published as an empty draft', async () => {
    mocks.loadContent.mockRejectedValue(new Error('unavailable'));
    await renderModal({ skill: bundledSkill, draft: true, editable: true });
    await waitFor(() => expect(mocks.loadContent).toHaveBeenCalledTimes(1));
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '发布' })).toBeDisabled();
  });

  it.each(['save', 'publish', 'delete', 'duplicate'] as const)(
    'does not apply a late %s receipt to a reopened detail',
    async (action) => {
      const receipt = deferred<unknown>();
      const service = {
        save: mocks.saveFile,
        publish: mocks.publish,
        delete: mocks.deleteDraft,
        duplicate: mocks.duplicate,
      }[action];
      service.mockReturnValueOnce(receipt.promise);
      mocks.loadContent.mockImplementation(async (name: string) => `# ${name} source`);
      const onChanged = vi.fn();
      const onClose = vi.fn();
      const view = await renderModal({
        skill: bundledSkill,
        draft: action !== 'duplicate',
        editable: action !== 'duplicate',
        onChanged,
        onClose,
      });
      await waitFor(() =>
        expect(
          screen.queryByText('alphafold2 source') || screen.queryByDisplayValue('# alphafold2 source')
        ).toBeInTheDocument()
      );
      if (action === 'duplicate') {
        fireEvent.click(screen.getByRole('button', { name: '创建可编辑副本' }));
        fireEvent.click(screen.getByRole('button', { name: '创建副本' }));
      } else {
        fireEvent.click(
          screen.getByRole('button', { name: { save: '保存', publish: '发布', delete: '删除草稿' }[action] })
        );
      }
      await waitFor(() => expect(service).toHaveBeenCalled());
      view.rerender(
        <SkillDetailModal
          visible={false}
          skill={null}
          draft={false}
          editable={false}
          onChanged={onChanged}
          onClose={onClose}
        />
      );
      view.rerender(
        <SkillDetailModal
          visible
          skill={{ ...bundledSkill, name: 'boltz2', displayName: 'Boltz2' }}
          draft
          editable
          onChanged={onChanged}
          onClose={onClose}
        />
      );
      await screen.findByDisplayValue('# boltz2 source');
      await act(async () => receipt.resolve({}));
      expect(onChanged).not.toHaveBeenCalled();
      expect(onClose).not.toHaveBeenCalled();
      expect(screen.getByRole('textbox', { name: 'Skill 文件内容' })).toHaveValue('# boltz2 source');
      expect(screen.getByRole('button', { name: '保存' })).toBeEnabled();
    }
  );

  it('freezes the submitted file and draft while a write is pending, with close still available', async () => {
    const receipt = deferred<unknown>();
    mocks.saveFile.mockReturnValueOnce(receipt.promise);
    mocks.loadFiles.mockResolvedValue(['SKILL.md', 'notes.md']);
    await renderModal({ skill: bundledSkill, draft: true, editable: true });
    const editor = await readyEditor();
    fireEvent.change(editor, { target: { value: '# Retained draft' } });
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    await waitFor(() => expect(mocks.saveFile).toHaveBeenCalledTimes(1));
    expect(editor).toBeDisabled();
    expect(screen.getByRole('combobox', { name: 'Skill 文件' })).toHaveAttribute('aria-disabled', 'true');
    expect(
      screen.getAllByRole('button', { name: '关闭' }).some((button) => !(button as HTMLButtonElement).disabled)
    ).toBe(true);
    await act(async () => receipt.resolve({}));
    expect(editor).toBeEnabled();
    expect(editor).toHaveValue('# Retained draft');
  });

  it('keeps an initial read failure visible and retries the same file without writing', async () => {
    mocks.loadContent.mockRejectedValueOnce(new Error('unavailable')).mockResolvedValueOnce('# Recovered source');
    await renderModal({ skill: bundledSkill, draft: true, editable: true });
    expect(await screen.findByRole('alert')).toHaveTextContent('无法读取 Skill 文件');
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    await screen.findByDisplayValue('# Recovered source');
    expect(mocks.loadContent).toHaveBeenLastCalledWith('alphafold2', 'SKILL.md');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(mocks.saveFile).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: '发布' })).toBeEnabled();
  });

  it('retries only library refresh after a successful save, never repeats the mutation', async () => {
    mocks.saveFile.mockResolvedValue({});
    const onChanged = vi.fn().mockRejectedValueOnce(new Error('refresh unavailable')).mockResolvedValue(undefined);
    await renderModal({ skill: bundledSkill, draft: true, editable: true, onChanged });
    const editor = await readyEditor();
    fireEvent.change(editor, { target: { value: '# Saved source' } });
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('变更已完成');
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(2));
    expect(mocks.saveFile).toHaveBeenCalledTimes(1);
    expect(editor).toHaveValue('# Saved source');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('does not close a new detail after the prior receipt starts an asynchronous library refresh', async () => {
    const refresh = deferred<void>();
    mocks.publish.mockResolvedValue({});
    const onChanged = vi.fn(() => refresh.promise);
    const onClose = vi.fn();
    const view = await renderModal({ skill: bundledSkill, draft: true, editable: true, onChanged, onClose });
    await readyEditor();
    fireEvent.click(screen.getByRole('button', { name: '发布' }));
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    view.rerender(
      <SkillDetailModal
        visible={false}
        skill={null}
        draft={false}
        editable={false}
        onChanged={onChanged}
        onClose={onClose}
      />
    );
    view.rerender(
      <SkillDetailModal
        visible
        skill={{ ...bundledSkill, name: 'boltz2', displayName: 'Boltz2' }}
        draft
        editable
        onChanged={onChanged}
        onClose={onClose}
      />
    );
    await readyEditor();
    await act(async () => refresh.resolve());
    expect(onClose).not.toHaveBeenCalled();
  });

  it('invalidates a saved receipt even when the same skill and mode are reopened', async () => {
    const receipt = deferred<unknown>();
    mocks.saveFile.mockReturnValueOnce(receipt.promise);
    const onChanged = vi.fn();
    const onClose = vi.fn();
    const props = { skill: bundledSkill, draft: true, editable: true, onChanged, onClose };
    const view = await renderModal(props);
    await readyEditor();
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    await waitFor(() => expect(mocks.saveFile).toHaveBeenCalledTimes(1));
    view.rerender(<SkillDetailModal {...props} visible={false} />);
    mocks.loadContent.mockResolvedValue('# Reopened same skill');
    view.rerender(<SkillDetailModal {...props} visible />);
    await screen.findByDisplayValue('# Reopened same skill');
    await act(async () => receipt.resolve({}));
    expect(onChanged).not.toHaveBeenCalled();
    expect(screen.getByRole('textbox', { name: 'Skill 文件内容' })).toHaveValue('# Reopened same skill');
  });

  it('coalesces rapid write activation before the busy render commits', async () => {
    const receipt = deferred<unknown>();
    mocks.saveFile.mockReturnValue(receipt.promise);
    await renderModal({ skill: bundledSkill, draft: true, editable: true });
    await readyEditor();
    const save = screen.getByRole('button', { name: '保存' });
    act(() => {
      fireEvent.click(save);
      fireEvent.click(save);
    });
    expect(mocks.saveFile).toHaveBeenCalledTimes(1);
    await act(async () => receipt.resolve({}));
  });

  it('retains a failed write draft and localizes the safe error after switching language', async () => {
    const receipt = deferred<unknown>();
    mocks.saveFile.mockReturnValueOnce(receipt.promise);
    const view = await renderModal({ skill: bundledSkill, draft: true, editable: true });
    const editor = await readyEditor();
    fireEvent.change(editor, { target: { value: '# Unsaved draft' } });
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    await act(async () => view.i18n.changeLanguage('en-US'));
    await act(async () => receipt.reject(new Error('private backend detail')));
    expect(screen.getByRole('alert')).toHaveTextContent('Failed to save the skill draft.');
    expect(screen.getByRole('alert')).not.toHaveTextContent('private backend detail');
    expect(editor).toHaveValue('# Unsaved draft');
    fireEvent.click(screen.getByRole('button', { name: 'Save', exact: true }));
    await waitFor(() => expect(mocks.saveFile).toHaveBeenCalledTimes(2));
    expect(mocks.saveFile).toHaveBeenLastCalledWith(
      'alphafold2',
      'SKILL.md',
      '# AlphaFold2\n\nDetailed workflow.',
      '# Unsaved draft'
    );
    expect(mocks.loadFiles).toHaveBeenCalledTimes(1);
  });

  it('distinguishes an unavailable catalogue from an empty list and retries only its read', async () => {
    mocks.loadFiles.mockRejectedValueOnce(new Error('unavailable')).mockResolvedValue(['SKILL.md']);
    await renderModal({ skill: bundledSkill, draft: false, editable: false });
    expect(await screen.findByRole('alert')).toHaveTextContent('无法读取 Skill 文件');
    expect(screen.queryByText('此 Skill 的源文件未由后端提供')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    expect(await screen.findByTestId('skill-markdown')).toHaveTextContent('Detailed workflow.');
    expect(mocks.loadFiles).toHaveBeenCalledTimes(2);
    expect(mocks.saveFile).not.toHaveBeenCalled();
  });

  it('does not paint the previous skill source while the replacement read starts', async () => {
    const paints: Array<{ name: string; content: string | undefined }> = [];
    function PaintProbe({ name }: { name: string }) {
      React.useLayoutEffect(() => {
        paints.push({ name, content: document.querySelector<HTMLTextAreaElement>('textarea')?.value });
      }, [name]);
      return (
        <SkillDetailModal
          visible
          skill={{ ...bundledSkill, name }}
          draft
          editable
          onClose={vi.fn()}
          onChanged={vi.fn()}
        />
      );
    }
    const view = await renderDetail(<PaintProbe name='alphafold2' />);
    await readyEditor();
    await act(async () => view.rerender(<PaintProbe name='boltz2' />));
    expect(paints.find((paint) => paint.name === 'boltz2')?.content).not.toBe('# AlphaFold2\n\nDetailed workflow.');
  });
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((finish, fail) => {
    resolve = finish;
    reject = fail;
  });
  return { promise, resolve, reject };
}

async function readyEditor() {
  const editor = await screen.findByRole('textbox', { name: 'Skill 文件内容' });
  await waitFor(() => expect(editor).toHaveValue('# AlphaFold2\n\nDetailed workflow.'));
  await waitFor(() => expect(screen.getByRole('button', { name: '保存' })).toBeEnabled());
  return editor;
}

function renderModal({
  skill,
  draft,
  editable,
  onChanged = vi.fn(),
  onClose = vi.fn(),
}: {
  skill: SkillModalItem;
  draft: boolean;
  editable: boolean;
  onChanged?: () => void | Promise<void>;
  onClose?: () => void;
}) {
  return renderDetail(
    <ConfigProvider>
      <SkillDetailModal
        visible
        skill={skill}
        draft={draft}
        editable={editable}
        onChanged={onChanged}
        onClose={onClose}
      />
    </ConfigProvider>
  );
}

async function renderDetail(ui: React.ReactElement) {
  let view!: Awaited<ReturnType<typeof renderWithSettingsI18n>>;
  await act(async () => {
    view = await renderWithSettingsI18n(ui);
  });
  return view;
}
