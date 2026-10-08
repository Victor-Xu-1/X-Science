import ProductBrand from '@/renderer/components/branding/ProductBrand';
import { cleanup, render, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it } from 'vitest';

afterEach(cleanup);

describe('shared X-Science brand', () => {
  it('renders the original mark without a duplicate accessible product label', () => {
    const { container } = render(<ProductBrand />);
    expect(screen.getByText('X-Science')).toBeVisible();
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    expect(container.querySelector('img')).toHaveAttribute('src', './branding/x-science-mark.png');
    expect(container.querySelector('img')).toHaveAttribute('aria-hidden', 'true');
  });

  it('keeps one identity and the same original image in prominent presentation', () => {
    const { container } = render(<ProductBrand prominent data-testid='brand' />);
    expect(screen.getByTestId('brand')).toHaveClass('x-science-brand--prominent');
    expect(screen.getAllByText('X-Science')).toHaveLength(1);
    expect(container.querySelector('img')).toHaveAttribute('src', './branding/x-science-mark.png');
  });
});
