import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import AccessibleContentDialog from '@/renderer/components/common/AccessibleContentDialog';

describe('AccessibleContentDialog', () => {
  afterEach(() => {
    cleanup();
    document.body.replaceChildren();
  });

  it('focuses its content, traps keyboard focus and restores the opener', () => {
    const opener = document.createElement('button');
    const firstRef = React.createRef<HTMLButtonElement>();
    document.body.append(opener);
    opener.focus();
    const view = render(
      <AccessibleContentDialog
        title='Files'
        visible
        closeLabel='Close'
        showCloseButton={false}
        initialFocusRef={firstRef}
        onClose={vi.fn()}
      >
        <button ref={firstRef}>First</button>
        <button>Last</button>
      </AccessibleContentDialog>
    );

    const first = screen.getByRole('button', { name: 'First' });
    const last = screen.getByRole('button', { name: 'Last' });
    expect(first).toHaveFocus();
    last.focus();
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Tab' });
    expect(first).toHaveFocus();

    view.rerender(
      <AccessibleContentDialog
        title='Files'
        visible={false}
        closeLabel='Close'
        showCloseButton={false}
        initialFocusRef={firstRef}
        onClose={vi.fn()}
      >
        <button>First</button>
      </AccessibleContentDialog>
    );
    expect(opener).toHaveFocus();
  });

  it('supports close controls while refusing dismissal during a busy operation', () => {
    const onClose = vi.fn();
    const view = render(
      <AccessibleContentDialog title='Files' visible closeLabel='Close' onClose={onClose}>
        Content
      </AccessibleContentDialog>
    );

    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(onClose).toHaveBeenCalledOnce();
    onClose.mockClear();
    view.rerender(
      <AccessibleContentDialog title='Files' visible closeLabel='Close' busy onClose={onClose}>
        Content
      </AccessibleContentDialog>
    );
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).not.toHaveBeenCalled();
  });

  it('includes links and disclosures but skips hidden, inert and disabled controls', () => {
    render(
      <AccessibleContentDialog title='References' visible closeLabel='Close' showCloseButton={false} onClose={vi.fn()}>
        <button hidden>Hidden</button>
        <button disabled>Disabled</button>
        <div inert>
          <button>Inert</button>
        </div>
        <div style={{ display: 'none' }}>
          <button>Not displayed</button>
        </div>
        <a href='#reference'>Reference</a>
        <details>
          <summary>Details</summary>
        </details>
      </AccessibleContentDialog>
    );
    const link = screen.getByRole('link', { name: 'Reference' });
    const summary = screen.getByText('Details');
    const dialog = screen.getByRole('dialog');
    expect(link).toHaveFocus();
    fireEvent.keyDown(dialog, { key: 'Tab', shiftKey: true });
    expect(summary).toHaveFocus();
    fireEvent.keyDown(dialog, { key: 'Tab' });
    expect(link).toHaveFocus();
  });

  it('keeps focus inside a content-only dialog with no actionable controls', () => {
    render(
      <AccessibleContentDialog title='Notice' visible closeLabel='Close' showCloseButton={false} onClose={vi.fn()}>
        Read-only content
      </AccessibleContentDialog>
    );
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveFocus();
    fireEvent.keyDown(dialog, { key: 'Tab' });
    expect(dialog).toHaveFocus();
  });
});
