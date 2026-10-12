const DEFAULT_USERNAME = 'victor';
const DEFAULT_PASSWORD = '12345678';

export function resolveXScienceTestUsername(): string {
  return process.env.X_SCIENCE_GO_WEB_USERNAME ?? process.env.X_SCIENCE_WEBUI_USERNAME ?? DEFAULT_USERNAME;
}

function resolveXScienceTestPassword(): string {
  return process.env.X_SCIENCE_GO_WEB_PASSWORD ?? process.env.X_SCIENCE_WEBUI_PASSWORD ?? DEFAULT_PASSWORD;
}

export async function createXScienceTestFetch(baseUrl: string): Promise<typeof fetch> {
  const normalizedBaseUrl = baseUrl.replace(/\/+$/, '');
  const gatewayOrigin = new URL(normalizedBaseUrl).origin;
  const login = await fetch(`${normalizedBaseUrl}/login`, {
    method: 'POST',
    headers: { accept: 'application/json', 'content-type': 'application/json' },
    body: JSON.stringify({
      username: resolveXScienceTestUsername(),
      password: resolveXScienceTestPassword(),
      remember: false,
    }),
  });
  if (!login.ok) {
    throw new Error(`X-Science test login failed: ${login.status} ${await login.text()}`);
  }
  const setCookie = login.headers.get('set-cookie') ?? '';
  const session = setCookie.match(/(?:^|,\s*)(x_science_session=[^;,\s]+)/)?.[1];
  const csrfToken = setCookie.match(/(?:^|,\s*)x_science_csrf=([^;,\s]+)/)?.[1];
  if (!session || !csrfToken) {
    throw new Error('X-Science test login did not return session and CSRF cookies');
  }

  return ((input: string | URL | Request, init: RequestInit = {}) => {
    const request = input instanceof Request ? input : undefined;
    const headers = new Headers(request?.headers);
    new Headers(init.headers).forEach((value, key) => headers.set(key, value));
    const target = new URL(request?.url ?? String(input), normalizedBaseUrl);
    if (target.origin === gatewayOrigin) {
      headers.set('cookie', `${session}; x_science_csrf=${csrfToken}`);
      const method = (init.method ?? request?.method ?? 'GET').toUpperCase();
      if (!['GET', 'HEAD', 'OPTIONS'].includes(method)) {
        headers.set('X-Science-CSRF-Token', decodeURIComponent(csrfToken));
      }
    }
    return fetch(target, { ...init, headers });
  }) as typeof fetch;
}
