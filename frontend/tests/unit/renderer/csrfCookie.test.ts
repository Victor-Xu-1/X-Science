import { describe, expect, it } from 'vitest';
import { CSRF_COOKIE_NAME, csrfCookieClearDirective } from '@/renderer/services/csrf';

describe('csrfCookieClearDirective', () => {
  it('matches the existing Path and SameSite contract on http pages', () => {
    expect(csrfCookieClearDirective('http:')).toBe(`${CSRF_COOKIE_NAME}=; Path=/; Max-Age=0; SameSite=Strict`);
  });

  it('adds Secure on https so a Secure-flagged cookie can actually expire', () => {
    expect(csrfCookieClearDirective('https:')).toBe(`${CSRF_COOKIE_NAME}=; Path=/; Max-Age=0; SameSite=Strict; Secure`);
  });
});
