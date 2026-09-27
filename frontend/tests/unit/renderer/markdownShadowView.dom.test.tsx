import { render } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/renderer/hooks/context/LayoutContext', () => ({
  useLayoutContext: () => null,
}));

vi.mock('@/renderer/hooks/context/ThemeContext', () => ({
  useThemeContext: () => ({
    theme: 'light',
    activeTheme: null,
    fontScale: 1,
    fontSizes: {},
  }),
}));

import ShadowView from '@/renderer/components/Markdown/ShadowView';

describe('ShadowView', () => {
  afterEach(() => vi.restoreAllMocks());

  it('keeps generated file links blue and bold inside the shadow stylesheet, including nested labels', () => {
    const view = render(
      <ShadowView>
        <a className='markdown-artifact-file-link'>
          <strong>Report</strong>
        </a>
      </ShadowView>
    );
    const style = view.container.querySelector('.markdown-shadow')?.shadowRoot?.querySelector('style')?.textContent;
    expect(style).toContain('.markdown-shadow-body .markdown-artifact-file-link');
    expect(style).toContain('color: #1d4ed8');
    expect(style).toContain('font-weight: 700');
    expect(style).toContain('.markdown-artifact-file-link :is(strong, code, span)');
  });

  it('projects the first render through a slot before the portal commit', () => {
    const originalAttachShadow = HTMLElement.prototype.attachShadow;
    let lightDomAtAttach = '';
    vi.spyOn(HTMLElement.prototype, 'attachShadow').mockImplementation(function (
      this: HTMLElement,
      init: ShadowRootInit
    ) {
      lightDomAtAttach = this.textContent ?? '';
      return originalAttachShadow.call(this, init);
    });

    const view = render(
      <ShadowView>
        <div className='markdown-shadow-body'>首个流式片段</div>
      </ShadowView>
    );

    const host = view.container.querySelector('.markdown-shadow');
    expect(lightDomAtAttach).toContain('首个流式片段');
    expect(host?.shadowRoot?.querySelector('slot')).not.toBeNull();
    expect(host?.shadowRoot?.textContent).toContain('首个流式片段');
  });
});
