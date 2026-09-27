import { describe, expect, it } from 'vitest';
import { pdfStaticCopyTargets, portableStaticCopySource } from '../../../vite.config';
import pdfPackage from 'pdfjs-dist/package.json';

describe('Vite static-copy source paths', () => {
  it('packages all PDF support resources under the parser version', () => {
    expect(pdfStaticCopyTargets).toHaveLength(3);
    for (const [index, directory] of ['cmaps', 'standard_fonts', 'wasm'].entries()) {
      expect(pdfStaticCopyTargets[index].src).toMatch(new RegExp(`/pdfjs-dist/${directory}$`));
      expect(pdfStaticCopyTargets[index].dest).toBe(`pdfjs/${pdfPackage.version}`);
    }
  });

  it('normalizes Windows paths before they enter the glob boundary', () => {
    expect(portableStaticCopySource(String.raw`C:\synon-biomed\frontend\LICENSE`)).toBe(
      'C:/synon-biomed/frontend/LICENSE'
    );
  });

  it('preserves already portable paths', () => {
    expect(portableStaticCopySource('/opt/synon-biomed/frontend/LICENSE')).toBe('/opt/synon-biomed/frontend/LICENSE');
  });
});
