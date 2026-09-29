import React from 'react';
import type { ItemProps } from 'react-virtuoso';

/** Keep message margins inside the measured row. Collapsed child margins are
 * invisible to Virtuoso's size observer and accumulate into incorrect seeks. */
export const MessageVirtualItem = React.forwardRef<HTMLDivElement, ItemProps<unknown> & { context?: unknown }>(
  ({ context: _context, item: _item, style, ...props }, ref) => (
    <div {...props} ref={ref} style={{ ...style, display: 'flow-root' }} />
  )
);
MessageVirtualItem.displayName = 'MessageVirtualItem';
