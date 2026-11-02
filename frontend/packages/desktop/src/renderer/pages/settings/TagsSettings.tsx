import React from 'react';
import SettingsPageWrapper from './components/SettingsPageWrapper';
import { TagsWorkbench } from './tags/TagsWorkbench';
import './tags/tags.css';

export default function TagsSettings() {
  return (
    <SettingsPageWrapper className='settings-tags-route'>
      <TagsWorkbench />
    </SettingsPageWrapper>
  );
}
