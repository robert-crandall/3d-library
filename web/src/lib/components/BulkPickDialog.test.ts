import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import BulkPickDialog from './BulkPickDialog.svelte';

function open(overrides: Record<string, unknown> = {}) {
  const onpick = vi.fn();
  const oncancel = vi.fn();
  render(BulkPickDialog, {
    title: 'Recategorize',
    prompt: 'Move 3 models into one category.',
    confirm: 'Recategorize',
    choices: [
      { id: 1, name: 'Functional', color: '#2f62d8' },
      { id: 2, name: 'Toys' }
    ],
    empty: 'No categories yet.',
    onpick,
    oncancel,
    ...overrides
  });
  return { onpick, oncancel };
}

/*
  What a weaker version of this file would miss: asserting only that the radios
  render would pass against a dialog that applied on selection rather than on
  the button, which turns a mis-click into an applied recategorize over
  everything selected instead of a wrong radio the user can move.
*/
describe('BulkPickDialog', () => {
  it('does not apply until the button is pressed', async () => {
    const { onpick } = open();

    await fireEvent.change(screen.getByRole('radio', { name: /Toys/ }));
    expect(onpick).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByRole('button', { name: 'Recategorize' }));
    expect(onpick).toHaveBeenCalledWith(2);
  });

  // Pressing it with nothing chosen would send `undefined` as a category id,
  // which the server would reject with a message about a request the user never
  // knowingly made.
  it('will not submit with nothing chosen', () => {
    open();
    expect(
      (screen.getByRole('button', { name: 'Recategorize' }) as HTMLButtonElement).disabled
    ).toBe(true);
  });

  it('says so when there is nothing to pick', () => {
    open({ choices: [] });
    expect(screen.getByText('No categories yet.')).toBeTruthy();
    expect(screen.queryAllByRole('radio')).toHaveLength(0);
  });

  // Cancel is the escape hatch from a dialog opened by mistake. It must not
  // apply anything on the way out.
  it('reports a cancel without picking', async () => {
    const { onpick, oncancel } = open();

    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(oncancel).toHaveBeenCalledTimes(1);
    expect(onpick).not.toHaveBeenCalled();
  });

  it('shows a failure without closing', () => {
    open({ error: 'unknown category' });
    expect(screen.getByRole('alert').textContent).toContain('unknown category');
    expect(screen.getByRole('dialog')).toBeTruthy();
  });
});
