export function sanitizeBackgroundDownloadRequest(value) {
  if (typeof value !== 'string' || !/^https?:\/\//i.test(value)) {
    throw new Error('A valid http(s) url is required')
  }
  const parsed = new URL(value)
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error('A valid http(s) url is required')
  }
  assertPublicFetchHostname(parsed.hostname)
  return Object.freeze({ href: parsed.toString() })
}

export async function fetchBackgroundDownloadResponse(sanitizedRequest) {
  return fetch(sanitizedRequest.href)
}

function assertPublicFetchHostname(hostname) {
  const normalized = String(hostname || '').trim().toLowerCase().replace(/\.$/, '')
  if (!normalized || normalized === 'localhost' || normalized.endsWith('.localhost')) {
    throw new Error('Refusing to fetch a local or private URL')
  }
  if (normalized === '::1' || normalized === '[::1]') {
    throw new Error('Refusing to fetch a local or private URL')
  }
  const ipv4Match = normalized.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/)
  if (ipv4Match) {
    const octets = ipv4Match.slice(1).map((part) => Number(part))
    if (octets.some((octet) => !Number.isInteger(octet) || octet < 0 || octet > 255)) {
      throw new Error('Refusing to fetch a local or private URL')
    }
    const [a, b] = octets
    if (
      a === 0
      || a === 10
      || a === 127
      || (a === 169 && b === 254)
      || (a === 172 && b >= 16 && b <= 31)
      || (a === 192 && b === 168)
      || a >= 224
    ) {
      throw new Error('Refusing to fetch a local or private URL')
    }
  }
}
