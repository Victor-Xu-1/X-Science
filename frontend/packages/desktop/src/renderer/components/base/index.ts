/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * XScience 基础组件库统一导出 / XScience base components unified exports
 *
 * 提供所有基础组件和类型的统一导出入口
 * Provides unified export entry for all base components and types
 */

// ==================== 组件导出 / Component Exports ====================

export { default as XScienceModal } from './XScienceModal';
export { default as XScienceCollapse } from './XScienceCollapse';
export { default as XScienceSelect } from './XScienceSelect';
export { default as XScienceScrollArea } from './XScienceScrollArea';
export { default as XScienceSteps } from './XScienceSteps';

// ==================== 类型导出 / Type Exports ====================

// XScienceModal 类型 / XScienceModal types
export type {
  ModalSize,
  ModalHeaderConfig,
  ModalFooterConfig,
  ModalContentStyleConfig,
  XScienceModalProps,
} from './XScienceModal';
export { MODAL_SIZES } from './XScienceModal';

// XScienceCollapse 类型 / XScienceCollapse types
export type { XScienceCollapseProps, XScienceCollapseItemProps } from './XScienceCollapse';

// XScienceSelect 类型 / XScienceSelect types
export type { XScienceSelectProps } from './XScienceSelect';

// XScienceSteps 类型 / XScienceSteps types
export type { XScienceStepsProps } from './XScienceSteps';
