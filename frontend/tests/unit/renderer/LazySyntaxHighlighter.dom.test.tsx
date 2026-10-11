/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { cleanup, render, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/renderer/components/Markdown/CodeHighlighter', () => ({
  default: ({ children }: { children?: React.ReactNode }) => <pre data-testid='syntax-highlighter'>{children}</pre>,
}));

import LazySyntaxHighlighter from '@/renderer/components/Markdown/LazySyntaxHighlighter';

afterEach(cleanup);

describe('LazySyntaxHighlighter', () => {
  it('shows the same code immediately, then swaps in the real highlighter', async () => {
    const { container } = render(
      <LazySyntaxHighlighter language='python' PreTag='div'>
        {'print("hello")'}
      </LazySyntaxHighlighter>
    );

    expect(container.textContent).toContain('print("hello")');

    await waitFor(() => {
      expect(container.querySelector('[data-testid="syntax-highlighter"]')).toHaveTextContent('print("hello")');
    });
  });

  it('reuses the cached highlighter on a later mount without treating it as an initializer', async () => {
    const first = render(
      <LazySyntaxHighlighter language='python' PreTag='div'>
        {'print("first")'}
      </LazySyntaxHighlighter>
    );

    await waitFor(() => {
      expect(first.container.querySelector('[data-testid="syntax-highlighter"]')).toHaveTextContent('print("first")');
    });

    const second = render(
      <LazySyntaxHighlighter language='javascript' PreTag='div'>
        {'console.log("second")'}
      </LazySyntaxHighlighter>
    );

    expect(second.container.querySelector('[data-testid="syntax-highlighter"]')).toHaveTextContent(
      'console.log("second")'
    );
  });
});
