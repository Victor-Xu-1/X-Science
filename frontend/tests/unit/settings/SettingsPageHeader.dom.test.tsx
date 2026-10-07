import { fireEvent, render, screen } from '@testing-library/react';
import React, { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import SettingsPageHeader from '@/renderer/pages/settings/components/SettingsPageHeader';
import { handleTabListKeyDown } from '@/renderer/utils/tabListKeyboard';

function Fixture({ vertical = false, rtl = false }: { vertical?: boolean; rtl?: boolean }) {
  const [active, setActive] = useState('first');
  return (
    <div
      role='tablist'
      aria-label='Modes'
      aria-orientation={vertical ? 'vertical' : 'horizontal'}
      style={{ direction: rtl ? 'rtl' : 'ltr' }}
      onKeyDown={handleTabListKeyDown}
    >
      {['first', 'disabled', 'last'].map((key) => (
        <button
          key={key}
          role='tab'
          disabled={key === 'disabled'}
          aria-selected={active === key}
          tabIndex={active === key ? 0 : -1}
          onClick={() => setActive(key)}
        >
          {key}
        </button>
      ))}
      <button>Unrelated action</button>
    </div>
  );
}

describe('settings tab keyboard contract', () => {
  it('labels the real header tablist, keeps one Tab stop and selects on arrow navigation', () => {
    const onChange = vi.fn();
    render(
      <SettingsPageHeader
        title='Catalog'
        tabs={[
          { key: 'one', label: 'One' },
          { key: 'two', label: 'Two' },
        ]}
        activeTab='one'
        onTabChange={onChange}
      />
    );
    expect(screen.getByRole('tablist', { name: 'Catalog' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'One' })).toHaveAttribute('tabindex', '0');
    expect(screen.getByRole('tab', { name: 'Two' })).toHaveAttribute('tabindex', '-1');
    screen.getByRole('tab', { name: 'One' }).focus();
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowRight' });
    expect(screen.getByRole('tab', { name: 'Two' })).toHaveFocus();
    expect(onChange).toHaveBeenCalledExactlyOnceWith('two');
  });
  it('skips disabled tabs, wraps and supports Home/End without trapping Tab', () => {
    render(<Fixture />);
    const first = screen.getByRole('tab', { name: 'first' });
    const last = screen.getByRole('tab', { name: 'last' });
    first.focus();
    fireEvent.keyDown(first, { key: 'ArrowRight' });
    expect(last).toHaveFocus();
    expect(last).toHaveAttribute('aria-selected', 'true');
    expect(first).toHaveAttribute('tabindex', '-1');
    fireEvent.keyDown(last, { key: 'ArrowRight' });
    expect(first).toHaveFocus();
    fireEvent.keyDown(first, { key: 'End' });
    expect(last).toHaveFocus();
    fireEvent.keyDown(last, { key: 'Home' });
    expect(first).toHaveFocus();
    const key = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    first.dispatchEvent(key);
    expect(key.defaultPrevented).toBe(false);
  });
  it('respects vertical orientation and unrelated controls/modified shortcuts', () => {
    render(<Fixture vertical />);
    const first = screen.getByRole('tab', { name: 'first' });
    first.focus();
    fireEvent.keyDown(first, { key: 'ArrowRight' });
    expect(first).toHaveFocus();
    fireEvent.keyDown(first, { key: 'ArrowDown', ctrlKey: true });
    expect(first).toHaveFocus();
    fireEvent.keyDown(first, { key: 'ArrowDown' });
    expect(screen.getByRole('tab', { name: 'last' })).toHaveFocus();
    const unrelated = screen.getByRole('button', { name: 'Unrelated action' });
    unrelated.focus();
    fireEvent.keyDown(unrelated, { key: 'Home' });
    expect(unrelated).toHaveFocus();
  });
  it('follows horizontal reading direction in RTL', () => {
    render(<Fixture rtl />);
    const first = screen.getByRole('tab', { name: 'first' });
    first.focus();
    fireEvent.keyDown(first, { key: 'ArrowLeft' });
    expect(screen.getByRole('tab', { name: 'last' })).toHaveFocus();
  });
});
