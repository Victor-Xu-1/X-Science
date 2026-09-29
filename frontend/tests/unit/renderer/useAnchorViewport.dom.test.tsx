import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { useAnchorViewport } from '@/renderer/pages/conversation/Messages/useAnchorViewport';

describe('explicit anchor viewport lifecycle', () => {
  it('keeps the ordinary numeric initial state and seeds a new viewport only for explicit navigation', () => {
    const { result, rerender } = renderHook(({ keys }) => useAnchorViewport('c', keys), {
      initialProps: { keys: ['text:tail'] },
    });
    expect(result.current.initial).toBe(0);
    act(() => result.current.reset('text:question', 'center'));
    rerender({ keys: ['text:older', 'text:question', 'text:answer'] });
    expect(result.current.initial).toEqual({ index: 1, align: 'center', behavior: 'auto' });
    const seed = result.current.initial;
    const revision = result.current.revision;
    rerender({ keys: ['text:older', 'text:question', 'text:answer', 'text:newer'] });
    expect(result.current.initial).toBe(seed);
    expect(result.current.revision).toBe(revision);
  });
  it("does not carry another conversation's scroll seed into a new conversation", () => {
    const { result, rerender } = renderHook(({ owner }) => useAnchorViewport(owner, ['text:q']), {
      initialProps: { owner: 'a' },
    });
    act(() => result.current.reset('text:q'));
    expect(result.current.revision).toBe(1);
    rerender({ owner: 'b' });
    expect(result.current.initial).toBe(0);
    expect(result.current.revision).toBe(0);
  });
});
