import { ConfigProvider } from '@arco-design/web-react';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import XScienceSkillsSettings from '@/renderer/pages/settings/XScienceSkillsSettings';
import type { XScienceSkillDraft } from '@/renderer/services/skills/xScienceSkillLibrary';
import { renderWithSettingsI18n } from './settingsI18nTestUtils';

const mocks = vi.hoisted(() => ({
  loadSkills: vi.fn(),
  loadDrafts: vi.fn(),
  loadSources: vi.fn(),
  loadUsage: vi.fn(),
  files: vi.fn(),
  snapshot: vi.fn(),
  publish: vi.fn(),
  importFile: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
}));

vi.mock('@arco-design/web-react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@arco-design/web-react')>();
  return {
    ...actual,
    Message: {
      ...actual.Message,
      useMessage: () => [{ success: mocks.success, warning: vi.fn(), error: mocks.error }, null],
    },
  };
});
vi.mock('@/renderer/services/skills/skillsCatalog', () => ({
  loadAvailableSkillsWithXScience: mocks.loadSkills,
}));
vi.mock('@/renderer/services/skills/xScienceSkillUsage', () => ({
  loadXScienceSkillUsage: mocks.loadUsage,
  findXScienceSkillUsage: (): null => null,
}));
vi.mock('@/renderer/services/skills/xScienceSkillLibrary', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/renderer/services/skills/xScienceSkillLibrary')>();
  return {
    ...actual,
    loadXScienceSkillDrafts: mocks.loadDrafts,
    loadXScienceSkillSources: mocks.loadSources,
    loadXScienceSkillFiles: mocks.files,
    loadXScienceSkillFileSnapshot: mocks.snapshot,
    publishXScienceSkillDraft: mocks.publish,
    importXScienceSkillFile: mocks.importFile,
  };
});

const name = 'reusable-procedure';
const revision = 'a'.repeat(64);
const content = '# Reviewed reusable procedure';
const draft: XScienceSkillDraft = {
  name,
  displayName: 'Reusable procedure',
  description: '',
  files: ['SKILL.md'],
  updatedAt: null,
};
const published = {
  name,
  displayName: draft.displayName,
  description: '',
  source: 'personal',
  skillId: `personal:${name}`,
  enabled: true,
};

beforeEach(() => {
  vi.resetAllMocks();
  vi.spyOn(console, 'error').mockImplementation(() => undefined);
  mocks.loadSkills.mockResolvedValue([]);
  mocks.loadDrafts.mockResolvedValue([draft]);
  mocks.loadSources.mockResolvedValue([]);
  mocks.loadUsage.mockResolvedValue({});
  mocks.files.mockResolvedValue(['SKILL.md']);
  mocks.snapshot.mockResolvedValue({ content, origin: 'draft', draftRevision: revision, files: ['SKILL.md'] });
  mocks.publish.mockResolvedValue({ status: 'published', name, origin: 'personal' });
  mocks.importFile.mockResolvedValue({});
});
afterEach(() => vi.restoreAllMocks());

async function renderPage() {
  await renderWithSettingsI18n(
    <MemoryRouter>
      <ConfigProvider>
        <XScienceSkillsSettings withWrapper={false} />
      </ConfigProvider>
    </MemoryRouter>,
    'en-US'
  );
  await screen.findByTestId(`x-science-skill-draft-${name}`);
}

async function openDraft() {
  fireEvent.click(screen.getByTestId(`x-science-skill-draft-${name}`));
  await screen.findByDisplayValue(content);
  await waitFor(() => expect(screen.getByRole('button', { name: 'Publish' })).toBeEnabled());
  // Represent committed backend publication; the parent and detail are real.
  mocks.loadSkills.mockResolvedValue([published]);
  mocks.loadDrafts.mockResolvedValue([]);
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe('actual Skill settings publication readback', () => {
  it.each(['loadSkills', 'loadDrafts'] as const)(
    'retains the completed publication when %s fails and retries only catalog reads',
    async (failedRead) => {
      await renderPage();
      await openDraft();
      mocks[failedRead].mockRejectedValueOnce(new Error('controlled catalog read unavailable'));
      fireEvent.click(screen.getByRole('button', { name: 'Publish' }));
      const detail = await screen.findByTestId('skill-detail-modal');
      await waitFor(() =>
        expect(within(detail).getByRole('alert')).toHaveTextContent(
          'The change was completed, but the skill library could not refresh.'
        )
      );
      expect(mocks.publish).toHaveBeenCalledTimes(1);
      expect(mocks.publish).toHaveBeenCalledWith(name, false, { expectedDraftRevision: revision });
      expect(mocks.loadSkills).toHaveBeenCalledTimes(2);
      expect(mocks.loadDrafts).toHaveBeenCalledTimes(2);
      expect(screen.getByRole('button', { name: 'Publish' })).toBeDisabled();
      expect(mocks.success).not.toHaveBeenCalled();

      mocks[failedRead].mockRejectedValueOnce(new Error('controlled catalog still unavailable'));
      fireEvent.click(within(detail).getByRole('button', { name: 'Retry' }));
      await waitFor(() => expect(mocks.loadSkills).toHaveBeenCalledTimes(3));
      await waitFor(() => expect(within(detail).getByRole('button', { name: 'Retry' })).toBeEnabled());
      expect(within(detail).getByRole('alert')).toHaveTextContent('The change was completed');
      expect(mocks.publish).toHaveBeenCalledTimes(1);
      expect(mocks.success).not.toHaveBeenCalled();

      fireEvent.click(within(detail).getByRole('button', { name: 'Retry' }));
      await waitFor(() => expect(mocks.success).toHaveBeenCalledWith('Skill published'));
      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
      expect(screen.getByTestId(`x-science-skill-row-${name}`)).toBeInTheDocument();
      expect(screen.queryByTestId(`x-science-skill-draft-${name}`)).not.toBeInTheDocument();
      expect(mocks.loadSkills).toHaveBeenCalledTimes(4);
      expect(mocks.loadDrafts).toHaveBeenCalledTimes(4);
      expect(mocks.publish).toHaveBeenCalledTimes(1);
      expect(mocks.snapshot).toHaveBeenCalledTimes(1);
    }
  );

  it.each(['loadUsage', 'loadSources'] as const)(
    'keeps optional %s failure visible without blocking or repeating the committed publication',
    async (optionalRead) => {
      await renderPage();
      await openDraft();
      mocks[optionalRead].mockRejectedValueOnce(new Error('controlled optional read unavailable'));
      fireEvent.click(screen.getByRole('button', { name: 'Publish' }));
      await waitFor(() => expect(mocks.success).toHaveBeenCalledWith('Skill published'));
      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
      expect(screen.getByTestId('x-science-skills-load-error')).toHaveTextContent(
        'Some skill data could not be loaded. Existing results remain available.'
      );
      expect(screen.getByTestId(`x-science-skill-row-${name}`)).toBeInTheDocument();
      expect(mocks.publish).toHaveBeenCalledTimes(1);
      expect(mocks.publish).toHaveBeenCalledWith(name, false, { expectedDraftRevision: revision });
      expect(mocks.loadSkills).toHaveBeenCalledTimes(2);
      expect(mocks.loadDrafts).toHaveBeenCalledTimes(2);
    }
  );

  it('retains the publication receipt when an older required refresh is superseded by an ordinary refresh', async () => {
    await renderPage();
    const importing = deferred<Record<string, unknown>>();
    mocks.importFile.mockReturnValueOnce(importing.promise);
    const input = document.querySelector<HTMLInputElement>('input[type="file"]');
    expect(input).not.toBeNull();
    fireEvent.change(input!, { target: { files: [new File(['# Independent import'], 'import-procedure.md')] } });
    await waitFor(() => expect(mocks.importFile).toHaveBeenCalledTimes(1));
    await openDraft();

    const oldRead = deferred<Array<typeof published>>();
    mocks.loadSkills.mockReturnValueOnce(oldRead.promise).mockResolvedValueOnce([published]);
    fireEvent.click(screen.getByRole('button', { name: 'Publish' }));
    await waitFor(() => expect(mocks.loadSkills).toHaveBeenCalledTimes(2));
    // Complete the already-started import. Its real parent callback starts the
    // newer ordinary refresh while the publication's required read is pending.
    await act(async () => importing.resolve({ status: 'imported' }));
    await waitFor(() => expect(mocks.loadSkills).toHaveBeenCalledTimes(3));
    await screen.findByTestId(`x-science-skill-row-${name}`);
    await act(async () => oldRead.resolve([]));
    const detail = await screen.findByTestId('skill-detail-modal');
    await waitFor(() => expect(within(detail).getByRole('alert')).toHaveTextContent('The change was completed'));
    expect(screen.getByTestId(`x-science-skill-row-${name}`)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Publish' })).toBeDisabled();
    expect(mocks.success).not.toHaveBeenCalledWith('Skill published');
    expect(mocks.publish).toHaveBeenCalledTimes(1);
    expect(mocks.snapshot).toHaveBeenCalledTimes(1);

    fireEvent.click(within(detail).getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(mocks.success).toHaveBeenCalledWith('Skill published'));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(mocks.loadSkills).toHaveBeenCalledTimes(4);
    expect(mocks.publish).toHaveBeenCalledTimes(1);
    expect(mocks.importFile).toHaveBeenCalledTimes(1);
    expect(mocks.snapshot).toHaveBeenCalledTimes(1);
  });
});
