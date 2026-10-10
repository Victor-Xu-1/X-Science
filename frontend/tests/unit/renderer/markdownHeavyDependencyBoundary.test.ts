import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const rendererRoot = path.resolve(import.meta.dirname, '../../../packages/desktop/src/renderer');

function source(relativePath: string): string {
  return readFileSync(path.join(rendererRoot, relativePath), 'utf8');
}

describe('markdown heavy dependency boundary', () => {
  it('loads mermaid only when a diagram block actually renders', () => {
    const mermaidBlock = source('components/Markdown/MermaidBlock.tsx');
    const markdown = source('components/Markdown/index.tsx');
    const codeBlock = source('components/Markdown/CodeBlock.tsx');

    expect(mermaidBlock).not.toMatch(/^import mermaid from ['"]mermaid['"]/m);
    expect(mermaidBlock).toContain("import('mermaid')");
    expect(markdown).not.toContain("from 'mermaid'");
    expect(codeBlock).not.toContain("from 'mermaid'");
  });

  it('uses on-demand highlight.js languages instead of the full highlighter bundle', () => {
    const consumers = [
      source('components/Markdown/CodeBlock.tsx'),
      source('components/Markdown/MermaidBlock.tsx'),
      source('pages/conversation/Preview/components/viewers/DiffViewer.tsx'),
    ];

    for (const file of consumers) {
      expect(file).toContain("from 'react-syntax-highlighter/dist/esm/light-async'");
      expect(file).not.toMatch(/from ['"]react-syntax-highlighter['"]/);
    }

    expect(source('components/Markdown/index.tsx')).not.toContain('react-syntax-highlighter');
  });
});
