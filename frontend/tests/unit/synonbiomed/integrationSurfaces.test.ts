/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { describe, expect, it } from 'vitest';
import { buildSynonBiomedIntegrationSurfaces } from '@/renderer/services/synonBiomedCatalog';

describe('X-Science system integration surfaces', () => {
  it('maps X-Science capabilities across SynonAI primary layouts without exposing old frontend module lists', () => {
    const surfaces = buildSynonBiomedIntegrationSurfaces();

    expect(surfaces.map((surface) => surface.id)).toEqual([
      'launch',
      'conversation',
      'workspace',
      'preview',
      'capabilities',
      'governance',
      'runtime',
    ]);
    expect(surfaces).toHaveLength(7);
    expect(surfaces.every((surface) => surface.synonAiSurface !== 'settings-only')).toBe(true);
    expect(surfaces.every((surface) => !('legacyModules' in surface))).toBe(true);
  });
});
