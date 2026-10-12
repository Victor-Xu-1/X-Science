import type { SpeechToTextConfig } from '@/common/types/provider/speech';
import type { IMcpServer, TProviderWithModel } from '@/common/config/storage';

export type GoogleClientSetting = {
  proxy?: string;
};

export type ImageGenerationModelSetting = TProviderWithModel & {
  switch?: boolean;
};

export type ClientBusinessSettingMap = {
  'google.config': GoogleClientSetting;
  'mcp.config': IMcpServer[] | undefined;
  'tools.imageGenerationModel': ImageGenerationModelSetting | undefined;
  'tools.speechToText': SpeechToTextConfig | undefined;
  'acp.promptTimeout': number | undefined;
  'xScience.session.defaultModelId': string | undefined;
  'xScience.session.subagentModelId': string | undefined;
  'xScience.session.effort': 'low' | 'medium' | 'high' | undefined;
  'xScience.sidebar.projectOrder': string[] | undefined;
};

export type ClientBusinessSettingKey = keyof ClientBusinessSettingMap;
