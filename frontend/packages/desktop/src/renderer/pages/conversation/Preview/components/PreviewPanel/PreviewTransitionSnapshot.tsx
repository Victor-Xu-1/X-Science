import React from 'react';

export type PreviewScrollSnapshot = {
  scroll: Array<{ node: HTMLElement; top: number; left: number }>;
  grid: { node: HTMLElement; autoRows: string } | null;
};
const gridLocks = new WeakMap<HTMLElement, { users: number; original: string; pinned: string }>();
export function retainPreviewGrid(grid: PreviewScrollSnapshot['grid']): () => void {
  if (!grid) return () => {};
  let lock = gridLocks.get(grid.node);
  if (!lock) {
    lock = { users: 0, original: grid.node.style.gridAutoRows, pinned: grid.autoRows };
    gridLocks.set(grid.node, lock);
    grid.node.style.gridAutoRows = lock.pinned;
  }
  lock.users += 1;
  let released = false;
  return () => {
    if (released) return;
    released = true;
    if (--lock.users !== 0) return;
    if (grid.node.style.gridAutoRows === lock.pinned) grid.node.style.gridAutoRows = lock.original;
    gridLocks.delete(grid.node);
  };
}
type Props = React.PropsWithChildren<{ active: boolean; onSnapshot: (snapshot: PreviewScrollSnapshot) => void }>;

/** React's pre-mutation snapshot phase captures offsets before fullscreen
 * classes can clamp them. No render-phase DOM/ref side effects. */
export class PreviewTransitionSnapshot extends React.Component<
  Props,
  Record<string, never>,
  PreviewScrollSnapshot | null
> {
  private root = React.createRef<HTMLDivElement>();
  getSnapshotBeforeUpdate(previous: Props): PreviewScrollSnapshot | null {
    if (previous.active === this.props.active || !this.root.current) return null;
    let grid = this.root.current.parentElement;
    while (grid && getComputedStyle(grid).display !== 'grid') grid = grid.parentElement;
    const scroll = [this.root.current, ...this.root.current.querySelectorAll<HTMLElement>('*')]
      .filter((node) => node.scrollTop !== 0 || node.scrollLeft !== 0)
      .map((node) => ({ node, top: node.scrollTop, left: node.scrollLeft }));
    return { scroll, grid: grid ? { node: grid, autoRows: getComputedStyle(grid).gridAutoRows } : null };
  }
  componentDidUpdate(
    _previous: Props,
    _previousState: Record<string, never>,
    snapshot: PreviewScrollSnapshot | null
  ): void {
    if (snapshot) this.props.onSnapshot(snapshot);
  }
  render() {
    return (
      <div ref={this.root} style={{ display: 'contents' }}>
        {this.props.children}
      </div>
    );
  }
}

export function restorePreviewScroll(root: HTMLElement, snapshot: PreviewScrollSnapshot): void {
  for (const { node, top, left } of snapshot.scroll) {
    if (!node.isConnected || !root.contains(node)) continue;
    node.scrollTop = top;
    node.scrollLeft = left;
  }
}
