import React from 'react';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ExpertWorkbench from '@/renderer/pages/settings/SynonBiomedExpertsSettings/ExpertWorkbench';
import type { SynonBiomedExpertProfile } from '@/renderer/services/agents/synonBiomedExpertProfiles';
import { renderWithI18n, type TestLanguage } from '../i18nTestUtils';

const api = vi.hoisted(() => ({
  profiles: vi.fn(),
  instructions: vi.fn(),
  skills: vi.fn(),
  connectors: vi.fn(),
  usage: vi.fn(),
  saveSkills: vi.fn(),
}));
vi.mock('@/renderer/services/agents/synonBiomedExpertProfiles', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/agents/synonBiomedExpertProfiles')>()),
  loadSynonBiomedExpertProfilesWithRuntimeConnectors: api.profiles,
  loadSynonBiomedExpertInstructions: api.instructions,
  updateSynonBiomedExpertSkills: api.saveSkills,
}));
vi.mock('@/renderer/services/synonBiomedCapabilities', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/synonBiomedCapabilities')>()),
  loadSynonBiomedSkills: api.skills,
  loadSynonBiomedMcpServers: api.connectors,
}));
vi.mock('@/renderer/services/agents/synonBiomedExpertUsage', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/agents/synonBiomedExpertUsage')>()),
  loadSynonBiomedExpertUsage: api.usage,
}));
vi.mock('@/renderer/pages/settings/SynonBiomedExpertsSettings/SynonBiomedExpertProfileModal', () => ({
  default: () => null,
}));

let selectedNames: string[];
function profile(): SynonBiomedExpertProfile {
  return {
    name: 'test-expert',
    source: 'user',
    displayName: 'Test Expert',
    description: 'Test description',
    systemPrompt: 'Test instructions',
    iconKey: '',
    colorKey: '',
    enabled: true,
    userHidden: false,
    unrestricted: false,
    skillNames: [...selectedNames],
    connectorIds: [],
  };
}
const catalog = ['existing-skill', 'new-skill'].map((name, index) => ({
  name,
  displayName: `English skill ${index + 1}`,
  description: 'Test skill',
  source: 'bundled',
  attachedAgents: [],
  name_i18n: { 'en-US': `English skill ${index + 1}`, 'zh-CN': `中文技能 ${index + 1}` },
}));

beforeEach(() => {
  vi.clearAllMocks();
  selectedNames = ['existing-skill'];
  window.location.hash = '';
  api.profiles.mockImplementation(async () => [profile()]);
  api.instructions.mockResolvedValue('Test instructions');
  api.skills.mockResolvedValue(catalog);
  api.connectors.mockResolvedValue([]);
  api.usage.mockResolvedValue({});
  api.saveSkills.mockImplementation(async (_name, _previous, next: string[]) => {
    selectedNames = [...next];
    return profile();
  });
});

async function open(language: TestLanguage) {
  let view!: Awaited<ReturnType<typeof renderWithI18n>>;
  await act(async () => {
    view = await renderWithI18n(<ExpertWorkbench />, language);
  });
  const card = await screen.findByTestId('expert-card-test-expert');
  await act(async () => {
    fireEvent.click(within(card).getByRole('button'));
  });
  const dialog = await screen.findByRole('dialog', { name: 'Test Expert' });
  await within(dialog).findByRole('combobox', { name: view.i18n.t('settings.expertsSettings.addSkill') });
  return { ...view, dialog };
}

describe('expert selected Skill display names', () => {
  it.each([
    { language: 'zh-CN' as const, label: '中文技能 1' },
    { language: 'en-US' as const, label: 'English skill 1' },
  ])('localizes existing selected rows and removal names in $language', async ({ language, label }) => {
    const { dialog, i18n } = await open(language);
    expect(within(dialog).getByText(label, { exact: true })).toBeVisible();
    expect(
      within(dialog).getByRole('button', { name: i18n.t('settings.expertsSettings.removeNamed', { name: label }) })
    ).toBeEnabled();
    expect(within(dialog).getByRole('tab', { name: `${i18n.t('settings.skills')} 1` })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(api.saveSkills).not.toHaveBeenCalled();
  });

  it('retains a newly picked Chinese label and saves only raw execution names', async () => {
    const { dialog, i18n } = await open('zh-CN');
    fireEvent.click(within(dialog).getByRole('combobox', { name: i18n.t('settings.expertsSettings.addSkill') }));
    fireEvent.click(await screen.findByRole('option', { name: '中文技能 2' }));
    expect(within(dialog).getByText('中文技能 2', { exact: true })).toBeVisible();
    fireEvent.click(within(dialog).getByRole('button', { name: i18n.t('settings.expertsSettings.saveChanges') }));
    await waitFor(() =>
      expect(api.saveSkills).toHaveBeenCalledWith('test-expert', ['existing-skill'], ['existing-skill', 'new-skill'])
    );
  });

  it('updates the open selected row when language changes without mutating the profile', async () => {
    const { dialog, i18n } = await open('zh-CN');
    expect(within(dialog).getByText('中文技能 1', { exact: true })).toBeVisible();
    await act(async () => {
      await i18n.changeLanguage('en-US');
    });
    expect(within(dialog).getByText('English skill 1', { exact: true })).toBeVisible();
    expect(selectedNames).toEqual(['existing-skill']);
    expect(api.saveSkills).not.toHaveBeenCalled();
  });

  it('keeps an unavailable catalog entry identifiable by its raw name', async () => {
    selectedNames = ['unavailable-user-skill'];
    api.skills.mockResolvedValue([]);
    const { dialog } = await open('zh-CN');
    expect(within(dialog).getByText('unavailable-user-skill', { exact: true })).toBeVisible();
    expect(api.saveSkills).not.toHaveBeenCalled();
  });
});
