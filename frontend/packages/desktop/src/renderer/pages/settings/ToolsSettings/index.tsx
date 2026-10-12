/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * ToolsSettings renders X-Science MCP runtime tools as an XScience settings module.
 */

import React from 'react';
import { XScienceMcpSettingsContent } from './XScienceMcpSettings';
import SettingsPageWrapper from '../components/SettingsPageWrapper';

const ToolsSettings: React.FC = () => {
  return (
    <SettingsPageWrapper>
      <div className='settings-tools-page flex flex-col'>
        <XScienceMcpSettingsContent />
      </div>
    </SettingsPageWrapper>
  );
};

export default ToolsSettings;
