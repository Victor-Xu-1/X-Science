/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { cleanup, render } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it } from 'vitest';
import {
  SettingsGeneratedArtwork,
  SettingsGeneratedIcon,
  SettingsGeneratedNavIcon,
  settingsGeneratedWebpSrc,
  SETTINGS_GENERATED_ARTWORK_SRC,
  SETTINGS_GENERATED_ICON_SRC,
  SETTINGS_GENERATED_NAV_SRC,
} from '@/renderer/pages/settings/components/SettingsGeneratedAsset';

afterEach(cleanup);

describe('SettingsGeneratedAsset picture layout', () => {
  it('puts the layout class on the picture box and keeps the PNG img fallback', () => {
    const { container } = render(<SettingsGeneratedArtwork id='network-globe' className='network-section__artwork' />);

    const picture = container.querySelector('picture');
    const image = container.querySelector('img');
    const source = container.querySelector('source');
    const pngSrc = SETTINGS_GENERATED_ARTWORK_SRC['network-globe'];

    expect(picture).toHaveClass('network-section__artwork');
    expect(picture).not.toHaveClass('settings-generated-picture--passthrough');
    expect(picture).toHaveAttribute('data-settings-generated-asset', 'artwork:network-globe');
    expect(image).not.toHaveClass('network-section__artwork');
    expect(image).toHaveAttribute('src', pngSrc);
    expect(image).toHaveAttribute('data-settings-generated-asset', 'artwork:network-globe');
    expect(source).toHaveAttribute('type', 'image/webp');
    expect(source).toHaveAttribute('srcset', settingsGeneratedWebpSrc(pngSrc));
  });

  it('keeps flex icon classes on the picture so section icon sizing still applies', () => {
    const { container } = render(<SettingsGeneratedIcon id='network' className='settings-section__icon' />);

    const picture = container.querySelector('picture');
    const image = container.querySelector('img');

    expect(picture).toHaveClass('settings-section__icon');
    expect(image).not.toHaveClass('settings-section__icon');
    expect(image).toHaveAttribute('src', SETTINGS_GENERATED_ICON_SRC.network);
  });

  it('uses display-contents passthrough when nav icons have no layout class', () => {
    const { container } = render(<SettingsGeneratedNavIcon id='tools' />);

    const picture = container.querySelector('picture');
    const image = container.querySelector('img');

    expect(picture).toHaveClass('settings-generated-picture--passthrough');
    expect(image).toHaveAttribute('src', SETTINGS_GENERATED_NAV_SRC.tools);
    expect(image).toHaveAttribute('data-settings-generated-asset', 'nav:tools');
  });
});
