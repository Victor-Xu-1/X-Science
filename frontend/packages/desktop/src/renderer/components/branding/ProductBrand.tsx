import { PRODUCT_MARK_SRC, PRODUCT_NAME } from '@/common/config/productIdentity';
import React from 'react';
import './ProductBrand.css';

type ProductBrandProps = {
  className?: string;
  prominent?: boolean;
  'data-testid'?: string;
};

/** One original mark and one text identity across the workbench. */
export default function ProductBrand({ className = '', prominent = false, ...attributes }: ProductBrandProps) {
  return (
    <span {...attributes} className={`x-science-brand ${prominent ? 'x-science-brand--prominent' : ''} ${className}`}>
      <img className='x-science-brand__mark' src={PRODUCT_MARK_SRC} alt='' aria-hidden='true' />
      <span className='x-science-brand__name'>{PRODUCT_NAME}</span>
    </span>
  );
}
