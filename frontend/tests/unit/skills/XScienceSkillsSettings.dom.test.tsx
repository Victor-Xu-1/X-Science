import React from 'react';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  loadAvailableSkillsWithXScience: vi.fn(),
  loadDrafts: vi.fn(),
  loadSources: vi.fn(),
  messageError: vi.fn(),
}));

vi.mock('@/renderer/services/skills/skillsCatalog', () => ({
  onSkillCatalogChanged: vi.fn(() => () => undefined),
  loadAvailableSkillsWithXScience: mocks.loadAvailableSkillsWithXScience,
}));

vi.mock('@/renderer/services/skills/xScienceSkillLibrary', () => ({
  loadXScienceSkillDrafts: mocks.loadDrafts,
  loadXScienceSkillSources: mocks.loadSources,
  deleteXSciencePersonalSkill: vi.fn(),
  importXScienceSkillFile: vi.fn(),
  removeXScienceSkillSource: vi.fn(),
}));

vi.mock('@/renderer/services/xScienceCapabilities', () => ({
  setXScienceSkillEnabled: vi.fn(),
}));

vi.mock('@/common', () => ({
  ipcBridge: {
    fs: {
      listSkillImportHistory: { invoke: vi.fn(async () => []) },
      getSkillImportLimits: { invoke: vi.fn(async () => ({ max_file_bytes: 0, max_total_bytes: 0 })) },
    },
    dialog: {
      showOpen: { invoke: vi.fn() },
    },
  },
}));

vi.mock('@arco-design/web-react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@arco-design/web-react')>();
  return {
    ...actual,
    Message: {
      ...actual.Message,
      error: mocks.messageError,
      useMessage: () => [{ success: vi.fn(), error: mocks.messageError, warning: vi.fn() }, <div key='messages' />],
    },
  };
});

vi.mock('react-router', () => ({
  useLocation: () => ({ pathname: '/settings/skills' }),
  useNavigate: () => vi.fn(),
  useSearchParams: () => [new URLSearchParams(), vi.fn()],
}));

import XScienceSkillsSettings from '@/renderer/pages/settings/XScienceSkillsSettings';
import { renderWithSettingsI18n } from '../settings/settingsI18nTestUtils';

const xscienceSkills = [
  {
    name: 'alphafold2',
    description: 'Protein structure prediction workflow.',
    location: '/runtime/assets/skills/alphafold2/SKILL.md',
    relative_location: 'skills/alphafold2/SKILL.md',
    is_auto_inject: false,
    is_custom: false,
    source: 'builtin',
  },
  {
    name: 'boltz2-nim',
    description: 'Boltz2 NIM binding workflow.',
    location: '/runtime/assets/skills/boltz2-nim/SKILL.md',
    relative_location: 'skills/boltz2-nim/SKILL.md',
    is_auto_inject: false,
    is_custom: false,
    source: 'builtin',
  },
];

describe('XScienceSkillsSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.loadAvailableSkillsWithXScience.mockResolvedValue(xscienceSkills);
    mocks.loadDrafts.mockResolvedValue([]);
    mocks.loadSources.mockResolvedValue([]);
  });

  it('renders X-Science runtime skills in the shared recommended library', async () => {
    await renderWithSettingsI18n(<XScienceSkillsSettings withWrapper={false} />);

    await waitFor(() => expect(mocks.loadAvailableSkillsWithXScience).toHaveBeenCalled());

    expect(screen.getByTestId('x-science-skills-section')).toBeInTheDocument();
    expect(screen.getByTestId('x-science-skill-row-alphafold2')).toHaveTextContent('alphafold2');
    expect(screen.getByTestId('x-science-skill-row-boltz2-nim')).toHaveTextContent('boltz2-nim');
    expect(screen.getByRole('heading', { name: '技能 2' })).toBeInTheDocument();
  });

  it('searches within X-Science runtime skills', async () => {
    await renderWithSettingsI18n(<XScienceSkillsSettings withWrapper={false} />);

    await waitFor(() => expect(screen.getByTestId('x-science-skill-row-alphafold2')).toBeInTheDocument());

    fireEvent.change(screen.getByTestId('input-search-x-science-skills'), { target: { value: 'boltz' } });

    expect(screen.queryByTestId('x-science-skill-row-alphafold2')).not.toBeInTheDocument();
    expect(screen.getByTestId('x-science-skill-row-boltz2-nim')).toBeInTheDocument();
  });

  it('shows a X-Science-specific empty state', async () => {
    mocks.loadAvailableSkillsWithXScience.mockResolvedValue([]);

    await renderWithSettingsI18n(<XScienceSkillsSettings withWrapper={false} />);

    await waitFor(() => expect(screen.getByText('暂无技能')).toBeInTheDocument());
  });
});
