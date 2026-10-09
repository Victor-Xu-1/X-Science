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
  saveInstructions: vi.fn(),
  enable: vi.fn(),
}));
vi.mock('@/renderer/services/agents/synonBiomedExpertProfiles', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/agents/synonBiomedExpertProfiles')>()),
  loadSynonBiomedExpertProfilesWithRuntimeConnectors: api.profiles,
  loadSynonBiomedExpertInstructions: api.instructions,
  updateSynonBiomedExpertSkills: api.saveSkills,
  saveSynonBiomedExpertInstructions: api.saveInstructions,
  setSynonBiomedExpertProfileEnabled: api.enable,
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
let selectedConnectorIds: string[];
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
    connectorIds: [...selectedConnectorIds],
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
  selectedConnectorIds = [];
  window.location.hash = '';
  api.profiles.mockImplementation(async () => [profile()]);
  api.instructions.mockResolvedValue('Test instructions');
  api.skills.mockResolvedValue(catalog);
  api.connectors.mockResolvedValue([]);
  api.usage.mockResolvedValue({});
  api.saveInstructions.mockResolvedValue(undefined);
  api.enable.mockImplementation(async (_name: string, enabled: boolean) => ({ ...profile(), enabled }));
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

  it('retains selected connector IDs when their metadata is unavailable', async () => {
    selectedConnectorIds = ['unavailable-user-connector'];
    api.connectors.mockResolvedValue([]);
    const { dialog } = await open('en-US');
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Connectors 1' }));
    expect(within(dialog).getByText('unavailable-user-connector', { exact: true })).toBeVisible();
    expect(selectedConnectorIds).toEqual(['unavailable-user-connector']);
  });
});

describe('reviewing selected expert capabilities', () => {
  it('searches a large selected set by execution ID without changing the saved selection', async () => {
    selectedNames = [...Array.from({ length: 100 }, (_, index) => `research-skill-${index}`), 'existing-skill'];
    const { dialog, i18n } = await open('zh-CN');
    const field = within(dialog).getByRole('searchbox', {
      name: i18n.t('settings.expertsSettings.searchSelectedSkills'),
    });
    const list = within(dialog).getByRole('region', { name: i18n.t('settings.expertsSettings.selectedSkills') });
    expect(list).toHaveAttribute('tabindex', '0');
    expect(within(list).getAllByRole('listitem')).toHaveLength(101);
    fireEvent.change(field, { target: { value: ' EXISTING-SKILL ' } });
    expect(within(list).getAllByRole('listitem')).toHaveLength(1);
    expect(within(list).getByText('中文技能 1', { exact: true })).toBeVisible();
    expect(within(dialog).getByTestId('expert-skill-coverage')).toHaveTextContent('Skills 101/2');
    expect(within(dialog).getByRole('button', { name: i18n.t('settings.expertsSettings.saveChanges') })).toBeDisabled();
    expect(selectedNames).toHaveLength(101);
    expect(api.saveSkills).not.toHaveBeenCalled();
  });

  it('searches translated labels, keeps full identity visible and preserves the query across locale changes', async () => {
    const { dialog, i18n } = await open('zh-CN');
    const field = within(dialog).getByRole('searchbox', {
      name: i18n.t('settings.expertsSettings.searchSelectedSkills'),
    });
    fireEvent.change(field, { target: { value: '中文技能' } });
    expect(within(dialog).getByText('existing-skill', { exact: true })).toBeVisible();
    await act(async () => {
      await i18n.changeLanguage('en-US');
    });
    expect(within(dialog).getByRole('searchbox', { name: 'Search selected skills' })).toHaveValue('中文技能');
    expect(within(dialog).getByText('No matching selected capabilities')).toBeVisible();
    fireEvent.change(within(dialog).getByRole('searchbox', { name: 'Search selected skills' }), {
      target: { value: 'English skill' },
    });
    expect(within(dialog).getByText('English skill 1', { exact: true })).toBeVisible();
    expect(api.instructions).toHaveBeenCalledTimes(1);
    expect(api.saveSkills).not.toHaveBeenCalled();
  });

  it('distinguishes an unmatched query from an empty selection and Escape clears it first', async () => {
    const { dialog, i18n } = await open('en-US');
    const field = within(dialog).getByRole('searchbox', { name: 'Search selected skills' });
    fireEvent.change(field, { target: { value: 'no-matching-entry' } });
    expect(within(dialog).getByText('No matching selected capabilities')).toBeVisible();
    expect(within(dialog).queryByText(i18n.t('settings.expertsSettings.noSkills'))).not.toBeInTheDocument();
    field.focus();
    fireEvent.keyDown(field, { key: 'Escape' });
    expect(field).toHaveValue('');
    expect(field).toHaveFocus();
    expect(dialog).toBeVisible();
    expect(within(dialog).getByText('English skill 1', { exact: true })).toBeVisible();
  });

  it('filters connector display names and raw IDs independently of the Skill query', async () => {
    selectedConnectorIds = ['literature-archive'];
    api.connectors.mockResolvedValue([
      {
        id: 'literature-archive',
        name: 'archive-engine',
        displayName: 'Literature Archive',
        enabled: true,
        attachedAgents: [],
      },
    ]);
    const { dialog, i18n } = await open('en-US');
    fireEvent.change(within(dialog).getByRole('searchbox', { name: 'Search selected skills' }), {
      target: { value: 'nothing' },
    });
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Connectors 1' }));
    const field = within(dialog).getByRole('searchbox', { name: 'Search selected connectors' });
    expect(field).toHaveValue('');
    fireEvent.change(field, { target: { value: 'LITERATURE-ARCHIVE' } });
    const list = within(dialog).getByRole('region', { name: i18n.t('settings.expertsSettings.selectedConnectors') });
    expect(within(list).getByText('Literature Archive', { exact: true })).toBeVisible();
    expect(within(list).getByText('literature-archive', { exact: true })).toBeVisible();
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Skills 1' }));
    expect(within(dialog).getByRole('searchbox', { name: 'Search selected skills' })).toHaveValue('nothing');
    expect(api.saveSkills).not.toHaveBeenCalled();
  });

  it('removes only the matching raw ID and retains hidden selections in the save payload', async () => {
    selectedNames = ['hidden-user-skill', 'existing-skill'];
    const { dialog, i18n } = await open('en-US');
    fireEvent.change(within(dialog).getByRole('searchbox', { name: 'Search selected skills' }), {
      target: { value: 'existing-skill' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove English skill 1' }));
    expect(within(dialog).getByTestId('expert-skill-coverage')).toHaveTextContent('Skills 1/2');
    fireEvent.click(within(dialog).getByRole('button', { name: i18n.t('settings.expertsSettings.saveChanges') }));
    await waitFor(() =>
      expect(api.saveSkills).toHaveBeenCalledWith(
        'test-expert',
        ['hidden-user-skill', 'existing-skill'],
        ['hidden-user-skill']
      )
    );
  });

  it('resets review queries when the same expert is closed and reopened', async () => {
    const { dialog, i18n } = await open('en-US');
    fireEvent.change(within(dialog).getByRole('searchbox', { name: 'Search selected skills' }), {
      target: { value: 'not-present' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: i18n.t('common.cancel') }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    fireEvent.click(within(screen.getByTestId('expert-card-test-expert')).getByRole('button'));
    const reopened = await screen.findByRole('dialog', { name: 'Test Expert' });
    expect(await within(reopened).findByRole('searchbox', { name: 'Search selected skills' })).toHaveValue('');
    expect(api.saveSkills).not.toHaveBeenCalled();
  });
});

describe('expert capability editor response ownership', () => {
  it('keeps a different expert open when an older enable operation finishes', async () => {
    let finish!: (value: SynonBiomedExpertProfile) => void;
    api.enable.mockImplementationOnce(
      () =>
        new Promise<SynonBiomedExpertProfile>((resolve) => {
          finish = resolve;
        })
    );
    const other = { ...profile(), name: 'other-expert', displayName: 'Other Expert', skillNames: ['new-skill'] };
    api.profiles.mockImplementation(async () => [profile(), other]);
    const { dialog, i18n } = await open('en-US');
    fireEvent.click(within(dialog).getByRole('switch', { name: 'Enable Test Expert' }));
    await waitFor(() => expect(api.enable).toHaveBeenCalledTimes(1));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel', exact: true }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    fireEvent.click(within(screen.getByTestId('expert-card-other-expert')).getByRole('button'));
    const next = await screen.findByRole('dialog', { name: 'Other Expert' });
    await within(next).findByRole('combobox', { name: i18n.t('settings.expertsSettings.addSkill') });
    await act(async () => {
      finish({ ...profile(), enabled: false });
    });
    expect(screen.getByRole('dialog', { name: 'Other Expert' })).toBeVisible();
    expect(within(next).getByText('English skill 2', { exact: true })).toBeVisible();
  });

  it('starts one save even when confirmation is invoked twice before repaint', async () => {
    let finish!: () => void;
    api.saveInstructions.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        })
    );
    const { dialog } = await open('en-US');
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Instructions', exact: true }));
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Instructions' }), {
      target: { value: 'One immutable submission' },
    });
    const save = within(dialog).getByRole('button', { name: 'Save changes', exact: true });
    await act(async () => {
      fireEvent.click(save);
      fireEvent.click(save);
    });
    expect(api.saveInstructions).toHaveBeenCalledTimes(1);
    await act(async () => {
      finish();
    });
  });

  it('does not start metadata refresh after its owning workbench is disposed', async () => {
    let finish!: () => void;
    api.saveInstructions.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        })
    );
    const view = await open('en-US');
    fireEvent.click(within(view.dialog).getByRole('tab', { name: 'Instructions', exact: true }));
    fireEvent.change(within(view.dialog).getByRole('textbox', { name: 'Instructions' }), {
      target: { value: 'Submitted before route change' },
    });
    fireEvent.click(within(view.dialog).getByRole('button', { name: 'Save changes', exact: true }));
    await waitFor(() => expect(api.saveInstructions).toHaveBeenCalledTimes(1));
    const reads = api.profiles.mock.calls.length;
    view.unmount();
    await act(async () => {
      finish();
    });
    expect(api.profiles).toHaveBeenCalledTimes(reads);
  });

  it('does not reopen a closed editor after its instruction save finishes', async () => {
    let finish!: () => void;
    api.saveInstructions.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        })
    );
    const { dialog, i18n } = await open('en-US');
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Instructions', exact: true }));
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Instructions' }), {
      target: { value: 'Changed local draft' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: i18n.t('settings.expertsSettings.saveChanges') }));
    await waitFor(() => expect(api.saveInstructions).toHaveBeenCalledTimes(1));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel', exact: true }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    await act(async () => {
      finish();
    });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.queryByText('Expert settings saved', { exact: true })).not.toBeInTheDocument();
  });

  it('does not replace a different expert with an obsolete capability-save result', async () => {
    let finish!: (value: SynonBiomedExpertProfile) => void;
    api.saveSkills.mockImplementationOnce(
      () =>
        new Promise<SynonBiomedExpertProfile>((resolve) => {
          finish = resolve;
        })
    );
    const other = { ...profile(), name: 'other-expert', displayName: 'Other Expert', skillNames: ['new-skill'] };
    api.profiles.mockImplementation(async () => [profile(), other]);
    const { dialog, i18n } = await open('en-US');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove English skill 1' }));
    fireEvent.click(within(dialog).getByRole('button', { name: i18n.t('settings.expertsSettings.saveChanges') }));
    await waitFor(() => expect(api.saveSkills).toHaveBeenCalledTimes(1));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel', exact: true }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    fireEvent.click(within(screen.getByTestId('expert-card-other-expert')).getByRole('button'));
    const nextDialog = await screen.findByRole('dialog', { name: 'Other Expert' });
    await within(nextDialog).findByRole('combobox', { name: i18n.t('settings.expertsSettings.addSkill') });
    await act(async () => {
      selectedNames = [];
      finish(profile());
    });
    expect(screen.getByRole('dialog', { name: 'Other Expert' })).toBeVisible();
    expect(within(nextDialog).getByText('English skill 2', { exact: true })).toBeVisible();
    expect(screen.queryByRole('dialog', { name: 'Test Expert' })).not.toBeInTheDocument();
  });
});
