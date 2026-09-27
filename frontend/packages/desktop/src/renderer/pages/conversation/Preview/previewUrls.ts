/**
 * Resolve a browser-loadable document/media source. An authenticated transport
 * URL takes precedence over metadata identifying the original local file.
 * Disk and virtual workspace paths are identities, never browser file URLs.
 */
export const buildPdfSrc = (file_path?: string, content?: string): string => {
  const source = (content?.trim() || file_path?.trim()) ?? '';
  if (source.startsWith('/api/')) return source;
  if (!/^(?:https?:|data:|blob:)/i.test(source)) return '';
  try {
    const url = new URL(source);
    return url.username || url.password ? '' : source;
  } catch {
    return '';
  }
};
