import { Tabs as ArcoTabs, type TabsProps } from '@arco-design/web-react';
import React from 'react';
import ArcoTabListHeader from './ArcoTabListHeader';

type WorkbenchTabsProps = TabsProps & { 'aria-label': string };
function WorkbenchTabs({ 'aria-label': label, ...props }: WorkbenchTabsProps) {
  return (
    <ArcoTabs
      {...props}
      renderTabHeader={
        props.renderTabHeader ??
        ((tabProps, Header) => (
          <ArcoTabListHeader
            label={label}
            orientation={props.tabPosition === 'left' || props.tabPosition === 'right' ? 'vertical' : 'horizontal'}
          >
            <Header {...tabProps} />
          </ArcoTabListHeader>
        ))
      }
    />
  );
}
export default Object.assign(WorkbenchTabs, { TabPane: ArcoTabs.TabPane });
