/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { cleanup, render, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';

const mermaidRender = vi.hoisted(() => vi.fn(async () => ({ svg: '<svg data-testid="rendered-mermaid"></svg>' })));
const mermaidInitialize = vi.hoisted(() => vi.fn());

vi.mock('mermaid', () => ({
  default: {
    initialize: mermaidInitialize,
    render: mermaidRender,
  },
}));

vi.mock('@/renderer/utils/ui/clipboard', () => ({
  copyText: vi.fn().mockResolvedValue(undefined),
}));

vi.mock('@/renderer/pages/conversation/Preview/context/PreviewContext', () => ({
  usePreviewContext: () => ({ openPreview: vi.fn() }),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}));

vi.mock('@arco-design/web-react', () => ({
  Message: { success: vi.fn(), error: vi.fn() },
}));

vi.mock('@icon-park/react', () => ({
  Copy: () => <span data-testid='copy-icon' />,
  PreviewOpen: () => <span data-testid='preview-icon' />,
}));

vi.mock('@/renderer/components/Markdown/LazySyntaxHighlighter', () => ({
  default: ({ children }: { children?: React.ReactNode }) => <pre data-testid='mermaid-source'>{children}</pre>,
}));

import MermaidBlock from '@/renderer/components/Markdown/MermaidBlock';

afterEach(cleanup);

describe('MermaidBlock', () => {
  it('loads mermaid on demand and still renders the diagram', async () => {
    render(<MermaidBlock code={'graph TD\n  A-->B'} />);

    await waitFor(() => {
      expect(mermaidInitialize).toHaveBeenCalled();
      expect(mermaidRender).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(document.querySelector('[data-testid="mermaid-diagram"]')).toBeTruthy();
    });
  });
});
