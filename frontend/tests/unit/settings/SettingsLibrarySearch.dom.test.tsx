import { fireEvent, screen } from '@testing-library/react';
import React, { useState } from 'react';
import { describe, expect, it } from 'vitest';
import SettingsLibrarySearch from '@/renderer/pages/settings/components/SettingsLibrarySearch';
import { renderWithSettingsI18n } from './settingsI18nTestUtils';

function Fixture() {
  const [query, setQuery] = useState('vina');
  return <SettingsLibrarySearch label='Search skills' value={query} onChange={setQuery} />;
}
describe('catalog search keyboard and clear', () => {
  it('has a named clear button and returns focus to the same search field', async () => {
    await renderWithSettingsI18n(<Fixture />, 'en-US');
    const field = screen.getByRole('searchbox', { name: 'Search skills' });
    const clear = screen.getByRole('button', { name: 'Clear · Search skills' });
    clear.focus();
    fireEvent.click(clear);
    expect(field).toHaveValue('');
    expect(field).toHaveFocus();
    expect(screen.queryByRole('button', { name: 'Clear · Search skills' })).not.toBeInTheDocument();
  });
  it('clears with Escape without submitting a task or leaving the field', async () => {
    await renderWithSettingsI18n(<Fixture />, 'en-US');
    const field = screen.getByRole('searchbox', { name: 'Search skills' });
    field.focus();
    fireEvent.keyDown(field, { key: 'Escape' });
    expect(field).toHaveValue('');
    expect(field).toHaveFocus();
  });
});
