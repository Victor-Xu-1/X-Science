import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const rendererRoot = path.resolve(import.meta.dirname, '../../../packages/desktop/src/renderer');

function source(relativePath: string): string {
  return readFileSync(path.join(rendererRoot, relativePath), 'utf8');
}

describe('preview editor lazy boundary', () => {
  it('keeps CodeMirror and table editors out of the preview panel entry graph', () => {
    const panel = source('pages/conversation/Preview/components/PreviewPanel/PreviewPanel.tsx');
    const board = source('pages/conversation/Preview/components/PreviewPanel/PreviewBoard.tsx');

    for (const file of [panel, board]) {
      expect(file).not.toMatch(/^import CodeEditor from /m);
      expect(file).not.toMatch(/^import DelimitedTableEditor from /m);
      expect(file).toContain("React.lazy(() => import('../editors/CodeEditor'))");
      expect(file).toContain("React.lazy(() => import('../editors/DelimitedTableEditor'))");
    }
  });

  it('does not pull CodeMirror language data through the markdown preview theme barrel', () => {
    const themeBarrel = source('pages/conversation/Preview/theme/index.ts');
    const markdownViewer = source('pages/conversation/Preview/components/viewers/MarkdownViewer.tsx');

    expect(themeBarrel).not.toContain('languageLoader');
    expect(markdownViewer).toContain("from '../../theme/markdownTheme'");
    expect(markdownViewer).not.toMatch(/from ['"]\.\.\/\.\.\/theme['"]/);
  });

  it('loads diff2html only after a workspace diff is expanded', () => {
    const fileChangeList = source('pages/conversation/Workspace/components/FileChangeList.tsx');
    expect(fileChangeList).not.toMatch(/^import Diff2Html from /m);
    expect(fileChangeList).toContain("React.lazy(() => import('@/renderer/components/media/Diff2Html'))");
  });
});
