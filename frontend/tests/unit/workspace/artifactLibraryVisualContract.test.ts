import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const rendererRoot = new URL('../../../packages/desktop/src/renderer/', import.meta.url);
const workspaceCss = readFileSync(new URL('pages/conversation/Workspace/workspace.css', rendererRoot), 'utf8');
const librarySource = readFileSync(
  new URL('pages/conversation/Workspace/components/XScienceArtifactLibrary.tsx', rendererRoot),
  'utf8'
);

describe('artifact library compact visual contract', () => {
  it('retains the thumbnail ratio and compact metadata', () => {
    expect(workspaceCss).toMatch(/aspect-ratio:\s*4\s*\/\s*3/);
    expect(workspaceCss).toMatch(/\.artifact-library__size[\s\S]*white-space:\s*nowrap/);
    expect(librarySource).not.toContain('h-92px');
  });
});
