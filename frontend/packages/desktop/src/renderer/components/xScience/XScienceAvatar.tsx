import React from 'react';
import { PRODUCT_MARK_SRC } from '@/common/config/productIdentity';

export const X_SCIENCE_AVATAR_SRC = PRODUCT_MARK_SRC;

export const isRobotAvatar = (value?: string | null): boolean => value?.trim() === '🤖';

type XScienceAvatarProps = {
  size?: number | string;
  className?: string;
  alt?: string;
};

/** Shared non-robot identity image for X-Science agents and runtime UI. */
const XScienceAvatar: React.FC<XScienceAvatarProps> = ({ size = 16, className = '', alt = '' }) => (
  <img
    src={X_SCIENCE_AVATAR_SRC}
    alt={alt}
    aria-hidden={alt ? undefined : true}
    className={`block object-contain ${className}`.trim()}
    style={{ width: size, height: size }}
  />
);

export default XScienceAvatar;
