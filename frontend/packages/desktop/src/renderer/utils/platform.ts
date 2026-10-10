/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Platform detection utilities
 * 平台检测工具函数
 */

/**
 * Check if running in Electron desktop environment
 * 检测是否运行在 Electron 桌面环境
 */
export const isElectronDesktop = (): boolean => {
  return false;
};

/**
 * Check if running on macOS
 * 检测是否运行在 macOS
 */
export const isMacOS = (): boolean => {
  return typeof navigator !== 'undefined' && /mac/i.test(navigator.userAgent);
};

/**
 * Check if running on Windows
 * 检测是否运行在 Windows
 */
export const isWindows = (): boolean => {
  return typeof navigator !== 'undefined' && /win/i.test(navigator.userAgent);
};

/**
 * Check if running on Linux
 * 检测是否运行在 Linux
 */
export const isLinux = (): boolean => {
  return typeof navigator !== 'undefined' && /linux/i.test(navigator.userAgent);
};

function isAbsoluteAssetUrl(url: string): boolean {
  return /^[a-z][a-z\d+.-]*:/i.test(url) || url.startsWith('//');
}

/**
 * Resolve a backend-served asset URL for the current environment.
 * In Electron, renderer pages are file:// based, so backend-relative paths
 * must be expanded against the backend HTTP origin.
 */
export const resolveBackendAssetUrl = (url: string | undefined): string | undefined => {
  if (!url) return url;
  if (isAbsoluteAssetUrl(url) || /^data:/i.test(url)) return url;
  if (url.startsWith('/')) {
    return url;
  }
  return url;
};

/**
 * Resolve an extension asset URL for the current environment.
 * Backend-managed extension assets are already emitted as HTTP URLs, so this
 * helper resolves app-relative backend paths into absolute backend URLs when
 * the desktop renderer is not same-origin with the backend process.
 *
 * 将扩展资源 URL 转换为当前环境可用的地址
 */
export const resolveExtensionAssetUrl = (url: string | undefined): string | undefined => {
  return resolveBackendAssetUrl(url);
};

/** Schemes a user-initiated window.open may follow. Private/intranet HTTP(S) stays allowed. */
const SAFE_WINDOW_SCHEMES = new Set(['http:', 'https:', 'mailto:', 'tel:']);
const SAFE_HTTP_SCHEMES = new Set(['http:', 'https:']);

function parseExternalUrl(url: string, base?: string): URL | null {
  const trimmed = url.trim();
  if (!trimmed || /\p{C}/u.test(trimmed)) return null;
  try {
    return base ? new URL(trimmed, base) : new URL(trimmed);
  } catch {
    return null;
  }
}

/**
 * True when `url` is a navigable http(s)/mailto/tel destination.
 * Relative paths resolve against `base` (typically the current page) so existing
 * same-origin markdown links keep working. javascript:, data:, file:, and other
 * active schemes are rejected. Private-network HTTP(S) is not blocked.
 */
export function isSafeExternalUrl(url: string, base?: string): boolean {
  const parsed = parseExternalUrl(url, base);
  return parsed !== null && SAFE_WINDOW_SCHEMES.has(parsed.protocol);
}

/**
 * Canonical http(s) href for persisted provider/job destinations.
 * Matches the existing compute-job remote-link contract: no mailto/tel, no
 * credentials stripping beyond what URL serialization already does, and no
 * private-IP block (science clusters are often on intranet hosts).
 */
export function sanitizeExternalHttpUrl(value: string | null | undefined): string | null {
  if (!value) return null;
  const parsed = parseExternalUrl(value);
  return parsed && SAFE_HTTP_SCHEMES.has(parsed.protocol) ? parsed.href : null;
}

/**
 * Open external URL in the appropriate context
 * - Electron: uses shell.openExternal via IPC (opens on local machine)
 * - WebUI: uses window.open in client browser (opens on remote client)
 *
 * 在适当的环境中打开外部链接
 * - Electron: 通过 IPC 调用 shell.openExternal（在本地机器打开）
 * - WebUI: 使用 window.open 在客户端浏览器打开（在远程客户端打开）
 */
export const openExternalUrl = async (url: string): Promise<void> => {
  if (!url) return;
  const base = typeof window !== 'undefined' ? window.location.href : undefined;
  if (!isSafeExternalUrl(url, base)) return;

  window.open(url.trim(), '_blank', 'noopener,noreferrer');
};
