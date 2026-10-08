import Tabs from '@/renderer/components/base/WorkbenchTabs';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import React, { useState } from 'react';
import { afterEach, describe, expect, it } from 'vitest';

function Fixture() {
  const [active, setActive] = useState('details');
  return (
    <Tabs aria-label='Artifact inspector' activeTab={active} onChange={setActive}>
      <Tabs.TabPane key='details' title='Details'>
        Metadata
      </Tabs.TabPane>
      <Tabs.TabPane key='unavailable' title='Unavailable' disabled />
      <Tabs.TabPane key='versions' title='Versions'>
        Version history
      </Tabs.TabPane>
    </Tabs>
  );
}
describe('Arco header keyboard adapter', () => {
  afterEach(cleanup);
  it('names the real header, preserves panel IDs and lets arrows select through Arco without an extra state authority', () => {
    render(<Fixture />);
    const header = screen.getByRole('tablist', { name: 'Artifact inspector' });
    const tabs = within(header).getAllByRole('tab');
    expect(tabs.map((tab) => tab.tabIndex)).toEqual([0, -1, -1]);
    expect(within(header).queryByRole('tabpanel')).not.toBeInTheDocument();
    tabs[0].focus();
    fireEvent.keyDown(tabs[0], { key: 'ArrowRight' });
    expect(tabs[2]).toHaveFocus();
    expect(tabs[2]).toHaveAttribute('aria-selected', 'true');
    expect(tabs.map((tab) => tab.tabIndex)).toEqual([-1, -1, 0]);
    const panel = screen.getByRole('tabpanel', { name: 'Versions' });
    expect(panel.id).toBe(tabs[2].getAttribute('aria-controls'));
    expect(panel).toHaveTextContent('Version history');
    fireEvent.keyDown(tabs[2], { key: 'Home' });
    expect(tabs[0]).toHaveFocus();
    expect(tabs[0]).toHaveAttribute('aria-selected', 'true');
  });
});
