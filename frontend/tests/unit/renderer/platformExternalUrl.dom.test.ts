import { afterEach, describe, expect, it, vi } from 'vitest';
import { openExternalUrl } from '@/renderer/utils/platform';

describe('openExternalUrl', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('opens http(s) and mailto destinations, including intranet hosts', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null);

    await openExternalUrl('https://example.org/paper');
    await openExternalUrl('http://192.168.1.10/result');
    await openExternalUrl('mailto:lab@example.org');

    expect(open).toHaveBeenCalledTimes(3);
    expect(open).toHaveBeenNthCalledWith(1, 'https://example.org/paper', '_blank', 'noopener,noreferrer');
    expect(open).toHaveBeenNthCalledWith(2, 'http://192.168.1.10/result', '_blank', 'noopener,noreferrer');
    expect(open).toHaveBeenNthCalledWith(3, 'mailto:lab@example.org', '_blank', 'noopener,noreferrer');
  });

  it('does not open javascript, data, or file schemes', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null);

    await openExternalUrl('javascript:alert(1)');
    await openExternalUrl('data:text/html,<script>alert(1)</script>');
    await openExternalUrl('file:///etc/passwd');
    await openExternalUrl('vbscript:msgbox(1)');

    expect(open).not.toHaveBeenCalled();
  });
});
