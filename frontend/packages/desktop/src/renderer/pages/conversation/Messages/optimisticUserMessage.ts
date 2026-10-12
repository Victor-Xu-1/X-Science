/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

export const X_SCIENCE_OPTIMISTIC_USER_MESSAGE_PREFIX = 'xscience-optimistic-user:';

export function isXScienceOptimisticUserMessage(messageId: string | null | undefined): boolean {
  return Boolean(messageId?.startsWith(X_SCIENCE_OPTIMISTIC_USER_MESSAGE_PREFIX));
}
