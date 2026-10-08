import React from 'react';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import PreviewLoadingState from '@/renderer/components/media/PreviewLoadingState';
import { renderWithI18n } from '../i18nTestUtils';

describe('shared loading presentation', () => {
  it.each([Number.NaN, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY])(
    'keeps non-finite progress %s unknown',
    async (progress) => {
      await renderWithI18n(<PreviewLoadingState progress={progress} />, 'en-US');
      const status = screen.getByRole('status');
      expect(status).not.toHaveTextContent(/NaN|Infinity|%/);
      expect(status.querySelector('[style]')).toBeNull();
    }
  );

  it.each([
    { progress: -5, expected: 0 },
    { progress: 0, expected: 0 },
    { progress: 27.5, expected: 27.5 },
    { progress: 130, expected: 100 },
  ])('retains the established bounded $progress percent display', async ({ progress, expected }) => {
    await renderWithI18n(<PreviewLoadingState progress={progress} />, 'en-US');
    const status = screen.getByRole('status');
    expect(status).toHaveTextContent(`${expected}%`);
    expect(status.querySelector('[style]')).toHaveStyle({ width: `${expected}%` });
  });

  it('keeps the existing preview minimum and honors reduced motion for unknown work', async () => {
    await renderWithI18n(<PreviewLoadingState />, 'en-US');
    const status = screen.getByRole('status');
    expect(status).toHaveClass('min-h-120px');
    expect(status.querySelector('.animate-pulse')).toHaveClass('motion-reduce:animate-none');
  });
});
