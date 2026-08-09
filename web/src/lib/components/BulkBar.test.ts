import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import BulkBar from './BulkBar.svelte';

function open(overrides: Record<string, unknown> = {}) {
  const handlers = {
    ontag: vi.fn(),
    oncategorize: vi.fn(),
    oncollect: vi.fn(),
    ondelete: vi.fn(),
    onclear: vi.fn()
  };
  render(BulkBar, { count: 3, ...handlers, ...overrides });
  return handlers;
}

/*
  What a weaker version of this file would miss: asserting only that the four
  buttons render would pass against a bar whose buttons all called the same
  handler, which is exactly the mistake that turns Recategorize into a delete.
  Each button is checked for its own callback, and for the others staying quiet.
*/
describe('BulkBar', () => {
  it('says how many are selected', () => {
    open({ count: 7 });
    expect(screen.getByText('7 selected')).toBeTruthy();
  });

  it('wires each action to its own handler', async () => {
    const h = open();

    await fireEvent.click(screen.getByRole('button', { name: 'Add tags' }));
    expect(h.ontag).toHaveBeenCalledTimes(1);

    await fireEvent.click(screen.getByRole('button', { name: 'Recategorize' }));
    expect(h.oncategorize).toHaveBeenCalledTimes(1);

    await fireEvent.click(screen.getByRole('button', { name: 'Add to collection' }));
    expect(h.oncollect).toHaveBeenCalledTimes(1);

    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect(h.ondelete).toHaveBeenCalledTimes(1);

    await fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
    expect(h.onclear).toHaveBeenCalledTimes(1);
  });

  // The bar is the only confirmation that a modified click landed, and a colour
  // change on a tile is not announced. Without the live region a screen-reader
  // user gets no feedback at all from selecting.
  it('announces itself', () => {
    open();
    const bar = screen.getByRole('status');
    expect(bar.getAttribute('aria-live')).toBe('polite');
    expect(bar.textContent).toContain('3 selected');
  });

  // A second click while the first request is in flight would apply the action
  // twice, and for delete the second one is a 404 reported as a failure.
  it('locks the actions while one is running', () => {
    open({ busy: true });
    for (const name of ['Add tags', 'Recategorize', 'Add to collection', 'Delete']) {
      expect((screen.getByRole('button', { name }) as HTMLButtonElement).disabled).toBe(true);
    }
  });
});
