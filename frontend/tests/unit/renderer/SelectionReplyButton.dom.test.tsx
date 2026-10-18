import type { TMessage } from '@/common/chat/chatLib';
import SelectionReplyButton from '@/renderer/pages/conversation/Messages/components/SelectionReplyButton';
import { act, fireEvent, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

const { emit, layout } = vi.hoisted(() => ({ emit: vi.fn(), layout: { isMobile: false } }));
vi.mock('@/renderer/utils/emitter', () => ({ emitter: { emit } }));
vi.mock('@/renderer/hooks/context/LayoutContext', () => ({ useLayoutContext: () => layout }));

const messages = [{ id: 'reply-source', position: 'left' }] as TMessage[];
function View({ items = messages }: { items?: TMessage[] }) {
  return (
    <>
      <div id='message-reply-source'>
        <p data-testid='source-text'>A selected passage.</p>
      </div>
      <SelectionReplyButton messages={items} />
    </>
  );
}
function selectPassage() {
  const source = screen.getByTestId('source-text');
  const range = document.createRange();
  range.selectNodeContents(source);
  Object.defineProperty(range, 'getBoundingClientRect', {
    value: () => ({ top: 100, bottom: 120, left: 50, width: 100 }),
  });
  const selection = window.getSelection()!;
  selection.removeAllRanges();
  selection.addRange(range);
  return source;
}
async function openSelection() {
  fireEvent.mouseUp(selectPassage());
  return screen.findByRole('button');
}
afterEach(() => {
  emit.mockClear();
  layout.isMobile = false;
  window.getSelection()?.removeAllRanges();
});

describe('Selection reply control', () => {
  it('preserves selection on pointer down and publishes once on native activation', async () => {
    await renderWithI18n(<View />);
    const button = await openSelection();
    expect(button).toHaveAttribute('type', 'button');
    fireEvent.mouseDown(button);
    expect(emit).not.toHaveBeenCalled();
    expect(window.getSelection()?.toString()).toBe('A selected passage.');
    fireEvent.click(button);
    expect(emit).toHaveBeenCalledExactlyOnceWith('sendbox.reply', {
      messageId: 'reply-source',
      content: 'A selected passage.',
      position: 'left',
    });
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(window.getSelection()?.rangeCount).toBe(0);
  });

  it.each(['{Enter}', ' '])('supports native keyboard activation with %s', async (key) => {
    await renderWithI18n(<View />);
    const button = await openSelection();
    button.focus();
    await userEvent.setup().keyboard(key);
    expect(emit).toHaveBeenCalledTimes(1);
    expect(emit).toHaveBeenLastCalledWith('sendbox.reply', expect.objectContaining({ messageId: 'reply-source' }));
  });

  it('shows keyboard selections and dismisses with Escape without discarding the selected text', async () => {
    await renderWithI18n(<View />);
    fireEvent.keyUp(selectPassage(), { key: 'Shift' });
    await screen.findByRole('button');
    fireEvent.keyUp(document, { key: 'Escape' });
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(window.getSelection()?.toString()).toBe('A selected passage.');
    expect(emit).not.toHaveBeenCalled();
  });

  it('does not publish a quote after its owning message is no longer present', async () => {
    const { rerender } = await renderWithI18n(<View />);
    const button = await openSelection();
    rerender(<View items={[]} />);
    fireEvent.click(button);
    expect(emit).not.toHaveBeenCalled();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('keeps native mobile text selection and clears outside interactions without a reply', async () => {
    const { rerender } = await renderWithI18n(<View />);
    await openSelection();
    fireEvent.mouseDown(document.body);
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    layout.isMobile = true;
    rerender(<View />);
    fireEvent.mouseUp(selectPassage());
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 30));
    });
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(emit).not.toHaveBeenCalled();
  });
});
