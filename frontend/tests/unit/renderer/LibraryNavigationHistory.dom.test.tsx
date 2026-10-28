import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router';
import { afterEach, beforeEach, expect, it } from 'vitest';
import { NavigationHistoryProvider, useNavigationHistory } from '@/renderer/hooks/context/NavigationHistoryContext';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

let returnForTest: (() => void) | undefined;
function Controls() {
  const location = useLocation();
  const navigate = useNavigate();
  const history = useNavigationHistory() as ReturnType<typeof useNavigationHistory> & {
    returnFromLibrary?: () => void;
  };
  returnForTest = () => history?.returnFromLibrary?.();
  return (
    <>
      <output aria-label='location'>{location.pathname + location.search + location.hash}</output>
      <button onClick={() => void navigate('/library')}>Library</button>
      <button onClick={() => void navigate('/library?item=one&project=other')}>Filter</button>
      <button onClick={() => history?.returnFromLibrary?.()}>Return</button>
    </>
  );
}
function mount(initial: string) {
  return render(
    <MemoryRouter initialEntries={[initial]}>
      <NavigationHistoryProvider>
        <Controls />
      </NavigationHistoryProvider>
    </MemoryRouter>
  );
}
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('alice');
  sessionStorage.clear();
});
afterEach(cleanup);

it('returns from Library filters to the exact retained workspace route and refresh fallback', async () => {
  const original = '/conversation/retained?q=draft#anchor';
  const first = mount(original);
  fireEvent.click(screen.getByText('Library'));
  fireEvent.click(screen.getByText('Filter'));
  first.unmount();
  mount('/library?item=one');
  fireEvent.click(screen.getByText('Return'));
  await waitFor(() => expect(screen.getByLabelText('location')).toHaveTextContent(original));
});

it('isolates refresh return targets by account and rejects an external or stale owner path', async () => {
  const first = mount('/conversation/alice');
  fireEvent.click(screen.getByText('Library'));
  const oldAccountReturn = returnForTest;
  first.unmount();
  act(() => setRendererAccountOwner('bob'));
  sessionStorage.setItem('x-science:library-return:bob', 'https://example.invalid/escape');
  mount('/library');
  act(() => oldAccountReturn?.());
  expect(screen.getByLabelText('location')).toHaveTextContent('/library');
  fireEvent.click(screen.getByText('Return'));
  await waitFor(() => expect(screen.getByLabelText('location')).toHaveTextContent('/guid'));
});
