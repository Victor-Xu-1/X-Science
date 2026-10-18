import React from 'react';
import { vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';
export { deferred, notebookFixture, submissions } from './manualNotebookControllerFixture';

const eventState = vi.hoisted(() => ({
  wakes: new Set<(event: { frame_id: string; root_frame_id: string }) => void>(),
}));
vi.mock('@/common', () => ({
  ipcBridge: {
    kernel: {
      executionCellUpdate: {
        on: (listener: (event: { frame_id: string; root_frame_id: string }) => void) => {
          eventState.wakes.add(listener);
          return () => eventState.wakes.delete(listener);
        },
      },
    },
    realtime: { reconnected: { on: () => () => {} } },
  },
}));
export const events = eventState;
import LiveNotebookPreview from '@/renderer/pages/conversation/Preview/components/notebook/LiveNotebookPreview';
export const navigation = {
  frameId: 'frame-1',
  rootFrameId: 'root-1',
  preferredKernelId: 'kernel-1',
  focusRequest: 'fixture-open',
};
export function notebookViews(count = 1) {
  return (
    <>
      {Array.from({ length: count }, (_, index) => (
        <LiveNotebookPreview key={index} navigation={navigation} />
      ))}
    </>
  );
}
export const renderNotebook = (count = 1) => renderWithI18n(notebookViews(count), 'en-US');
