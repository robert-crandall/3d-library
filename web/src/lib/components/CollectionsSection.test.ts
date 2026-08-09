import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const post = vi.fn();
const put = vi.fn();
const del = vi.fn();
const get = vi.fn((..._args: unknown[]) => Promise.resolve({ data: [] }));
vi.mock('$lib/api/client', () => ({
  api: {
    GET: (...args: unknown[]) => get(...args),
    POST: (...args: unknown[]) => post(...args),
    PUT: (...args: unknown[]) => put(...args),
    DELETE: (...args: unknown[]) => del(...args)
  }
}));

import CollectionsSection from './CollectionsSection.svelte';
import { library } from '$lib/library.svelte';

beforeEach(() => {
  post.mockReset();
  put.mockReset();
  del.mockReset();
  get.mockReset();
  get.mockResolvedValue({ data: [] });
  library.reset();
  library.collections = [
    { id: 12, name: 'Dry box build', description: 'Every part of it.', modelCount: 4 },
    { id: 13, name: 'Gifts 2026', description: '', modelCount: 0 }
  ];
});

describe('CollectionsSection', () => {
  it('sends the name and the description when adding, and clears both', async () => {
    post.mockResolvedValue({ data: { id: 9 } });
    render(CollectionsSection);

    const name = screen.getByLabelText('New collection name') as HTMLInputElement;
    const description = screen.getByLabelText(
      'New collection description'
    ) as HTMLTextAreaElement;
    await fireEvent.input(name, { target: { value: '  Lamp  ' } });
    await fireEvent.input(description, { target: { value: '  Two parts.  ' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Add' }));

    // Trimmed, because the server trims too and a name that only differs by
    // spaces is the same name.
    await waitFor(() =>
      expect(post.mock.calls[0][1]).toEqual({ body: { name: 'Lamp', description: 'Two parts.' } })
    );
    // Both boxes empty afterwards: a description left in the form gets attached
    // to whatever is typed next.
    expect(name.value).toBe('');
    expect(description.value).toBe('');
  });

  it('shows the server refusal for a duplicate name and keeps what was typed', async () => {
    post.mockResolvedValue({
      error: { title: 'Unprocessable Entity', errors: [{ message: 'a collection called Lamp already exists' }] }
    });
    render(CollectionsSection);

    const name = screen.getByLabelText('New collection name') as HTMLInputElement;
    await fireEvent.input(name, { target: { value: 'Lamp' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Add' }));

    // The server's words, not ours: it is the only thing that knows the name is
    // taken, and it already says so.
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('already exists');
    // And the name is still there to edit, not thrown away with the refusal.
    expect(name.value).toBe('Lamp');
  });

  it('renames with both fields prefilled from the row', async () => {
    put.mockResolvedValue({ data: {} });
    render(CollectionsSection);

    await fireEvent.click(screen.getAllByRole('button', { name: 'Rename' })[0]);
    const name = screen.getByLabelText('New name for Dry box build') as HTMLInputElement;
    const description = screen.getByLabelText(
      'New description for Dry box build'
    ) as HTMLTextAreaElement;
    // Prefilled, not blank: a rename form that starts empty makes clearing the
    // description the default outcome of fixing a typo in the name.
    expect(name.value).toBe('Dry box build');
    expect(description.value).toBe('Every part of it.');

    await fireEvent.input(name, { target: { value: 'Dry box' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() =>
      expect(put.mock.calls[0][1]).toEqual({
        params: { path: { id: 12 } },
        body: { name: 'Dry box', description: 'Every part of it.' }
      })
    );
  });

  it('says the models survive before deleting, without counting them', async () => {
    del.mockResolvedValue({ data: {} });
    render(CollectionsSection);

    await fireEvent.click(screen.getByRole('button', { name: 'Delete Dry box build' }));
    // Deleting a collection is the one delete here that takes nothing away, and
    // saying so is what stops it reading like the category delete, which does.
    //
    // It says so without the count, because modelCount is roots-only: a
    // collection holding nothing but versions has a count of 0, and telling
    // someone "0 models are in this collection" while it visibly holds two is
    // worse than not counting at all.
    const dialog = await screen.findByRole('dialog');
    expect(dialog.textContent).toContain('does not delete the models in it');
    expect(dialog.textContent).not.toMatch(/\d+ models?/);

    // A string name matches the whole accessible name, so this is the dialog's
    // confirm button and not the row's "Delete Dry box build".
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() =>
      expect(del.mock.calls[0][1]).toEqual({ params: { path: { id: 12 } } })
    );
  });

  it('does not claim a version-only collection is empty', async () => {
    // modelCount is roots-only, by the same rule that keeps versions out of the
    // grid. So a collection whose only member is a version reads 0 here, and
    // any copy built on that number would be telling the user something the
    // model detail page contradicts.
    library.collections = [{ id: 12, name: 'Dry box build', description: '', modelCount: 0 }];
    render(CollectionsSection);

    await fireEvent.click(screen.getByRole('button', { name: 'Delete Dry box build' }));
    const dialog = await screen.findByRole('dialog');
    expect(dialog.textContent).not.toContain('0 models');
    expect(dialog.textContent).toContain('does not delete the models in it');
  });

  it('re-reads the store after a write rather than patching the row', async () => {
    put.mockResolvedValue({ data: {} });
    render(CollectionsSection);

    await fireEvent.click(screen.getAllByRole('button', { name: 'Rename' })[0]);
    await fireEvent.input(screen.getByLabelText('New name for Dry box build'), {
      target: { value: 'Dry box' }
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    // The sidebar reads the same store, and a rename that only changed this
    // list would leave the old name in it until a reload.
    await waitFor(() =>
      expect(get.mock.calls.some((call) => String(call[0]) === '/api/collections')).toBe(true)
    );
  });

  it('keeps the section usable when the request rejects outright', async () => {
    post.mockRejectedValue(new TypeError('Failed to fetch'));
    render(CollectionsSection);

    await fireEvent.input(screen.getByLabelText('New collection name'), {
      target: { value: 'Lamp' }
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Add' }));

    expect((await screen.findByRole('alert')).textContent).toContain('Could not reach the server');
    // Without the finally, a rejection leaves `busy` true and every control in
    // the section disabled until the page is reloaded.
    await waitFor(() =>
      expect(
        (screen.getByLabelText('New collection name') as HTMLInputElement).disabled
      ).toBe(false)
    );
    cleanup();
  });
});
