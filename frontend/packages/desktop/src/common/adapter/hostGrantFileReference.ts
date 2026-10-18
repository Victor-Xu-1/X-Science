const SCHEME = 'x-science-host-grant:';

export type HostGrantFileReference = { grant: string; path: string; name: string };

export function isHostGrantFileReference(value: string): boolean {
  return value.toLowerCase().startsWith(SCHEME);
}

function validText(value: string): boolean {
  return !Array.from(value).some((character) => {
    const codePoint = character.codePointAt(0)!;
    return (
      codePoint <= 0x1f || (codePoint >= 0x7f && codePoint <= 0x9f) || (codePoint >= 0xd800 && codePoint <= 0xdfff)
    );
  });
}

function validReference(grant: string, path: string): boolean {
  return Boolean(
    grant &&
    grant.trim() === grant &&
    validText(grant) &&
    validText(path) &&
    !path.includes('\\') &&
    path.split('/').every((segment) => segment !== '' && segment !== '.' && segment !== '..')
  );
}

export function createHostGrantFileReference(grant: string, path: string): string | null {
  if (!validReference(grant, path)) return null;
  const reference = `${SCHEME}//file?${new URLSearchParams({ grant, path })}`;
  return reference.length <= 8192 ? reference : null;
}

/** A display decoder, not file authorization. The server revalidates the live grant. */
export function decodeHostGrantFileReference(value: string): HostGrantFileReference | null {
  if (!isHostGrantFileReference(value) || value.length > 8192 || value.includes('#')) return null;
  try {
    const uri = new URL(value);
    // URLSearchParams replaces malformed UTF-8; reject it before that lossy decode.
    decodeURIComponent(uri.search.slice(1).replace(/\+/g, ' '));
    if (uri.protocol !== SCHEME || uri.host !== 'file' || uri.username || uri.password || uri.pathname) return null;
    const keys = Array.from(uri.searchParams.keys());
    if (
      keys.length !== 2 ||
      uri.searchParams.getAll('grant').length !== 1 ||
      uri.searchParams.getAll('path').length !== 1
    )
      return null;
    const grant = uri.searchParams.get('grant') ?? '';
    const path = uri.searchParams.get('path') ?? '';
    if (!validReference(grant, path)) return null;
    return { grant, path, name: path.split('/').at(-1)! };
  } catch {
    return null;
  }
}
