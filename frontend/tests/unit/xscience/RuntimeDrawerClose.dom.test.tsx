import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React, { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import XScienceRuntimeDrawers from '@/renderer/components/xScience/runtime/XScienceRuntimeDrawers';
import { renderWithI18n, type TestLanguage } from '../i18nTestUtils';

afterEach(cleanup);

type Surface = 'plan' | 'review';

function Fixture({
  surface,
  onClose,
  onComplete,
  onRepair,
}: {
  surface: Surface;
  onClose: () => void;
  onComplete: (action: 'approve' | 'discard') => void;
  onRepair: () => void;
}) {
  const [visible, setVisible] = useState(false);
  const close = () => {
    onClose();
    setVisible(false);
  };
  return (
    <>
      <button type='button' onClick={() => setVisible(true)}>
        Open runtime view
      </button>
      <XScienceRuntimeDrawers
        mobile={false}
        reviewTitle='Fixture findings'
        reviewVisible={surface === 'review' && visible}
        reviewChecks={[]}
        reviewLoading={false}
        reviewError={null}
        reviewRepairing={false}
        onCloseReview={close}
        onRetryReview={vi.fn()}
        onRepairAndRegenerate={onRepair}
        planTitle='Fixture plan'
        planVisible={surface === 'plan' && visible}
        planDocument={{ version: 1, taskSummary: 'Immutable fixture plan', phases: [], feasibility: null }}
        planReference={{
          artifactId: 'fixture-plan',
          versionId: 'fixture-version',
          revisionNumber: 1,
          revisionCount: 1,
        }}
        planLoading={false}
        planError={null}
        planApprovalAvailable
        planAction={null}
        returnPlanLabel='Discard fixture'
        approvePlanLabel='Approve fixture'
        onClosePlan={close}
        onRetryPlan={vi.fn()}
        onCompletePlan={onComplete}
      />
    </>
  );
}

async function openFixture(surface: Surface, language: TestLanguage = 'en-US') {
  const callbacks = { onClose: vi.fn(), onComplete: vi.fn(), onRepair: vi.fn() };
  const user = userEvent.setup();
  await renderWithI18n(<Fixture surface={surface} {...callbacks} />, language);
  const opener = screen.getByRole('button', { name: 'Open runtime view' });
  await user.click(opener);
  return { user, opener, ...callbacks };
}

describe('C419 runtime drawer native close', () => {
  it.each([
    ['plan', 'en-US', 'Close'],
    ['plan', 'zh-CN', '关闭'],
    ['review', 'en-US', 'Close'],
    ['review', 'zh-CN', '关闭'],
  ] as const)('C419 semantic %s has a named Tab-reachable native close in %s', async (surface, language, label) => {
    const callbacks = await openFixture(surface, language);
    const close = await screen.findByRole('button', { name: label });
    expect(close.tagName).toBe('BUTTON');
    expect(close).toHaveAttribute('type', 'button');
    expect(close.tabIndex).toBe(0);
    expect(close.parentElement).not.toHaveAttribute('role', 'button');
    expect(close.querySelector('[aria-hidden="true"]')).not.toBeNull();
    await waitFor(() => expect(close).toHaveFocus());
    await callbacks.user.tab({ shift: true });
    await callbacks.user.tab();
    expect(close).toHaveFocus();
    expect(callbacks.onClose).not.toHaveBeenCalled();
    expect(callbacks.onComplete).not.toHaveBeenCalled();
    expect(callbacks.onRepair).not.toHaveBeenCalled();
  });

  it.each([
    ['plan', 'click'],
    ['plan', 'Enter'],
    ['plan', 'Space'],
    ['review', 'click'],
    ['review', 'Enter'],
    ['review', 'Space'],
  ] as const)('closes %s once through native %s without a domain action', async (surface, activation) => {
    const callbacks = await openFixture(surface);
    const close = await screen.findByRole('button', { name: 'Close' });
    await waitFor(() => expect(close).toHaveFocus());
    if (activation === 'click') await callbacks.user.click(close);
    else await callbacks.user.keyboard(activation === 'Enter' ? '{Enter}' : ' ');
    expect(callbacks.onClose).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Close' })).not.toBeInTheDocument());
    expect(callbacks.onComplete).not.toHaveBeenCalled();
    expect(callbacks.onRepair).not.toHaveBeenCalled();
    expect(callbacks.opener).toBeEnabled();
  });

  it.each(['plan', 'review'] as const)('retains the original Arco Escape owner for %s', async (surface) => {
    const callbacks = await openFixture(surface);
    const close = await screen.findByRole('button', { name: 'Close' });
    await waitFor(() => expect(close).toHaveFocus());
    act(() => fireEvent.keyDown(close, { key: 'Escape', keyCode: 27, which: 27 }));
    expect(callbacks.onClose).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Close' })).not.toBeInTheDocument());
    expect(callbacks.onComplete).not.toHaveBeenCalled();
  });
});
