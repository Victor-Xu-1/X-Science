import React, { createRef, useState } from 'react';
import type { SelectHandle } from '@arco-design/web-react/es/Select/interface';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import XScienceSelect, { type XScienceSelectProps } from '@/renderer/components/base/XScienceSelect';
import LanguageSwitcher from '@/renderer/components/settings/LanguageSwitcher';

const language = vi.hoisted(() => ({ change: vi.fn() }));
vi.mock('@/renderer/services/i18n', () => ({
  changeLanguage: language.change,
  normalizeLanguageCode: (value: string) => value,
}));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    i18n: { language: 'zh-CN', resolvedLanguage: 'zh-CN' },
    t: (key: string) => (key === 'settings.language' ? 'Language' : key),
  }),
}));

// The original Arco public keyboard handler reads the numeric browser fields.
// user-event leaves those fields at zero in jsdom.
function pressKey(key: string, keyCode: number) {
  fireEvent.keyDown(document.activeElement!, { key, code: key, keyCode, which: keyCode });
}

async function openMenu(user: ReturnType<typeof userEvent.setup>, name = 'Choice') {
  const control = screen.getByRole('combobox', { name });
  await user.click(control);
  const menu = await screen.findByRole('listbox');
  await waitFor(() => expect(menu.closest('.arco-trigger')).not.toHaveStyle({ pointerEvents: 'none' }));
  return { control, menu };
}
const waitForClose = () => waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());

function Choice(props: XScienceSelectProps & React.RefAttributes<SelectHandle>) {
  return (
    <XScienceSelect aria-label='Choice' value='one' {...props}>
      <XScienceSelect.Option value='one'>One</XScienceSelect.Option>
      <XScienceSelect.Option value='two'>Two</XScienceSelect.Option>
    </XScienceSelect>
  );
}

beforeEach(() => {
  language.change.mockResolvedValue(undefined);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

describe('shared Select close-focus ownership', () => {
  it('returns the real LanguageSwitcher to its trigger after Escape without changing language', async () => {
    const user = userEvent.setup();
    render(<LanguageSwitcher />);
    const { control } = await openMenu(user, 'Language');
    pressKey('Escape', 27);
    await waitForClose();
    expect(control).toHaveFocus();
    expect(control).toHaveTextContent('中文');
    expect(language.change).not.toHaveBeenCalled();
  });

  it('preserves the LanguageSwitcher intentional selection blur and single change callback', async () => {
    const user = userEvent.setup();
    render(<LanguageSwitcher />);
    const { control, menu } = await openMenu(user, 'Language');
    await user.click(within(menu).getByRole('option', { name: 'English' }));
    await waitForClose();
    await waitFor(() => expect(language.change).toHaveBeenCalledExactlyOnceWith('en-US'));
    expect(control).not.toHaveFocus();
  });

  it('keeps controlled visibility, callback counts and the complete forwarded public handle', async () => {
    const user = userEvent.setup();
    const changes = vi.fn();
    const changed = vi.fn();
    const cleaned = vi.fn();
    const handle: { current: SelectHandle | null } = { current: null };
    function Controlled() {
      const [open, setOpen] = useState(false);
      return (
        <Choice
          ref={(next) => {
            handle.current = next;
            return () => {
              cleaned(next);
              if (handle.current === next) handle.current = null;
            };
          }}
          popupVisible={open}
          onChange={changed}
          onVisibleChange={(next) => {
            changes(next);
            setOpen(next);
          }}
        />
      );
    }
    const rendered = render(<Controlled />);
    const { control } = await openMenu(user);
    expect(handle.current?.dom).toBe(control);
    expect(Object.keys(handle.current!)).toEqual(
      expect.arrayContaining([
        'dom',
        'focus',
        'blur',
        'hotkeyHandler',
        'activeOptionValue',
        'getOptionInfoList',
        'getOptionInfoByValue',
        'scrollIntoView',
        'getRootDOMNode',
      ])
    );
    expect(handle.current!.getOptionInfoList()).toHaveLength(2);
    const beforeArrow = handle.current;
    pressKey('ArrowDown', 40);
    expect(cleaned).toHaveBeenCalledWith(beforeArrow);
    act(() => handle.current!.hotkeyHandler(new KeyboardEvent('keydown', { key: 'Enter', keyCode: 13 })));
    await waitForClose();
    expect(control).toHaveFocus();
    expect(changed).toHaveBeenCalledExactlyOnceWith('two', expect.objectContaining({ value: 'two' }));
    expect(changes.mock.calls.map(([next]) => next)).toEqual([true, false]);
    const finalHandle = handle.current;
    rendered.unmount();
    expect(cleaned).toHaveBeenLastCalledWith(finalHandle);
    expect(handle.current).toBeNull();
    const objectRef = createRef<SelectHandle>();
    const second = render(<Choice ref={objectRef} />);
    expect(objectRef.current?.dom).toBe(screen.getByRole('combobox', { name: 'Choice' }));
    second.unmount();
    expect(objectRef.current).toBeNull();
  });

  it('does not restore or override a controlled close request that its owner rejects', async () => {
    const user = userEvent.setup();
    const changes = vi.fn();
    function HeldOpen() {
      const [open, setOpen] = useState(false);
      return (
        <Choice
          popupVisible={open}
          onVisibleChange={(next) => {
            changes(next);
            if (next) setOpen(true);
          }}
        />
      );
    }
    render(<HeldOpen />);
    const { control, menu } = await openMenu(user);
    const focus = vi.spyOn(control, 'focus');
    pressKey('Escape', 27);
    expect(control).toHaveAttribute('aria-expanded', 'true');
    expect(menu).toBeInTheDocument();
    expect(focus).not.toHaveBeenCalled();
    expect(changes.mock.calls.map(([next]) => next)).toEqual([true, false]);
  });

  it('observes an owner closing from onChange before the deferred visibility callback', async () => {
    const user = userEvent.setup();
    const changed = vi.fn();
    function CommitClose() {
      const [open, setOpen] = useState(false);
      return (
        <Choice
          popupVisible={open}
          onVisibleChange={setOpen}
          onChange={(value) => {
            changed(value);
            setOpen(false);
          }}
        />
      );
    }
    render(<CommitClose />);
    const { control, menu } = await openMenu(user);
    await user.click(within(menu).getByRole('option', { name: 'Two' }));
    await waitForClose();
    expect(control).toHaveFocus();
    expect(changed).toHaveBeenCalledExactlyOnceWith('two');
  });

  it.each(['outside click', 'Tab'] as const)('leaves focus with an external %s handoff', async (action) => {
    const user = userEvent.setup();
    render(
      <>
        <Choice />
        <button>Outside</button>
      </>
    );
    const { control } = await openMenu(user);
    const outside = screen.getByRole('button', { name: 'Outside' });
    if (action === 'Tab') {
      // Supply real browser legacy fields on this one event while user-event
      // performs the original Tab's default focus move from its actual target.
      control.addEventListener(
        'keydown',
        (event) => {
          Object.defineProperties(event, { keyCode: { value: 9 }, which: { value: 9 } });
        },
        { capture: true, once: true }
      );
      await user.tab();
    } else {
      await user.click(outside);
    }
    await waitForClose();
    expect(outside).toHaveFocus();
  });

  it.each(['disabled', 'replacement', 'unmount'] as const)('discards close intent on %s', async (action) => {
    const user = userEvent.setup();
    function Replaced() {
      const [closed, setClosed] = useState(false);
      if (closed && action === 'unmount') return <button>Replacement</button>;
      return (
        <Choice
          key={closed && action === 'replacement' ? 'new' : 'old'}
          disabled={closed && action === 'disabled'}
          onVisibleChange={(next) => {
            if (!next) setClosed(true);
          }}
        />
      );
    }
    render(<Replaced />);
    const { control } = await openMenu(user);
    const focus = vi.spyOn(control, 'focus');
    pressKey('Escape', 27);
    await waitForClose();
    expect(focus).not.toHaveBeenCalled();
    expect(control).not.toHaveFocus();
    if (action === 'disabled') expect(control).toHaveAttribute('aria-disabled', 'true');
    else expect(control.isConnected).toBe(false);
  });

  it.each(['search', 'multiple'] as const)(
    'returns %s focus through the original public input handle',
    async (mode) => {
      const user = userEvent.setup();
      const ref = createRef<SelectHandle>();
      const changed = vi.fn();
      render(
        <Choice
          ref={ref}
          {...(mode === 'multiple' ? { mode: 'multiple' as const, value: ['one'] } : { showSearch: true })}
          onChange={changed}
        />
      );
      await openMenu(user);
      const input = document.activeElement;
      expect(input?.tagName).toBe('INPUT');
      pressKey('Escape', 27);
      await waitForClose();
      expect(input).toHaveFocus();
      expect(ref.current?.dom).toContainElement(input as HTMLElement);
      expect(changed).not.toHaveBeenCalled();
    }
  );

  it.each(['focus trigger', 'onFocus owner'] as const)('does not reopen or compete with a %s', async (kind) => {
    const user = userEvent.setup();
    const visibility = vi.fn();
    function FocusOwned() {
      const [open, setOpen] = useState(false);
      return (
        <Choice
          popupVisible={open}
          {...(kind === 'focus trigger' ? { trigger: 'focus' as const } : { onFocus: () => setOpen(true) })}
          onVisibleChange={(next) => {
            visibility(next);
            setOpen(next);
          }}
        />
      );
    }
    render(<FocusOwned />);
    let control: HTMLElement;
    if (kind === 'onFocus owner') {
      control = screen.getByRole('combobox', { name: 'Choice' });
      act(() => control.focus());
      const menu = await screen.findByRole('listbox');
      await waitFor(() => expect(menu.closest('.arco-trigger')).not.toHaveStyle({ pointerEvents: 'none' }));
    } else {
      ({ control } = await openMenu(user));
    }
    pressKey('Escape', 27);
    await waitForClose();
    expect(control).not.toHaveFocus();
    expect(visibility.mock.calls.map(([next]) => next)).toEqual(kind === 'focus trigger' ? [true, false] : [false]);
  });
});
