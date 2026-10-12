/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { Steps } from '@arco-design/web-react';
import type { StepsProps } from '@arco-design/web-react/es/Steps';
import classNames from 'classnames';
import React from 'react';

/**
 * 步骤条组件属性 / Steps component props
 */
export interface XScienceStepsProps extends StepsProps {
  /** 额外的类名 / Additional class name */
  className?: string;
}

/**
 * 步骤条组件 / Steps component
 *
 * 基于 Arco Design Steps 的封装，提供统一的样式主题
 * Wrapper around Arco Design Steps with unified theme styling
 *
 * @features
 * - 自定义品牌色主题 / Custom brand color theme
 * - 完成态的特殊样式处理 / Special styling for finished state
 * - 完整的 Arco Steps API 支持 / Full Arco Steps API support
 *
 * @example
 * ```tsx
 * // 基本用法 / Basic usage
 * <XScienceSteps current={1}>
 *   <XScienceSteps.Step title="步骤1" description="这是描述" />
 *   <XScienceSteps.Step title="步骤2" description="这是描述" />
 *   <XScienceSteps.Step title="步骤3" description="这是描述" />
 * </XScienceSteps>
 *
 * // 垂直步骤条 / Vertical steps
 * <XScienceSteps current={1} direction="vertical">
 *   <XScienceSteps.Step title="步骤1" description="描述" />
 *   <XScienceSteps.Step title="步骤2" description="描述" />
 * </XScienceSteps>
 *
 * // 带图标的步骤条 / Steps with icons
 * <XScienceSteps current={1}>
 *   <XScienceSteps.Step title="完成" icon={<IconCheck />} />
 *   <XScienceSteps.Step title="进行中" icon={<IconLoading />} />
 *   <XScienceSteps.Step title="待处理" icon={<IconClock />} />
 * </XScienceSteps>
 *
 * // 迷你版步骤条 / Mini steps
 * <XScienceSteps current={1} size="small" type="dot">
 *   <XScienceSteps.Step title="步骤1" />
 *   <XScienceSteps.Step title="步骤2" />
 *   <XScienceSteps.Step title="步骤3" />
 * </XScienceSteps>
 * ```
 *
 * @see arco-override.css for custom styles (.x-science-steps)
 */
const XScienceSteps: React.FC<XScienceStepsProps> & { Step: typeof Steps.Step } = ({ className, ...props }) => {
  return <Steps {...props} className={classNames('x-science-steps', className)} />;
};

XScienceSteps.displayName = 'XScienceSteps';

// 导出子组件 / Export sub-component
XScienceSteps.Step = Steps.Step;

export default XScienceSteps;
