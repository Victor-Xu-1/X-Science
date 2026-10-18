import { createHostGrantFileReference, decodeHostGrantFileReference } from '@/common/adapter/hostGrantFileReference';
import { describe, expect, it } from 'vitest';

describe('host-grant file display decoder', () => {
  it('preserves literal percent escapes and validates encoded text without lossy replacement', () => {
    const value = createHostGrantFileReference('/fixture/root + 测量', 'nested/%2e%2e.txt');
    expect(value).not.toBeNull();
    expect(decodeHostGrantFileReference(value!)).toEqual({
      grant: '/fixture/root + 测量',
      path: 'nested/%2e%2e.txt',
      name: '%2e%2e.txt',
    });
    expect(decodeHostGrantFileReference(`${value}%FF`)).toBeNull();
    expect(createHostGrantFileReference('/fixture/root', '../outside.txt')).toBeNull();
    expect(createHostGrantFileReference('/fixture/root', 'invalid\ud800.txt')).toBeNull();
  });

  it('does not turn duplicate or extra query fields into a display-valid reference', () => {
    const value = createHostGrantFileReference('/fixture/root', 'sample.txt')!;
    expect(decodeHostGrantFileReference(`${value}&path=other.txt`)).toBeNull();
    expect(decodeHostGrantFileReference(`${value}&owner=other`)).toBeNull();
    expect(decodeHostGrantFileReference(`${value}#ignored`)).toBeNull();
  });
});
