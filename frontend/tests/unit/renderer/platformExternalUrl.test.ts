import { describe, expect, it } from 'vitest';
import { isSafeExternalUrl, sanitizeExternalHttpUrl } from '@/renderer/utils/platform';

describe('external URL safety', () => {
  it('accepts http(s), mailto, and tel destinations, including intranet hosts', () => {
    expect(isSafeExternalUrl('https://github.com/Victor-Xu-1/X-Science')).toBe(true);
    expect(isSafeExternalUrl('http://192.168.10.4/jupyter')).toBe(true);
    expect(isSafeExternalUrl('http://127.0.0.1:8888/lab')).toBe(true);
    expect(isSafeExternalUrl('mailto:pi@example.org')).toBe(true);
    expect(isSafeExternalUrl('tel:+15551212')).toBe(true);
  });

  it('accepts http(s) paths with emoji and zero-width joiners', () => {
    const emojiPath = 'https://example.org/people/👩‍🔬';
    expect(isSafeExternalUrl(emojiPath)).toBe(true);
    expect(sanitizeExternalHttpUrl(emojiPath)).toBe(new URL(emojiPath).href);
  });

  it('rejects executable, local-file, and control-character schemes', () => {
    expect(isSafeExternalUrl('javascript:alert(1)')).toBe(false);
    expect(isSafeExternalUrl('  javascript:alert(1)')).toBe(false);
    expect(isSafeExternalUrl('data:text/html,<script>alert(1)</script>')).toBe(false);
    expect(isSafeExternalUrl('file:///etc/passwd')).toBe(false);
    expect(isSafeExternalUrl('vbscript:msgbox(1)')).toBe(false);
    expect(isSafeExternalUrl('blob:https://example.org/uuid')).toBe(false);
    expect(isSafeExternalUrl('https://example.org/ok\u0000')).toBe(false);
    expect(isSafeExternalUrl('')).toBe(false);
  });

  it('keeps relative same-origin paths when a page base is supplied', () => {
    expect(isSafeExternalUrl('guide.html', 'https://app.example/docs/index.html')).toBe(true);
    expect(isSafeExternalUrl('javascript:alert(1)', 'https://app.example/docs/index.html')).toBe(false);
  });

  it('sanitizes persisted compute-job URLs to http(s) only without blocking private hosts', () => {
    expect(sanitizeExternalHttpUrl('https://compute.example/jobs/sandbox-1')).toBe(
      'https://compute.example/jobs/sandbox-1'
    );
    expect(sanitizeExternalHttpUrl('http://10.0.0.8:8080/job')).toBe('http://10.0.0.8:8080/job');
    expect(sanitizeExternalHttpUrl('javascript:alert(1)')).toBeNull();
    expect(sanitizeExternalHttpUrl('mailto:ops@example.org')).toBeNull();
    expect(sanitizeExternalHttpUrl('data:text/html,hi')).toBeNull();
    expect(sanitizeExternalHttpUrl(null)).toBeNull();
  });
});
