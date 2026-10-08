import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderBootDocument } from '../../../scripts/rendererBoot';

const frontend = path.resolve(import.meta.dirname, '../../..');
const template = readFileSync(path.join(frontend, 'packages/desktop/src/renderer/index.html'), 'utf8');
const reloadPage = vi.fn();
const copy = {
  'en-US': {
    loading: 'Starting {{productName}}…',
    slow: 'Still starting…',
    slowDescription: 'Some resources are taking longer to load.',
    failed: 'Unable to open the app',
    failedDescription: 'Reload to try again.',
    reload: 'Reload',
  },
  'zh-CN': {
    loading: '{{productName}} 正在启动…',
    slow: '启动时间较长',
    slowDescription: '部分资源仍在加载。',
    failed: '无法打开应用',
    failedDescription: '重新加载后再试。',
    reload: '重新加载',
  },
};

function mount(hint?: string): void {
  document.documentElement.innerHTML = renderBootDocument(template, 'X-Science', 'en-US', copy);
  if (hint) localStorage.setItem('i18nextLng', hint);
  const runtime = readFileSync(path.join(frontend, 'public/app-boot.js'), 'utf8');
  const runtimeWindow = {
    setTimeout: window.setTimeout.bind(window),
    clearTimeout: window.clearTimeout.bind(window),
    MutationObserver: window.MutationObserver,
    ErrorEvent: window.ErrorEvent,
    HTMLScriptElement: window.HTMLScriptElement,
    addEventListener: window.addEventListener.bind(window),
    removeEventListener: window.removeEventListener.bind(window),
    location: { href: 'http://127.0.0.1:18885/#/conversation/current', reload: reloadPage },
  };
  new Function('window', 'document', runtime)(runtimeWindow, document);
}

describe('independent app startup recovery', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    localStorage.clear();
    reloadPage.mockReset();
  });
  afterEach(() => {
    window.dispatchEvent(new Event('pagehide'));
    vi.useRealTimers();
    document.documentElement.innerHTML = '<head></head><body></body>';
  });

  it('renders the shared English default and a native reload form before any JavaScript', () => {
    const html = renderBootDocument(template, 'X-Science', 'en-US', copy);
    expect(html).toContain('lang="en-US"');
    expect(html).toContain('Starting X-Science…');
    expect(html).toContain('data-app-boot-reload');
    expect(html).toContain('method="get"');
    expect(html.match(/data-app-boot-label>([^<]+)/)?.[1]).toBe('Starting X-Science…');
  });

  it('preserves a saved Chinese choice without a preference write', () => {
    mount('zh-CN');
    expect(document.documentElement.lang).toBe('zh-CN');
    expect(document.querySelector('[data-app-boot-label]')?.textContent).toBe('X-Science 正在启动…');
    expect(document.querySelector('[data-app-boot-reload]')?.textContent).toBe('重新加载');
    expect(localStorage.getItem('i18nextLng')).toBe('zh-CN');
  });

  it('offers recovery for slow loading without pretending it has failed or reloading automatically', () => {
    mount();
    vi.advanceTimersByTime(30_000);
    expect(document.querySelector('[data-app-boot-label]')?.textContent).toBe(copy['en-US'].slow);
    expect(document.querySelector('.app-boot-loading')?.getAttribute('role')).toBe('status');
    expect(document.querySelector('[data-app-boot-description]')?.textContent).toBe(copy['en-US'].slowDescription);
    expect(reloadPage).not.toHaveBeenCalled();
  });

  it('reloads the current page only on explicit user action', () => {
    mount();
    document.querySelector<HTMLButtonElement>('[data-app-boot-reload]')!.click();
    expect(reloadPage).toHaveBeenCalledOnce();
  });

  it('shows a safe localized failure for the application entry without leaking raw errors', () => {
    mount();
    const entry = document.querySelector('script[data-app-entry]')!;
    entry.dispatchEvent(new Event('error'));
    expect(document.querySelector('.app-boot-loading')?.getAttribute('role')).toBe('alert');
    expect(document.querySelector('[data-app-boot-label]')?.textContent).toBe(copy['en-US'].failed);
    vi.advanceTimersByTime(30_000);
    expect(document.querySelector('[data-app-boot-label]')?.textContent).toBe(copy['en-US'].failed);
  });

  it('does not replace mounted app content when a later error or timeout arrives', async () => {
    mount();
    document.getElementById('root')!.innerHTML = '<main><textarea>Retained draft</textarea></main>';
    await Promise.resolve();
    window.dispatchEvent(new Event('vite:preloadError'));
    vi.advanceTimersByTime(30_000);
    expect(document.querySelector('textarea')?.value).toBe('Retained draft');
    expect(document.querySelector('.app-boot-loading')).toBeNull();
  });

  it('escapes markup and serialized configuration rather than trusting product names', () => {
    const name = '</script><img src=x onerror=alert(1)>';
    const html = renderBootDocument(template, name, 'en-US', copy);
    document.documentElement.innerHTML = html;
    expect(document.querySelector('img[src=x]')).toBeNull();
    expect(JSON.parse(document.getElementById('app-boot-config')!.textContent!).productName).toBe(name);
  });

  it('rejects incomplete dictionaries and never replaces markers inside product data', () => {
    expect(() => renderBootDocument(template, 'X-Science', 'missing', copy)).toThrow('Incomplete');
    expect(() =>
      renderBootDocument(template, 'X-Science', 'en-US', {
        ...copy,
        'zh-CN': { ...copy['zh-CN'], failedDescription: '' },
      })
    ).toThrow('Incomplete');
    const name = '__APP_BOOT_CONFIGURATION__';
    const html = renderBootDocument(template, name, 'en-US', copy);
    document.documentElement.innerHTML = html;
    expect(document.querySelector('[data-app-boot-label]')?.textContent).toBe(`Starting ${name}…`);
  });
});
