import React from 'react';
import { act, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';
import ConversationTurnRail from '@/renderer/pages/conversation/components/ConversationTitleMinimap/ConversationTurnRail';
import {
  isNearMessageBoundary,
  navigateVirtualMessage,
} from '@/renderer/pages/conversation/Messages/virtualMessageNavigation';
import type { TurnPreviewItem } from '@/renderer/pages/conversation/components/ConversationTitleMinimap/minimapTypes';
import type { VirtuosoHandle } from 'react-virtuoso';

vi.mock('@/renderer/pages/conversation/components/ConversationTitleMinimap/useTurnRailGeometry', () => ({
  useTurnRailGeometry: () => ({ left: 20, top: 300, rtl: false, progress: 0, visible: [0] }),
}));
const item: TurnPreviewItem = {
  index: 1,
  messageId: 'q1',
  question: 'Question',
  questionRaw: 'Question',
  answer: 'Answer',
  answerRaw: 'Answer',
  messageIds: ['q1', 'a1'],
};
afterEach(() => vi.useRealTimers());
describe('turn rail interactions', () => {
  it('does not mistake virtual overscan for the actual next-page boundary', () => {
    const viewport = document.createElement('div');
    Object.defineProperties(viewport, { clientHeight: { value: 600 }, scrollHeight: { value: 1600 } });
    expect(isNearMessageBoundary(viewport, 'end')).toBe(false);
    viewport.scrollTop = 900;
    expect(isNearMessageBoundary(viewport, 'end')).toBe(true);
    expect(isNearMessageBoundary(viewport, 'start')).toBe(false);
    viewport.scrollTop = 0;
    expect(isNearMessageBoundary(viewport, 'start')).toBe(true);
    expect(isNearMessageBoundary(null, 'end')).toBe(false);
  });
  it('shows a single turn, previews on focus and delegates only one jump', async () => {
    const jump = vi.fn();
    await renderWithI18n(<ConversationTurnRail items={[item]} viewport={null} onJump={jump} />);
    const button = screen.getByRole('button', { name: /Question/ });
    fireEvent.focus(button);
    expect(screen.getByRole('tooltip')).toHaveTextContent('Answer');
    expect(button).toHaveAttribute('aria-current', 'location');
    fireEvent.click(button);
    expect(jump).toHaveBeenCalledExactlyOnceWith('q1');
    expect(screen.getByTestId('conversation-turn-preview')).toHaveAttribute('aria-hidden', 'true');
  });
  it('preserves the preview during its120ms pointer bridge and closes on Escape', async () => {
    await renderWithI18n(<ConversationTurnRail items={[item]} viewport={null} onJump={() => undefined} />);
    vi.useFakeTimers();
    const button = screen.getByRole('button', { name: /Question/ });
    fireEvent.pointerEnter(button, { pointerType: 'mouse' });
    fireEvent.pointerLeave(button, { pointerType: 'mouse' });
    act(() => vi.advanceTimersByTime(119));
    expect(screen.getByTestId('conversation-turn-preview')).toHaveAttribute('data-open', 'true');
    act(() => vi.advanceTimersByTime(1));
    expect(screen.getByTestId('conversation-turn-preview')).toHaveAttribute('data-open', 'false');
    fireEvent.focus(button);
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.getByTestId('conversation-turn-preview')).toHaveAttribute('data-open', 'false');
  });
  it('does not create a navigation line for an empty conversation', async () => {
    await renderWithI18n(<ConversationTurnRail items={[]} viewport={null} onJump={() => undefined} />);
    expect(screen.queryByRole('navigation')).toBeNull();
  });
  it('uses measured seeking for unmounted rows and smooth motion only for nearby rendered rows', () => {
    const scrollToIndex = vi.fn();
    const list = { scrollToIndex } as unknown as VirtuosoHandle;
    const viewport = document.createElement('div');
    Object.defineProperty(viewport, 'clientHeight', { value: 500 });
    navigateVirtualMessage(list, viewport, 'q1', 99, { behavior: 'smooth' });
    expect(scrollToIndex).toHaveBeenLastCalledWith({ index: 99, align: 'start', behavior: 'auto' });
    const row = document.createElement('div');
    row.dataset.sourceMessageId = 'q1';
    viewport.appendChild(row);
    navigateVirtualMessage(list, viewport, 'q1', 99, { behavior: 'smooth', block: 'center' });
    expect(scrollToIndex).toHaveBeenLastCalledWith({ index: 99, align: 'center', behavior: 'smooth' });
  });
});
