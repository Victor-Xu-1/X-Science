/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import enPreview from '@/renderer/services/i18n/locales/en-US/preview.json';
import zhPreview from '@/renderer/services/i18n/locales/zh-CN/preview.json';
import { createInstance } from 'i18next';
import { describe, expect, it } from 'vitest';

function collectLeafPaths(value: unknown, prefix = ''): string[] {
  if (value === null || typeof value !== 'object') return [prefix];
  if (Array.isArray(value)) {
    return value.flatMap((entry, index) => collectLeafPaths(entry, `${prefix}[${index}]`));
  }
  return Object.entries(value).flatMap(([key, entry]) => collectLeafPaths(entry, prefix ? `${prefix}.${key}` : key));
}

describe('preview i18n resources', () => {
  it('keeps English and Chinese translation keys structurally identical', () => {
    expect(collectLeafPaths(enPreview).toSorted()).toEqual(collectLeafPaths(zhPreview).toSorted());
  });

  it.each([
    [
      'en-US',
      enPreview,
      'Parent and derived structure overlay scene',
      'Parent + derived structure overlay · layers: 3',
    ],
    ['zh-CN', zhPreview, '母结构与派生结构叠加场景', '母结构 + 派生结构叠加 · 3 层'],
  ] as const)(
    'localizes the overlay scene and preserves its layer count in %s',
    async (language, preview, label, summary) => {
      const i18n = createInstance();
      await i18n.init({
        lng: language,
        fallbackLng: false,
        resources: { [language]: { preview } },
        defaultNS: 'preview',
      });
      expect(i18n.t('scientific.structure.sceneOverlayLabel')).toBe(label);
      expect(i18n.t('scientific.structure.sceneOverlaySummary', { layerCount: 3 })).toBe(summary);
    }
  );
});
