import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import BulkTagsDialog from './BulkTagsDialog.svelte';

function open(overrides: Record<string, unknown> = {}) {
  const onapply = vi.fn();
  const oncancel = vi.fn();
  render(BulkTagsDialog, {
    tags: [
      { id: 4, name: 'printed' },
      { id: 5, name: 'wip' },
      { id: 6, name: 'gift' }
    ],
    count: 3,
    onapply,
    oncancel,
    ...overrides
  });
  return { onapply, oncancel };
}

/*
  What a weaker version of this file would miss: checking one box and asserting
  the call would pass against a dialog that sent only the last box touched. Tags
  are additive and adding two at once is the normal case, so the multi-select
  assertion is the point of the component existing separately from the radio one.
*/
describe('BulkTagsDialog', () => {
  it('sends every tag that was checked', async () => {
    const { onapply } = open();

    await fireEvent.click(screen.getByRole('checkbox', { name: 'printed' }));
    await fireEvent.click(screen.getByRole('checkbox', { name: 'gift' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Add tags' }));

    expect(onapply).toHaveBeenCalledWith([4, 6]);
  });

  it('drops a tag that was checked and unchecked', async () => {
    const { onapply } = open();

    await fireEvent.click(screen.getByRole('checkbox', { name: 'printed' }));
    await fireEvent.click(screen.getByRole('checkbox', { name: 'wip' }));
    await fireEvent.click(screen.getByRole('checkbox', { name: 'wip' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Add tags' }));

    expect(onapply).toHaveBeenCalledWith([4]);
  });

  // An empty list is a request that does nothing, reported as if it did.
  it('will not submit with nothing checked', () => {
    open();
    expect((screen.getByRole('button', { name: 'Add tags' }) as HTMLButtonElement).disabled).toBe(
      true
    );
  });

  // The user is about to change several models at once, so the prompt has to
  // say how many. "Add tags" on its own does not.
  it('says how many models it will change', () => {
    open({ count: 5 });
    expect(screen.getByText(/Add these tags to 5 models/)).toBeTruthy();
  });

  it('says so when there are no tags', () => {
    open({ tags: [] });
    expect(screen.getByText(/No tags yet/)).toBeTruthy();
    expect(screen.queryAllByRole('checkbox')).toHaveLength(0);
  });

  it('reports a cancel without applying', async () => {
    const { onapply, oncancel } = open();

    await fireEvent.click(screen.getByRole('checkbox', { name: 'printed' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(oncancel).toHaveBeenCalledTimes(1);
    expect(onapply).not.toHaveBeenCalled();
  });
});
