import { fireEvent, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { SkillRow } from '@/renderer/pages/settings/skills/SkillLibraryCard';
import { renderWithSettingsI18n } from './settingsI18nTestUtils';

async function card() {
  const onOpen = vi.fn();
  const onToggle = vi.fn();
  await renderWithSettingsI18n(
    <SkillRow
      skill={{ name: 'sample-skill', description: 'A sample capability', enabled: true }}
      pendingSkill={null}
      personal={false}
      usage={null}
      usageAvailable={false}
      onOpen={onOpen}
      onToggle={onToggle}
      onRemove={vi.fn()}
    />
  );
  return { onOpen, onToggle };
}
describe('Independent catalog card actions', () => {
  it('does not nest the enable switch inside a button-role container', async () => {
    const { onOpen, onToggle } = await card();
    const toggle = screen.getByRole('switch');
    expect(toggle.parentElement?.closest('button, [role="button"]')).toBeNull();
    fireEvent.click(toggle);
    expect(onToggle).toHaveBeenCalledTimes(1);
    expect(onOpen).not.toHaveBeenCalled();
  });
  it('opens the details with one native keyboard action', async () => {
    const { onOpen } = await card();
    const open = screen.getByRole('button', { name: /sample-skill/ });
    expect(open.tagName).toBe('BUTTON');
    open.focus();
    await userEvent.setup().keyboard('{Enter}');
    expect(onOpen).toHaveBeenCalledTimes(1);
  });
});
