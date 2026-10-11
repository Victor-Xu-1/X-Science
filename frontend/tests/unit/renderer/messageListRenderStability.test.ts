/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import {
  areStringNumberMapsEqual,
  areStringSetsEqual,
  reuseIfEqual,
} from '@/renderer/pages/conversation/Messages/messageListRenderStability';
import { describe, expect, it } from 'vitest';

describe('messageListRenderStability', () => {
  it('reuses the previous map when streaming only updates message content', () => {
    const previous = new Map([
      ['user-1', 0],
      ['assistant-1', 1],
    ]);
    const next = new Map([
      ['user-1', 0],
      ['assistant-1', 1],
    ]);
    expect(areStringNumberMapsEqual(previous, next)).toBe(true);
    expect(reuseIfEqual(previous, next, areStringNumberMapsEqual)).toBe(previous);
  });

  it('replaces the map when a new message id appears', () => {
    const previous = new Map([['user-1', 0]]);
    const next = new Map([
      ['user-1', 0],
      ['assistant-1', 1],
    ]);
    expect(areStringNumberMapsEqual(previous, next)).toBe(false);
    expect(reuseIfEqual(previous, next, areStringNumberMapsEqual)).toBe(next);
  });

  it('reuses the previous copy-row set when membership is unchanged', () => {
    const previous = new Set(['assistant-1']);
    const next = new Set(['assistant-1']);
    expect(areStringSetsEqual(previous, next)).toBe(true);
    expect(reuseIfEqual(previous, next, areStringSetsEqual)).toBe(previous);
  });
});
