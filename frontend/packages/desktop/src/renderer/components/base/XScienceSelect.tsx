/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { Select } from '@arco-design/web-react';
import type { SelectProps } from '@arco-design/web-react';
import type { SelectHandle } from '@arco-design/web-react/es/Select/interface';
import classNames from 'classnames';
import React from 'react';
import { useSelectCloseFocus } from './useSelectCloseFocus';

/**
 * 自定义下拉选择组件属性 / Custom select component props
 */
type NativeSelectProps = Omit<SelectProps, 'size'>;
type NativeSelectSize = NonNullable<SelectProps['size']>;
type XScienceSelectSize = NativeSelectSize | 'middle';

export interface XScienceSelectProps extends NativeSelectProps {
  /** 额外的类名 / Additional class name */
  className?: string;
  /** 统一尺寸，新增 middle（32px）/ Unified size with additional "middle" (32px) */
  size?: XScienceSelectSize;
}

/**
 * 基础样式类名
 * 注意：主题相关样式（背景色、边框色）在 arco-override.css 的 .x-science-select 类中定义
 * Note: Theme-related styles (background, border colors) are defined in .x-science-select class in arco-override.css
 */
const BASE_CLASS = classNames(
  'x-science-select',
  '[&_.arco-select-view]:rounded-[4px]',
  '[&_.arco-select-view]:border',
  '[&_.arco-select-view]:border-solid',
  '[&_.arco-select-view]:border-arco-2',
  '[&_.arco-select-view]:shadow-none',
  '[&_.arco-select-view]:transition-colors',
  '[&_.arco-select-view:hover]:border-[var(--color-primary)]',
  '[&_.arco-select-view:focus-within]:border-[var(--color-primary)]',
  '[&_.arco-select-view-disabled]:bg-[var(--color-bg-2)]',
  '[&_.arco-select-view-disabled]:opacity-80'
);

/**
 * 默认的弹出层容器获取函数
 * 始终返回 document.body 以避免嵌套容器导致的 ResizeObserver 循环错误
 * Default popup container getter function
 * Always returns document.body to avoid ResizeObserver loop errors from nested containers
 */
const defaultGetPopupContainer = (): HTMLElement => {
  // 在浏览器环境下始终挂载到 body，避免嵌套容器导致 ResizeObserver 循环
  // Always mount popup to body in browsers to avoid nested-container ResizeObserver loops
  if (typeof document !== 'undefined' && document.body) {
    return document.body;
  }
  // SSR/测试环境降级返回占位，具体不会真正渲染
  // Fallback for SSR/tests – this code path shouldn't render popups
  return undefined as unknown as HTMLElement;
};

/**
 * 自定义下拉选择组件 / Custom select component
 *
 * 基于 Arco Design Select 的封装，提供统一的样式主题和弹出层处理
 * Wrapper around Arco Design Select with unified theme styling and popup handling
 *
 * @features
 * - 自动适配明暗主题 / Auto theme adaptation (light/dark)
 * - 弹出层挂载到 body，避免布局问题 / Popup mounted to body to avoid layout issues
 * - 统一的圆角和边框样式 / Unified border radius and border styles
 * - 完整的 Arco Select API 支持 / Full Arco Select API support
 *
 * @example
 * ```tsx
 * // 基本用法 / Basic usage
 * <XScienceSelect placeholder="请选择" style={{ width: 200 }}>
 *   <XScienceSelect.Option value="1">选项1</XScienceSelect.Option>
 *   <XScienceSelect.Option value="2">选项2</XScienceSelect.Option>
 * </XScienceSelect>
 *
 * // 多选 / Multiple selection
 * <XScienceSelect mode="multiple" placeholder="请选择多个">
 *   <XScienceSelect.Option value="1">选项1</XScienceSelect.Option>
 *   <XScienceSelect.Option value="2">选项2</XScienceSelect.Option>
 * </XScienceSelect>
 *
 * // 分组 / Grouped options
 * <XScienceSelect placeholder="请选择">
 *   <XScienceSelect.OptGroup label="分组1">
 *     <XScienceSelect.Option value="1">选项1</XScienceSelect.Option>
 *   </XScienceSelect.OptGroup>
 *   <XScienceSelect.OptGroup label="分组2">
 *     <XScienceSelect.Option value="2">选项2</XScienceSelect.Option>
 *   </XScienceSelect.OptGroup>
 * </XScienceSelect>
 * ```
 *
 * @see arco-override.css for theme-related styles (.x-science-select)
 */
const mapSizeToNative = (size?: XScienceSelectSize): NativeSelectSize | undefined => {
  if (!size) return undefined;
  if (size === 'middle') return 'default';
  return size;
};

type XScienceSelectComponent = React.ForwardRefExoticComponent<
  XScienceSelectProps & React.RefAttributes<SelectHandle>
> & {
  Option: typeof Select.Option;
  OptGroup: typeof Select.OptGroup;
};

const InternalSelect = React.forwardRef<SelectHandle, XScienceSelectProps>(
  ({ className, getPopupContainer, size = 'middle', ...rest }, ref) => {
    const normalizedSize = mapSizeToNative(size);
    const select = React.useRef<SelectHandle>(null);
    // Arco replaces this handle when its internal active option changes, even
    // without a wrapper render. Forward each original object, not a snapshot.
    const selectRef = React.useCallback<React.RefCallback<SelectHandle>>(
      (handle: SelectHandle | null) => {
        select.current = handle;
        if (typeof ref === 'function') {
          const cleanup = (ref as React.RefCallback<SelectHandle>)(handle);
          if (typeof cleanup === 'function') {
            return () => {
              if (select.current === handle) select.current = null;
              cleanup();
            };
          }
        } else if (ref) ref.current = handle;
      },
      [ref]
    );
    const trigger = rest.triggerProps?.trigger ?? rest.trigger ?? 'click';
    const clickOnly = trigger === 'click' || (Array.isArray(trigger) && trigger.length === 1 && trigger[0] === 'click');
    // A focus-triggered/custom owner may intentionally reopen or move focus.
    // Keep that owner's semantics instead of competing with it on close.
    const controlledVisible = 'popupVisible' in rest ? rest.popupVisible : rest.triggerProps?.popupVisible;
    const focus = useSelectCloseFocus(
      select,
      clickOnly && rest.triggerElement == null && !rest.onFocus,
      !!rest.disabled,
      controlledVisible
    );
    return (
      <Select
        ref={selectRef}
        size={normalizedSize}
        className={classNames(BASE_CLASS, className)}
        getPopupContainer={getPopupContainer || defaultGetPopupContainer}
        {...rest}
        onVisibleChange={(next) => {
          focus.visibleChange(next);
          rest.onVisibleChange?.(next);
        }}
        onKeyDown={(event) => {
          rest.onKeyDown?.(event);
          focus.keyDown(event);
        }}
      />
    );
  }
);

const XScienceSelect = InternalSelect as XScienceSelectComponent;

XScienceSelect.displayName = 'XScienceSelect';

// 导出子组件 / Export sub-components
XScienceSelect.Option = Select.Option;
XScienceSelect.OptGroup = Select.OptGroup;

export default XScienceSelect;
