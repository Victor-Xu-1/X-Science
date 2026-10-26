import DOMPurify from 'dompurify';
import { describe, expect, it } from 'vitest';

describe('DOMPurify shadow template text', () => {
  it('removes non-executable split template text while preserving ordinary shadow content', () => {
    const host = document.createElement('div');
    const shadow = host.attachShadow({ mode: 'open' });
    const ordinaryTail = document.createElement('span');
    ordinaryTail.textContent = ' remains';
    shadow.append(
      document.createTextNode('Ordinary text '),
      document.createTextNode('$'),
      document.createElement('object'),
      document.createTextNode('{safeMarker}'),
      ordinaryTail
    );

    DOMPurify.sanitize(host, { IN_PLACE: true, SAFE_FOR_TEMPLATES: true });
    shadow.normalize();

    expect(shadow.querySelector('object')).toBeNull();
    expect(shadow.textContent).not.toContain('${safeMarker}');
    expect(shadow.textContent).toContain('Ordinary text');
    expect(shadow.textContent).toContain(' remains');
  });
});
