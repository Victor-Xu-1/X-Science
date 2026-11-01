/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import React from 'react';
import type { i18n } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import MarkdownView from '@/renderer/components/Markdown';
import { mergeArtifactReferences } from '@/common/chat/chatLib';
import type { ArtifactReferenceWire } from '@/common/adapter/messageStreamProtocol';
import { useXScienceArtifactResolver } from '@/renderer/pages/conversation/Messages/useXScienceArtifactLinkPreview';
import { createTestI18n } from '../i18nTestUtils';

const copyTextMock = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));
const openExternalUrlMock = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));
const artifactMocks = vi.hoisted(() => ({
  list: vi.fn(),
  window: vi.fn(),
  preview: vi.fn(),
  index: { byArtifactId: new Map(), byVersionId: new Map(), byFilename: new Map() },
}));

vi.mock('@/common', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/common')>();
  return {
    ...actual,
    ipcBridge: {
      ...actual.ipcBridge,
      conversation: {
        ...actual.ipcBridge.conversation,
        listArtifacts: { ...actual.ipcBridge.conversation.listArtifacts, invoke: artifactMocks.list },
      },
    },
  };
});
vi.mock('@/renderer/pages/conversation/Messages/artifacts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/renderer/pages/conversation/Messages/artifacts')>();
  return {
    ...actual,
    useConversationArtifactIndex: () => artifactMocks.index,
    useResolveConversationArtifactWindow: () => artifactMocks.window,
  };
});
vi.mock('@/renderer/pages/conversation/Preview/context/PreviewContext', () => ({
  usePreviewContext: () => ({ openPreview: artifactMocks.preview }),
}));

vi.mock('@/renderer/components/Markdown/ShadowView', () => ({
  __esModule: true,
  default: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
}));

vi.mock('@/renderer/components/Markdown/CodeBlock', () => ({
  __esModule: true,
  default: ({ children }: { children?: React.ReactNode }) => <code>{children}</code>,
}));

vi.mock('@/renderer/components/media/LocalImageView', () => ({
  __esModule: true,
  default: ({ src, alt }: { src: string; alt: string }) => <img src={src} alt={alt} />,
}));

vi.mock('@/renderer/utils/chat/latexDelimiters', () => ({
  convertLatexDelimiters: (text: string) => text,
}));

vi.mock('@/renderer/utils/platform', () => ({
  openExternalUrl: openExternalUrlMock,
}));

vi.mock('@/renderer/utils/ui/clipboard', () => ({
  copyText: copyTextMock,
}));

vi.mock('@arco-design/web-react', () => ({
  Button: ({
    children,
    icon,
    ...props
  }: React.ButtonHTMLAttributes<HTMLButtonElement> & { icon?: React.ReactNode }) => (
    <button type='button' {...props}>
      {icon}
      {children}
    </button>
  ),
  Message: {
    error: vi.fn(),
  },
  Tooltip: ({ children }: { children?: React.ReactNode }) => <>{children}</>,
}));

vi.mock('@icon-park/react', () => ({
  Copy: () => <span data-testid='copy-icon' />,
}));

let testI18n: i18n;

const renderWithProviders = (ui: React.ReactElement) => render(<I18nextProvider i18n={testI18n}>{ui}</I18nextProvider>);

const resolutionReference: ArtifactReferenceWire = {
  artifact_id: 'artifact-report',
  version_id: 'version-report',
  relation: 'produced',
  filename: 'report.json',
  availability: 'available',
};
const resolvedCollections = () => [
  {
    kind: 'scientific_files',
    payload: {
      files: [
        {
          ...resolutionReference,
          version_number: 1,
          content_url: '/api/artifacts/artifact-report/versions/version-report',
          content_type: 'application/json',
          preview_kind: 'json',
        },
      ],
    },
  },
];
const StreamingArtifactLink = ({
  references,
  conversationId = 'frame-report',
  workspace = 'x-science://project-report',
}: {
  references: ArtifactReferenceWire[];
  conversationId?: string;
  workspace?: string;
}) => {
  // The real stream producer deliberately returns fresh arrays and objects.
  const artifactReferences = mergeArtifactReferences(undefined, references, 'replace');
  const resolver = useXScienceArtifactResolver({ conversationId, workspace, artifactReferences });
  return (
    <MarkdownView onLink={resolver.handleLink} resolveLinkHref={resolver.resolveLinkHref}>
      {'[Report](report.json)'}
    </MarkdownView>
  );
};

describe('MarkdownView local file links', () => {
  beforeAll(async () => {
    testI18n = await createTestI18n('en-US');
  });

  beforeEach(() => {
    copyTextMock.mockClear();
    openExternalUrlMock.mockClear();
    artifactMocks.list.mockReset();
    artifactMocks.list.mockResolvedValue([]);
    artifactMocks.window.mockReset();
    artifactMocks.window.mockResolvedValue(null);
    artifactMocks.index = { byArtifactId: new Map(), byVersionId: new Map(), byFilename: new Map() };
  });

  afterEach(() => vi.unstubAllGlobals());

  it('renders local file links as app controls instead of browser anchors', () => {
    const onLocalFileLink = vi.fn();

    renderWithProviders(
      <MarkdownView onLocalFileLink={onLocalFileLink}>
        {'[report.xlsx](/C:/Users/Administrator/AppData/Roaming/XScience/report.xlsx)'}
      </MarkdownView>
    );

    expect(screen.queryByRole('link')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'report.xlsx' }));
    expect(onLocalFileLink).toHaveBeenCalledWith(
      'C:/Users/Administrator/AppData/Roaming/XScience/report.xlsx',
      expect.objectContaining({
        filePath: 'C:/Users/Administrator/AppData/Roaming/XScience/report.xlsx',
      })
    );

    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    expect(copyTextMock).toHaveBeenCalledWith('C:/Users/Administrator/AppData/Roaming/XScience/report.xlsx');
  });

  it('renders line references as file chips and copies the full reference', () => {
    const onLocalFileLink = vi.fn();

    renderWithProviders(
      <MarkdownView onLocalFileLink={onLocalFileLink}>
        {'[2026-06-19.log](C:/Users/Administrator/AppData/Roaming/XScience/logs/2026-06-19.log:1421)'}
      </MarkdownView>
    );

    const fileButton = screen.getByRole('button', { name: /2026-06-19\.log\s+L1421/ });
    fireEvent.click(fileButton);

    expect(onLocalFileLink).toHaveBeenCalledWith(
      'C:/Users/Administrator/AppData/Roaming/XScience/logs/2026-06-19.log',
      expect.objectContaining({
        filePath: 'C:/Users/Administrator/AppData/Roaming/XScience/logs/2026-06-19.log',
        rawReference: 'C:/Users/Administrator/AppData/Roaming/XScience/logs/2026-06-19.log:1421',
        line: 1421,
      })
    );

    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    expect(copyTextMock).toHaveBeenCalledWith(
      'C:/Users/Administrator/AppData/Roaming/XScience/logs/2026-06-19.log:1421'
    );
  });

  it('renders line and column references as file chips and copies the full reference', () => {
    const onLocalFileLink = vi.fn();

    renderWithProviders(
      <MarkdownView onLocalFileLink={onLocalFileLink}>
        {'[app.log](C:/Users/Administrator/AppData/Roaming/XScience/logs/app.log:1421:7)'}
      </MarkdownView>
    );

    const fileButton = screen.getByRole('button', { name: /app\.log\s+L1421:7/ });
    fireEvent.click(fileButton);

    expect(onLocalFileLink).toHaveBeenCalledWith(
      'C:/Users/Administrator/AppData/Roaming/XScience/logs/app.log',
      expect.objectContaining({
        filePath: 'C:/Users/Administrator/AppData/Roaming/XScience/logs/app.log',
        rawReference: 'C:/Users/Administrator/AppData/Roaming/XScience/logs/app.log:1421:7',
        line: 1421,
        column: 7,
      })
    );

    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    expect(copyTextMock).toHaveBeenCalledWith('C:/Users/Administrator/AppData/Roaming/XScience/logs/app.log:1421:7');
  });

  it('renders hash range references as file chips and copies normalized local references', () => {
    const onLocalFileLink = vi.fn();

    renderWithProviders(
      <MarkdownView onLocalFileLink={onLocalFileLink}>
        {'[user.js 1-260行](/Users/demo/project/user.js#L1-L260)'}
      </MarkdownView>
    );

    expect(screen.queryByRole('link', { name: /user\.js/ })).not.toBeInTheDocument();

    const fileButton = screen.getByRole('button', { name: /user\.js 1-260行\s+L1-L260/ });
    fireEvent.click(fileButton);

    expect(onLocalFileLink).toHaveBeenCalledWith(
      '/Users/demo/project/user.js',
      expect.objectContaining({
        filePath: '/Users/demo/project/user.js',
        rawReference: '/Users/demo/project/user.js#L1-L260',
        line: 1,
        endLine: 260,
      })
    );

    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    expect(copyTextMock).toHaveBeenCalledWith('/Users/demo/project/user.js#L1-L260');
  });

  it('does not render a no-op open button when no local file handler is provided', () => {
    renderWithProviders(
      <MarkdownView>{'[report.xlsx](/C:/Users/Administrator/AppData/Roaming/XScience/report.xlsx)'}</MarkdownView>
    );

    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'report.xlsx' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    expect(copyTextMock).toHaveBeenCalledWith('C:/Users/Administrator/AppData/Roaming/XScience/report.xlsx');
  });

  it('lets a conversation handler intercept a relative artifact link before external navigation', async () => {
    const onLink = vi.fn().mockResolvedValue(true);
    renderWithProviders(<MarkdownView onLink={onLink}>{'[README](README.md)'}</MarkdownView>);

    fireEvent.click(screen.getByRole('link', { name: 'README' }));

    await waitFor(() => {
      expect(onLink).toHaveBeenCalledWith('README.md');
    });
    expect(openExternalUrlMock).not.toHaveBeenCalled();
  });

  it.each([
    '/api/artifacts/artifact-report/versions/version-report',
    'https://x-science.bio/api/artifacts/artifact-report/versions/version-report',
    '#/artifacts/artifact-report',
    '/#/artifacts/artifact-report',
    'http://127.0.0.1:8765/#/artifacts/artifact-report',
    '%7B%7Bartifact%3Aversion-report%7D%7D',
    './report.pdf',
  ])('marks generated file links consistently and contains unresolved clicks: %s', async (href) => {
    const onLink = vi.fn().mockResolvedValue(false);
    const resolveLinkHref = vi.fn().mockResolvedValue(null);
    renderWithProviders(
      <MarkdownView onLink={onLink} resolveLinkHref={resolveLinkHref}>{`[**Report.pdf**](${href})`}</MarkdownView>
    );
    const link = screen.getByRole('link', { name: 'Report.pdf' });
    expect(link).toHaveClass('markdown-artifact-file-link');
    expect(link).not.toHaveAttribute('target', '_blank');
    fireEvent.click(link);
    await waitFor(() => expect(onLink).toHaveBeenCalledWith(href));
    expect(openExternalUrlMock).not.toHaveBeenCalled();
    await waitFor(() => expect(link).toHaveAttribute('data-artifact-link-resolution', 'unavailable'));
  });

  it('does not give normal external web links the generated-file treatment', () => {
    renderWithProviders(<MarkdownView>{'[Docs](https://example.test/report.pdf)'}</MarkdownView>);
    expect(screen.getByRole('link', { name: 'Docs' })).not.toHaveClass('markdown-artifact-file-link');
  });

  it.each([
    {
      language: 'en-US' as const,
      unavailable: 'File reference unavailable',
      hint: 'Unable to resolve this file reference. The original link is preserved.',
    },
    {
      language: 'zh-CN' as const,
      unavailable: '文件引用不可用',
      hint: '无法解析此文件引用，已保留原始链接。',
    },
  ])('makes an unavailable exact artifact reference persistently understandable in $language', async (labels) => {
    const href = '/api/artifacts/artifact-unknown/versions/version-known';
    const onLink = vi.fn().mockResolvedValue(true);
    const i18n = await createTestI18n(labels.language);
    render(
      <I18nextProvider i18n={i18n}>
        <MarkdownView onLink={onLink} resolveLinkHref={async () => null}>
          {`[Saved report](${href})`}
        </MarkdownView>
      </I18nextProvider>
    );
    const link = screen.getByRole('link', { name: 'Saved report' });
    await waitFor(() => expect(link).toHaveAttribute('data-artifact-link-resolution', 'unavailable'));
    expect(link).toHaveAttribute('aria-disabled', 'true');
    expect(link).toHaveAttribute('title', labels.hint);
    const status = screen.getByRole('status');
    expect(status).toHaveTextContent(labels.unavailable);
    expect(link).toHaveAttribute('aria-describedby', status.id);
    expect(status.textContent).not.toContain('artifact-unknown');
    fireEvent.click(link);
    await waitFor(() => expect(onLink).toHaveBeenCalledWith(href));
    expect(link).toHaveAttribute('data-original-href', href);
    expect(status).toHaveTextContent(labels.unavailable);
    expect(openExternalUrlMock).not.toHaveBeenCalled();
  });

  it('keeps artifact resolution state scoped to the current resolver and retires late prior-scope responses', async () => {
    const href = '/api/artifacts/artifact-report/versions/version-report';
    const resolved = '/api/artifacts/artifact-report/versions/version-authorized';
    let finishOld!: (value: string | null) => void;
    let finishNext!: (value: string | null) => void;
    const oldResolver = vi.fn(() => new Promise<string | null>((resolve) => (finishOld = resolve)));
    const currentResolver = vi.fn(async () => resolved);
    const nextResolver = vi.fn(() => new Promise<string | null>((resolve) => (finishNext = resolve)));
    const onLink = vi.fn().mockResolvedValue(true);
    const view = (resolve: (href: string) => Promise<string | null>) => (
      <I18nextProvider i18n={testI18n}>
        <MarkdownView onLink={onLink} resolveLinkHref={resolve}>
          {`[Report](${href})`}
        </MarkdownView>
      </I18nextProvider>
    );
    const rendered = render(view(oldResolver));
    const link = screen.getByRole('link', { name: 'Report' });
    expect(link).toHaveAttribute('aria-busy', 'true');
    rendered.rerender(view(currentResolver));
    await waitFor(() => expect(link).toHaveAttribute('href', resolved));
    await act(async () => finishOld(null));
    expect(link).toHaveAttribute('href', resolved);
    expect(link).not.toHaveAttribute('aria-disabled');
    expect(screen.queryByRole('status')).toBeNull();
    rendered.rerender(view(nextResolver));
    expect(link).toHaveAttribute('data-artifact-link-resolution', 'pending');
    expect(screen.getByRole('status')).toHaveTextContent('Resolving file…');
    expect(link).not.toHaveAttribute('href', resolved);
    await act(async () => finishNext(null));
    await waitFor(() => expect(link).toHaveAttribute('aria-disabled', 'true'));
    expect(screen.getByRole('status')).toHaveTextContent('File reference unavailable');
  });

  it('retires an old artifact href response while preserving the new exact click identity', async () => {
    const oldHref = '/api/artifacts/old-artifact/versions/old-version';
    const newHref = '/api/artifacts/new-artifact/versions/new-version';
    let finishOld!: (value: string | null) => void;
    const resolve = vi.fn((href: string) =>
      href === oldHref ? new Promise<string | null>((finish) => (finishOld = finish)) : Promise.resolve(newHref)
    );
    const onLink = vi.fn().mockResolvedValue(true);
    const view = (href: string) => (
      <I18nextProvider i18n={testI18n}>
        <MarkdownView onLink={onLink} resolveLinkHref={resolve}>
          {`[Report](${href})`}
        </MarkdownView>
      </I18nextProvider>
    );
    const rendered = render(view(oldHref));
    rendered.rerender(view(newHref));
    const link = screen.getByRole('link', { name: 'Report' });
    await waitFor(() => expect(link).toHaveAttribute('href', newHref));
    await act(async () => finishOld(null));
    expect(link).toHaveAttribute('href', newHref);
    fireEvent.click(link);
    await waitFor(() => expect(onLink).toHaveBeenCalledExactlyOnceWith(newHref));
    expect(openExternalUrlMock).not.toHaveBeenCalled();
  });

  it.each([true, false])(
    'keeps equivalent stream reference arrays stable without repeated resolution: available=%s',
    async (available) => {
      artifactMocks.list.mockResolvedValue(available ? resolvedCollections() : []);
      const view = (references: ArtifactReferenceWire[]) => (
        <I18nextProvider i18n={testI18n}>
          <StreamingArtifactLink references={references} />
        </I18nextProvider>
      );
      const rendered = render(view([resolutionReference]));
      const link = screen.getByRole('link', { name: 'Report' });
      const state = available ? 'resolved' : 'unavailable';
      await waitFor(() => expect(link).toHaveAttribute('data-artifact-link-resolution', state));
      for (let chunk = 1; chunk <= 3; chunk++) {
        rendered.rerender(view([{ ...resolutionReference, attempt: chunk, checksum: `diagnostic-${chunk}` }]));
        expect(link).toHaveAttribute('data-artifact-link-resolution', state);
        await act(async () => undefined);
      }
      expect(artifactMocks.list).toHaveBeenCalledTimes(1);
      expect(openExternalUrlMock).not.toHaveBeenCalled();
    }
  );

  it.each(['conversation', 'workspace', 'account-index', 'references'] as const)(
    'keeps real artifact resolution scope changes isolated from late prior metadata: %s',
    async (change) => {
      let finishOld!: (value: ReturnType<typeof resolvedCollections>) => void;
      artifactMocks.list.mockReturnValueOnce(new Promise((resolve) => (finishOld = resolve)));
      artifactMocks.list.mockResolvedValue([]);
      const view = (next = false) => (
        <I18nextProvider i18n={testI18n}>
          <StreamingArtifactLink
            references={[
              next && change === 'references'
                ? { ...resolutionReference, version_id: 'version-new' }
                : { ...resolutionReference },
            ]}
            conversationId={next && change === 'conversation' ? 'frame-new' : 'frame-report'}
            workspace={next && change === 'workspace' ? 'x-science://project-new' : 'x-science://project-report'}
          />
        </I18nextProvider>
      );
      const rendered = render(view());
      const link = screen.getByRole('link', { name: 'Report' });
      await waitFor(() => expect(artifactMocks.list).toHaveBeenCalledTimes(1));
      if (change === 'account-index') {
        // The real provider clears its window and index in useLayoutEffect
        // when owner/conversation baseSnapshotKey changes.
        artifactMocks.index = { byArtifactId: new Map(), byVersionId: new Map(), byFilename: new Map() };
      }
      rendered.rerender(view(true));
      await waitFor(() => expect(link).toHaveAttribute('data-artifact-link-resolution', 'unavailable'));
      await act(async () => finishOld(resolvedCollections()));
      expect(link).toHaveAttribute('data-artifact-link-resolution', 'unavailable');
      expect(link).not.toHaveAttribute('href', '/api/artifacts/artifact-report/versions/version-report');
      expect(artifactMocks.list).toHaveBeenCalledTimes(2);
    }
  );

  it('preserves generic relative webpage navigation outside an artifact-aware conversation', async () => {
    const onLink = vi.fn().mockResolvedValue(false);
    renderWithProviders(<MarkdownView onLink={onLink}>{'[Guide](guide.html)'}</MarkdownView>);
    const link = screen.getByRole('link', { name: 'Guide' });
    expect(link).not.toHaveClass('markdown-artifact-file-link');
    fireEvent.click(link);
    await waitFor(() =>
      expect(openExternalUrlMock).toHaveBeenCalledWith(new URL('guide.html', window.location.href).href)
    );
  });

  it('opens a generated link with Enter while resolution is pending', async () => {
    const onLink = vi.fn().mockResolvedValue(true);
    renderWithProviders(
      <MarkdownView onLink={onLink} resolveLinkHref={() => new Promise(() => undefined)}>
        {'[Report](/api/artifacts/artifact-report/versions/version-report)'}
      </MarkdownView>
    );
    const link = screen.getByRole('link', { name: 'Report' });
    expect(link).toHaveAttribute('aria-busy', 'true');
    link.focus();
    await userEvent.setup().keyboard('{Enter}');
    expect(onLink).toHaveBeenCalledWith('/api/artifacts/artifact-report/versions/version-report');
    expect(openExternalUrlMock).not.toHaveBeenCalled();
  });

  it('contains middle-click previews without intercepting the right-click context menu', async () => {
    const onLink = vi.fn().mockResolvedValue(true);
    renderWithProviders(<MarkdownView onLink={onLink}>{'[Report](/api/artifacts/a/versions/v)'}</MarkdownView>);
    const link = screen.getByRole('link', { name: 'Report' });
    fireEvent(link, new MouseEvent('auxclick', { bubbles: true, cancelable: true, button: 2 }));
    expect(onLink).not.toHaveBeenCalled();
    fireEvent(link, new MouseEvent('auxclick', { bubbles: true, cancelable: true, button: 1 }));
    await waitFor(() => expect(onLink).toHaveBeenCalledWith('/api/artifacts/a/versions/v'));
    expect(openExternalUrlMock).not.toHaveBeenCalled();
  });

  it('falls back to normal external navigation when a conversation handler declines a link', async () => {
    const onLink = vi.fn().mockResolvedValue(false);
    renderWithProviders(<MarkdownView onLink={onLink}>{'[docs](https://x-science.com/docs)'}</MarkdownView>);

    fireEvent.click(screen.getByRole('link', { name: 'docs' }));

    await waitFor(() => {
      expect(onLink).toHaveBeenCalledWith('https://x-science.com/docs');
      expect(openExternalUrlMock).toHaveBeenCalledWith('https://x-science.com/docs');
    });
  });

  it('projects an encoded artifact reference to its real API href while preserving click identity', async () => {
    const onLink = vi.fn().mockResolvedValue(true);
    const resolveLinkHref = vi.fn().mockResolvedValue('/api/artifacts/artifact-report/versions/version-report');

    renderWithProviders(
      <MarkdownView onLink={onLink} resolveLinkHref={resolveLinkHref}>
        {'[Final report](%7B%7Bartifact%3Aversion-report%7D%7D)'}
      </MarkdownView>
    );

    const link = screen.getByRole('link', { name: 'Final report' });
    expect(link).toHaveClass('x-science-artifact-link');
    expect(link).toHaveAttribute('data-x-science-artifact-link', 'true');
    await waitFor(() => {
      expect(link).toHaveAttribute('href', '/api/artifacts/artifact-report/versions/version-report');
    });
    expect(resolveLinkHref).toHaveBeenCalledWith('%7B%7Bartifact%3Aversion-report%7D%7D');

    fireEvent.click(link);
    await waitFor(() => {
      expect(onLink).toHaveBeenCalledWith('%7B%7Bartifact%3Aversion-report%7D%7D');
    });
    expect(openExternalUrlMock).not.toHaveBeenCalled();
  });

  it('keeps ordinary http links as browser anchors', () => {
    renderWithProviders(<MarkdownView>{'[docs](https://x-science.com/docs)'}</MarkdownView>);

    const link = screen.getByRole('link', { name: 'docs' });
    expect(link).toHaveAttribute('href', 'https://x-science.com/docs');
  });

  it('keeps http hash links as browser anchors', () => {
    renderWithProviders(<MarkdownView>{'[docs](https://x-science.com/docs#L10)'}</MarkdownView>);

    const link = screen.getByRole('link', { name: 'docs' });
    expect(link).toHaveAttribute('href', 'https://x-science.com/docs#L10');
  });

  it('adds empty alt text to external raw HTML images without alt text', () => {
    let callback: IntersectionObserverCallback | undefined;
    let target: Element | undefined;
    vi.stubGlobal(
      'IntersectionObserver',
      class {
        constructor(nextCallback: IntersectionObserverCallback) {
          callback = nextCallback;
        }
        observe(nextTarget: Element) {
          target = nextTarget;
        }
        unobserve() {}
        disconnect() {}
      }
    );
    const { container } = renderWithProviders(
      <MarkdownView allowHtml>{'<img src="https://example.com/generated.png" />'}</MarkdownView>
    );

    expect(container.querySelector('img')).toBeNull();
    if (!target) throw new Error('external image was not observed');
    act(() =>
      callback?.(
        [{ target, isIntersecting: true, intersectionRatio: 1 } as IntersectionObserverEntry],
        {} as IntersectionObserver
      )
    );
    const image = container.querySelector('img');
    expect(image).toHaveAttribute('src', 'https://example.com/generated.png');
    expect(image).toHaveAttribute('alt', '');
  });

  it('renders a bounded raster data image from streamed Markdown', () => {
    let callback: IntersectionObserverCallback | undefined;
    let target: Element | undefined;
    vi.stubGlobal(
      'IntersectionObserver',
      class {
        constructor(nextCallback: IntersectionObserverCallback) {
          callback = nextCallback;
        }
        observe(nextTarget: Element) {
          target = nextTarget;
        }
        unobserve() {}
        disconnect() {}
      }
    );
    const source = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB';
    renderWithProviders(<MarkdownView>{`![CRBN binding overview](${source})`}</MarkdownView>);

    if (!target) throw new Error('inline image was not observed');
    act(() =>
      callback?.(
        [{ target, isIntersecting: true, intersectionRatio: 1 } as IntersectionObserverEntry],
        {} as IntersectionObserver
      )
    );
    expect(screen.getByRole('img', { name: 'CRBN binding overview' })).toHaveAttribute('src', source);
  });

  it('renders a relative X-Science image through its resolved artifact URL', async () => {
    let callback: IntersectionObserverCallback | undefined;
    let target: Element | undefined;
    vi.stubGlobal(
      'IntersectionObserver',
      class {
        constructor(nextCallback: IntersectionObserverCallback) {
          callback = nextCallback;
        }
        observe(nextTarget: Element) {
          target = nextTarget;
        }
        unobserve() {}
        disconnect() {}
      }
    );
    const resolveImageSrc = vi.fn().mockResolvedValue('/api/artifacts/artifact-figure');
    renderWithProviders(
      <MarkdownView resolveImageSrc={resolveImageSrc}>{'![Coverage]({{artifact:version-figure}})'}</MarkdownView>
    );

    expect(resolveImageSrc).not.toHaveBeenCalled();
    expect(target).toBeDefined();
    if (!target) throw new Error('artifact image was not observed');
    act(() =>
      callback?.(
        [{ target, isIntersecting: true, intersectionRatio: 1 } as IntersectionObserverEntry],
        {} as IntersectionObserver
      )
    );

    await waitFor(() => {
      expect(screen.getByRole('img', { name: 'Coverage' })).toHaveAttribute('src', '/api/artifacts/artifact-figure');
    });
    expect(resolveImageSrc).toHaveBeenCalledWith('{{artifact:version-figure}}');
  });
});
