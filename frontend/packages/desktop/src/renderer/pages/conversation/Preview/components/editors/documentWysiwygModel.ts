import DOMPurify from 'dompurify';
import TurndownService from 'turndown';
import { gfm } from 'turndown-plugin-gfm';
import {
  ORIGINAL_LINK_ATTRIBUTE,
  ORIGINAL_RESOURCE_ATTRIBUTE,
  validateDocumentResourceSource,
  type DocumentResourceKind,
} from './documentResourceModel';

export { ORIGINAL_LINK_ATTRIBUTE, ORIGINAL_RESOURCE_ATTRIBUTE } from './documentResourceModel';

const ARTIFACT_VERSION_URL = /^\/api\/artifacts\/versions\/([0-9a-f-]{36})$/i;

export function restoreMarkdownResourceSource(source: string): string {
  const match = source.match(ARTIFACT_VERSION_URL);
  return match ? `{{artifact:${match[1]}}}` : source;
}

export function markdownFromEditableHtml(html: string): string {
  const container = document.createElement('div');
  container.innerHTML = sanitizeEditableHtmlForTurndown(html);
  container
    .querySelectorAll('[data-document-selected]')
    .forEach((node) => node.removeAttribute('data-document-selected'));

  const turndown = new TurndownService({
    headingStyle: 'atx',
    bulletListMarker: '-',
    codeBlockStyle: 'fenced',
    emDelimiter: '*',
    strongDelimiter: '**',
  });
  turndown.use(gfm);
  turndown.addRule('synon-original-image-source', {
    filter: 'img',
    replacement: (_content, node) => {
      const image = node as HTMLImageElement;
      const source = restoreMarkdownResourceSource(
        validatedResourceSource(image, ORIGINAL_RESOURCE_ATTRIBUTE, 'src', 'image')
      );
      const alt = image.getAttribute('alt') || '';
      const title = image.getAttribute('title');
      const width = image.style.width || image.getAttribute('width') || '';
      if (source && width && width !== '100%') {
        const encodedSource = escapeHtmlAttribute(source);
        const encodedAlt = escapeHtmlAttribute(alt);
        const encodedWidth = escapeHtmlAttribute(width);
        return `<img src="${encodedSource}" alt="${encodedAlt}" style="width:${encodedWidth};height:auto">`;
      }
      return source
        ? `![${turndown.escape(alt).replace(/[<>]/g, '\\$&')}](${escapeMarkdownDestination(source)}${title ? ` "${escapeMarkdownTitle(title)}"` : ''})`
        : '';
    },
  });
  turndown.addRule('synon-artifact-link-source', {
    // Own rejected anchors too: native Turndown would otherwise restore a
    // protocol that DOMPurify permits but the shared resource policy rejects.
    filter: 'a',
    replacement: (content, node) => {
      const link = node as HTMLAnchorElement;
      const source = restoreMarkdownResourceSource(
        validatedResourceSource(link, ORIGINAL_LINK_ATTRIBUTE, 'href', 'link')
      );
      if (!source) return content;
      const title = link.getAttribute('title');
      return `[${content}](${escapeMarkdownDestination(source)}${title ? ` "${escapeMarkdownTitle(title)}"` : ''})`;
    },
  });
  turndown.addRule('synon-katex-source', {
    filter: (node) => {
      if (!(node instanceof HTMLElement) || !node.classList.contains('katex')) return false;
      return !node.parentElement?.closest('.katex');
    },
    replacement: (_content, node) => {
      const source = node.querySelector('annotation[encoding="application/x-tex"]')?.textContent?.trim() ?? '';
      const display = node.parentElement?.classList.contains('katex-display');
      return source ? (display ? `\n\n$$${source}$$\n\n` : `$${source}$`) : '';
    },
  });

  return turndown
    .turndown(container)
    .replace(/\n{3,}/g, '\n\n')
    .trimEnd();
}

export function replaceHtmlDocumentBody(originalHtml: string, editedBodyOrDocument: string): string {
  const parser = new DOMParser();
  const original = parser.parseFromString(originalHtml, 'text/html');
  const edited = parser.parseFromString(editedBodyOrDocument, 'text/html');
  const body = original.importNode(edited.body, true);

  restoreOriginalResourceSources(body);
  body.removeAttribute('contenteditable');
  body.removeAttribute('aria-label');
  original.documentElement.replaceChild(body, original.body);

  return `${serializeDoctype(original.doctype)}${original.documentElement.outerHTML}`;
}

export function restoreOriginalResourceSources(root: ParentNode): void {
  root.querySelectorAll<HTMLElement>(`[${ORIGINAL_RESOURCE_ATTRIBUTE}]`).forEach((element) => {
    const source = validatedResourceSource(element, ORIGINAL_RESOURCE_ATTRIBUTE, 'src', 'image');
    if (source) element.setAttribute('src', source);
    else element.removeAttribute('src');
    element.removeAttribute(ORIGINAL_RESOURCE_ATTRIBUTE);
    element.removeAttribute('data-document-selected');
  });
  root.querySelectorAll<HTMLElement>(`[${ORIGINAL_LINK_ATTRIBUTE}]`).forEach((element) => {
    const source = validatedResourceSource(element, ORIGINAL_LINK_ATTRIBUTE, 'href', 'link');
    if (source) element.setAttribute('href', source);
    else element.removeAttribute('href');
    element.removeAttribute(ORIGINAL_LINK_ATTRIBUTE);
  });
}

// Data attributes survive HTML sanitization, so provenance is not permission
// to restore a source. Reuse the insertion policy at every export boundary.
function validatedResourceSource(
  element: Element,
  originalAttribute: string,
  displayAttribute: string,
  kind: DocumentResourceKind
): string {
  for (const candidate of [element.getAttribute(originalAttribute), element.getAttribute(displayAttribute)]) {
    if (!candidate) continue;
    const validation = validateDocumentResourceSource(candidate, kind);
    if (validation.valid) return validation.source;
  }
  return '';
}

function escapeMarkdownDestination(source: string): string {
  const escaped = source.replace(/[\\()[\]<>]/g, '\\$&');
  return /\s/.test(source) ? `<${escaped}>` : escaped;
}

function escapeMarkdownTitle(title: string): string {
  return title
    .replaceAll('\\', '\\\\')
    .replaceAll('"', '\\"')
    .replace(/[<>]/g, '\\$&')
    .replaceAll('\r', '&#13;')
    .replaceAll('\n', '&#10;');
}

export function isLocalDocumentResource(source: string): boolean {
  return (
    Boolean(source) && !/^\{\{artifact:[^{}]+\}\}$/i.test(source) && !/^(?:https?:|data:|blob:|\/\/|#)/i.test(source)
  );
}

export function sanitizeDocumentPasteHtml(html: string): string {
  return DOMPurify.sanitize(html, {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ['script', 'style', 'iframe', 'object', 'embed'],
    FORBID_ATTR: ['srcdoc'],
  });
}

/** Sanitize editor HTML before Turndown while preserving KaTeX source annotations. */
export function sanitizeEditableHtmlForTurndown(html: string): string {
  return DOMPurify.sanitize(html, {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ['script', 'style', 'iframe', 'object', 'embed'],
    FORBID_ATTR: ['srcdoc', 'onerror', 'onclick', 'onload'],
    ADD_TAGS: ['math', 'semantics', 'annotation', 'mrow', 'mi', 'mn', 'mo', 'msup', 'mfrac', 'mspace', 'mtext'],
    ADD_ATTR: ['encoding', 'class'],
  });
}

function serializeDoctype(doctype: DocumentType | null): string {
  if (!doctype) return '';
  const publicId = doctype.publicId ? ` PUBLIC "${doctype.publicId}"` : '';
  const systemId = doctype.systemId ? `${publicId ? '' : ' SYSTEM'} "${doctype.systemId}"` : '';
  return `<!DOCTYPE ${doctype.name}${publicId}${systemId}>\n`;
}

function escapeHtmlAttribute(value: string): string {
  return value.replaceAll('&', '&amp;').replaceAll('"', '&quot;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
}
