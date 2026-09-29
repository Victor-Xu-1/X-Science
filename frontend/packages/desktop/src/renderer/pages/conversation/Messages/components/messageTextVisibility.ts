/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import type { IMessageText } from '@/common/chat/chatLib';
import { decodeRoundSummary } from '@/common/chat/roundSummary';

/**
 * Keep transcript virtualization and MessageText on the same visibility rule.
 * A row whose renderer returns null is still measured by react-virtuoso and
 * produces a zero-sized-item error on every transcript refresh.
 */
export const hasRenderableMessageText = (message: IMessageText): boolean => {
  if (
    message.position === 'left' &&
    message.terminal_status === 'completed' &&
    !message.terminal_superseded &&
    decodeRoundSummary(message.round_summary)
  )
    return true;
  const content = message.content.content;
  if (typeof content !== 'string' || content.trim().length === 0) return false;

  // Historical interrupted responses may contain only protocol delimiters.
  // Exclude the row at the shared visibility boundary, not only its renderer,
  // while retaining user-authored input and meaningful scientific symbols.
  if (message.position !== 'right' && /[<>]/u.test(content) && /^[\s<>/|"'\\]+$/u.test(content)) {
    return false;
  }

  return !(
    message.position !== 'right' &&
    message.terminal_status === 'cancelled' &&
    content.trim() === 'user_cancelled'
  );
};
