/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function formatDate(value: string | null, language: string, unknownLabel: string): string {
  if (!value) return unknownLabel;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString(language);
}
