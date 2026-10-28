/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */
import React from 'react';

export type LibraryRailSlot = {
  host: HTMLElement | null;
  owner?: object;
  register: (owner: object, account: string) => () => void;
};

export interface LayoutContextValue {
  isMobile: boolean;
  siderCollapsed: boolean;
  setSiderCollapsed: (value: boolean) => void;
  siderId?: string;
  siderToggleId?: string;
  libraryRail?: LibraryRailSlot;
}

export const LayoutContext = React.createContext<LayoutContextValue | null>(null);

export function useLayoutContext(): LayoutContextValue | null {
  return React.useContext(LayoutContext);
}
