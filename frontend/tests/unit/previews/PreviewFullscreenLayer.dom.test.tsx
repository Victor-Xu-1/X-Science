import { readFileSync } from 'node:fs';
import path from 'node:path';
import { cleanup, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import Modal from '@/renderer/components/base/WorkbenchModal';
import { PreviewFullscreenLayer } from '@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewFullscreenLayer';
import { renderWithI18n } from '../i18nTestUtils';

let lockStyle: HTMLStyleElement;
const styles = readFileSync(
  path.resolve('packages/desktop/src/renderer/pages/conversation/Preview/components/PreviewPanel/preview.css'),
  'utf8'
);

// Use the actual body rule only: jsdom does not parse the complete container-
// query sheet, while the real Modal retains its own inline scroll ownership.
beforeEach(() => {
  document.body.style.overflow = 'auto';
  lockStyle = document.createElement('style');
  lockStyle.textContent = styles.match(/html body\.workbench-preview-scroll-lock\s*\{[^}]*\}/)?.[0] ?? '';
  document.head.append(lockStyle);
});
afterEach(() => {
  cleanup();
  lockStyle.remove();
  document.body.style.overflow = '';
});

const bodyContainer = () => document.body;
function Scenario({ fullscreen, modal }: { fullscreen: boolean; modal: boolean }) {
  return (
    <>
      <PreviewFullscreenLayer active={fullscreen} label='Test file' onExit={() => {}}>
        <button type='button'>File action</button>
      </PreviewFullscreenLayer>
      <Modal visible={modal} title='Inspector' getPopupContainer={bodyContainer} onCancel={() => {}}>
        <p>Read-only details</p>
      </Modal>
    </>
  );
}

describe('fullscreen and real dialog scroll ownership', () => {
  it('retains fullscreen locking when an earlier dialog closes, without restoring its obsolete inline lock later', async () => {
    const view = await renderWithI18n(<Scenario fullscreen={false} modal />);
    await waitFor(() => expect(document.body.style.overflow).toBe('hidden'));
    view.rerender(<Scenario fullscreen modal />);
    view.rerender(<Scenario fullscreen modal={false} />);
    await waitFor(() => expect(document.body.style.overflow).toBe('auto'));
    expect(getComputedStyle(document.body).overflow).toBe('hidden');
    view.rerender(<Scenario fullscreen={false} modal={false} />);
    expect(getComputedStyle(document.body).overflow).toBe('auto');
    expect(document.body).not.toHaveClass('workbench-preview-scroll-lock');
  });

  it('leaves a later dialog in control when fullscreen exits before it', async () => {
    const view = await renderWithI18n(<Scenario fullscreen modal={false} />);
    expect(getComputedStyle(document.body).overflow).toBe('hidden');
    view.rerender(<Scenario fullscreen modal />);
    await waitFor(() => expect(screen.getByRole('dialog', { name: 'Inspector' })).toBeInTheDocument());
    view.rerender(<Scenario fullscreen={false} modal />);
    expect(getComputedStyle(document.body).overflow).toBe('hidden');
    view.rerender(<Scenario fullscreen={false} modal={false} />);
    await waitFor(() => expect(getComputedStyle(document.body).overflow).toBe('auto'));
    expect(document.body).not.toHaveClass('workbench-preview-scroll-lock');
  });
});
