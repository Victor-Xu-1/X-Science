import { cleanup, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import XSciencePlanReviewContent from '@/renderer/components/xScience/runtime/XSciencePlanReviewContent';
import type {
  XSciencePlanDocument,
  XSciencePlanReference,
} from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import { renderWithI18n } from '../i18nTestUtils';

const document: XSciencePlanDocument = {
  version: 3,
  taskSummary: 'Analyze a public single-cell dataset.',
  feasibility: {
    confidence: 'medium',
    rationale: 'The data is public and the workflow is reproducible.',
  },
  phases: [
    {
      id: 'phase-1',
      name: 'Data acquisition',
      steps: [],
      delegations: [
        {
          id: 'delegation-1',
          name: 'Acquire data',
          agentName: 'Research agent',
          steps: [
            {
              id: 'step-1',
              title: 'Download metadata',
              description: 'Fetch and validate the public metadata file.',
            },
          ],
        },
      ],
    },
  ],
};

const reference: XSciencePlanReference = {
  artifactId: 'plan-current',
  versionId: 'plan-current-v1',
  revisionNumber: 2,
  revisionCount: 2,
  contentVersion: 1,
  generatedAt: '2026-08-27T09:52:00.000Z',
};

describe('XSciencePlanReviewContent', () => {
  afterEach(() => cleanup());

  it('uses the shared lightweight plan surface without unsafe border utilities', async () => {
    await renderWithI18n(
      <XSciencePlanReviewContent
        document={document}
        reference={reference}
        loading={false}
        error={null}
        onRetry={vi.fn()}
      />,
      'en-US'
    );

    const plan = screen.getByTestId('x-science-plan-review');
    const phase = screen.getByTestId('x-science-plan-phase');
    const delegation = screen.getByTestId('x-science-plan-delegation');

    expect(plan).toHaveClass('x-science-plan-review');
    expect(screen.getByText('Current execution plan')).toBeInTheDocument();
    expect(screen.getByText('Revision 2 of 2')).toBeInTheDocument();
    expect(screen.queryByText(/Plan version/)).not.toBeInTheDocument();
    expect(phase).toHaveClass('x-science-plan-review__phase');
    expect(delegation).toHaveClass('x-science-plan-review__delegation');

    for (const element of [phase, delegation]) {
      expect(element.className).not.toMatch(/(?:^|\s)border-solid(?:\s|$)/);
      expect(element.className).not.toMatch(/(?:^|\s)border-l-2(?:\s|$)/);
    }
  });

  it('C397 pinned labels an English historical preview and makes opening current explicit', async () => {
    const open = vi.fn();
    await renderWithI18n(
      <XSciencePlanReviewContent
        document={document}
        reference={reference}
        loading={false}
        error={null}
        onRetry={vi.fn()}
        replaced
        onOpenCurrent={open}
      />,
      'en-US'
    );
    expect(screen.getByText('Historical execution plan')).toBeInTheDocument();
    expect(screen.getByText('This plan has been replaced and is read-only.')).toBeInTheDocument();
    expect(screen.queryByText('Current execution plan')).not.toBeInTheDocument();
    screen.getByRole('button', { name: 'View current plan' }).click();
    expect(open).toHaveBeenCalledOnce();
  });
});
