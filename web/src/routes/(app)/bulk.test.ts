import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import LibraryPage from './+page.svelte';
import { nav } from '$lib/testing/nav.svelte';
import { library } from '$lib/library.svelte';

const get = vi.fn();
const post = vi.fn();
vi.mock('$lib/api/client', () => ({
  api: {
    GET: (...args: unknown[]) => get(...args),
    POST: (...args: unknown[]) => post(...args)
  }
}));

vi.mock('$app/state', async () => ({ page: (await import('$lib/testing/nav.svelte')).nav }));
vi.mock('$app/navigation', () => ({ goto: () => Promise.resolve() }));

const models = [
  { id: 1, name: 'Benchy', fileCount: 1, totalSize: 1024, createdAt: '2026-01-01T00:00:00Z' },
  { id: 2, name: 'Gridfinity', fileCount: 2, totalSize: 2048, createdAt: '2026-01-02T00:00:00Z' },
  { id: 3, name: 'Whistle', fileCount: 1, totalSize: 512, createdAt: '2026-01-03T00:00:00Z' }
];

const tags = [{ id: 7, name: 'printed', modelCount: 0 }];
const categories = [
  { id: 5, name: 'Functional', color: '#2f62d8', description: '', modelCount: 0 }
];
const collections = [{ id: 9, name: 'To print', description: '', modelCount: 0 }];

/** Answers the model list with an envelope and everything else with a bare
 *  list. The taxonomy the dialogs offer is seeded on the store directly,
 *  because the layout is what refreshes it and these tests render the page. */
function answers(items = models) {
  return (path: unknown) =>
    Promise.resolve(
      String(path).startsWith('/api/models')
        ? { data: { items, total: items.length, page: 1, pageSize: 24 } }
        : { data: [] }
    );
}

/** The bar button and the dialog's submit share a verb, which is right in the
 *  UI and ambiguous in a query, so dialog lookups are scoped to the dialog. */
function inDialog() {
  return within(screen.getByRole('dialog'));
}

function tile(name: string) {
  return screen.getByRole('link', { name: new RegExp(name) });
}

/** One ctrl-click. mousedown first, because that is the order a browser sends
 *  them and the tile's duplicate-gesture guard resets on it. */
async function pick(name: string, init: MouseEventInit = { ctrlKey: true }) {
  const el = tile(name);
  await fireEvent.mouseDown(el);
  await fireEvent.click(el, init);
}

/*
  What a weaker version of this file would miss: asserting the bar appears after
  a ctrl-click would pass against a page that kept the selection across a
  filter change, which is the failure this milestone is most likely to ship -
  the user selects three models, filters to a category that excludes them, and
  presses Delete on a set they can no longer see.
*/
describe('library page bulk actions', () => {
  beforeEach(() => {
    nav.url = new URL('http://localhost/');
    get.mockReset();
    get.mockImplementation(answers());
    library.reset();
    library.tags = tags;
    library.categories = categories;
    library.collections = collections;
    post.mockReset();
    post.mockImplementation(() => Promise.resolve({ data: undefined, response: { status: 200 } }));
  });

  // Absent, not disabled: four dead buttons above an untouched grid are four
  // things to explain, and the bar appearing is the feedback that the click
  // landed.
  it('has no bulk bar until something is selected', async () => {
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    expect(screen.queryByRole('button', { name: 'Add tags' })).toBeNull();

    await pick('Benchy');
    expect(await screen.findByText('1 selected')).toBeTruthy();
  });

  it('toggles a tile off with a second ctrl-click', async () => {
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    await pick('Benchy');
    await pick('Gridfinity');
    expect(await screen.findByText('2 selected')).toBeTruthy();

    await pick('Benchy');
    expect(await screen.findByText('1 selected')).toBeTruthy();
  });

  // Shift extends from the last tile touched, over the order on screen.
  it('extends a range with shift-click', async () => {
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    await pick('Benchy');
    await pick('Whistle', { shiftKey: true });

    expect(await screen.findByText('3 selected')).toBeTruthy();
  });

  it('drops the selection on Clear', async () => {
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    await pick('Benchy');
    await fireEvent.click(await screen.findByRole('button', { name: 'Clear' }));

    expect(screen.queryByText('1 selected')).toBeNull();
  });

  // The one that matters. A selection that survives a filter change is a
  // Delete button pointed at models the user can no longer see.
  it('drops the selection when the filter changes', async () => {
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    await pick('Benchy');
    expect(await screen.findByText('1 selected')).toBeTruthy();

    nav.url = new URL('http://localhost/?categoryId=5');

    await waitFor(() => expect(screen.queryByText('1 selected')).toBeNull());
  });

  it('drops the selection when the page changes', async () => {
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    await pick('Benchy');
    expect(await screen.findByText('1 selected')).toBeTruthy();

    nav.url = new URL('http://localhost/?page=2');

    await waitFor(() => expect(screen.queryByText('1 selected')).toBeNull());
  });

  it('sends the selected ids when tags are applied', async () => {
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    await pick('Benchy');
    await pick('Whistle');
    await fireEvent.click(await screen.findByRole('button', { name: 'Add tags' }));
    await fireEvent.click(await screen.findByRole('checkbox', { name: 'printed' }));
    await fireEvent.click(inDialog().getByRole('button', { name: 'Add tags' }));

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/api/models/bulk/tags', {
        body: { modelIds: [1, 3], tagIds: [7] }
      })
    );
  });

  it('sends the selected ids when a category is applied', async () => {
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    await pick('Gridfinity');
    await fireEvent.click(await screen.findByRole('button', { name: 'Recategorize' }));
    await fireEvent.change(await screen.findByRole('radio', { name: /Functional/ }));
    await fireEvent.click(inDialog().getByRole('button', { name: 'Recategorize' }));

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/api/models/bulk/category', {
        body: { modelIds: [2], categoryId: 5 }
      })
    );
  });

  it('sends the selected ids when a collection is applied', async () => {
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    await pick('Gridfinity');
    await fireEvent.click(await screen.findByRole('button', { name: 'Add to collection' }));
    await fireEvent.change(await screen.findByRole('radio', { name: /To print/ }));
    await fireEvent.click(screen.getByRole('button', { name: 'Add' }));

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/api/models/bulk/collection', {
        body: { modelIds: [2], collectionId: 9 }
      })
    );
  });

  // The grid, the sidebar counts and the badges all change, and under a filter
  // the models can leave the view entirely, so the server's answer is the only
  // honest one.
  it('re-reads the library after an action lands', async () => {
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });
    const before = get.mock.calls.filter((c) => String(c[0]).startsWith('/api/models')).length;

    await pick('Benchy');
    await fireEvent.click(await screen.findByRole('button', { name: 'Add tags' }));
    await fireEvent.click(await screen.findByRole('checkbox', { name: 'printed' }));
    await fireEvent.click(inDialog().getByRole('button', { name: 'Add tags' }));

    await waitFor(() =>
      expect(
        get.mock.calls.filter((c) => String(c[0]).startsWith('/api/models')).length
      ).toBeGreaterThan(before)
    );
  });

  // A failed action leaves the selection alone: the models are still there and
  // still the ones the user meant, so taking the selection away would make
  // retrying mean re-selecting.
  it('keeps the dialog and the selection when an action is refused', async () => {
    post.mockImplementation(() =>
      Promise.resolve({
        error: { title: 'error', errors: [{ message: 'unknown tag' }] },
        response: { status: 422 }
      })
    );
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    await pick('Benchy');
    await fireEvent.click(await screen.findByRole('button', { name: 'Add tags' }));
    await fireEvent.click(await screen.findByRole('checkbox', { name: 'printed' }));
    await fireEvent.click(inDialog().getByRole('button', { name: 'Add tags' }));

    expect(await screen.findByText(/unknown tag/)).toBeTruthy();
    expect(screen.getByText('1 selected')).toBeTruthy();
  });

  // A dialog that outlives its selection is a dialog whose submit sends an
  // empty modelIds, and in the delete case one whose sentence counts models
  // that are no longer on screen. A weaker version of this test would only
  // check the bar went, which passes with the dialog still up.
  it('closes an open dialog when the filter changes', async () => {
    post.mockImplementation(() =>
      Promise.resolve({ data: { models: 1, versions: 0, files: 1 }, response: { status: 200 } })
    );
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    for (const open of ['Add tags', 'Recategorize', 'Add to collection', 'Delete']) {
      await pick('Benchy');
      await fireEvent.click(await screen.findByRole('button', { name: open }));
      expect(await screen.findByRole('dialog')).toBeTruthy();

      nav.url = new URL(`http://localhost/?q=${encodeURIComponent(open)}`);
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
      expect(screen.queryByText('1 selected')).toBeNull();
    }
  });

  // openapi-fetch rejects instead of returning an error when the request never
  // reaches the server. Without the finally the busy flag stays set, and every
  // button in the dialog - Cancel included - is disabled forever.
  it('recovers when the request never reaches the server', async () => {
    post.mockImplementation(() => Promise.reject(new Error('offline')));
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    await pick('Benchy');
    await fireEvent.click(await screen.findByRole('button', { name: 'Add tags' }));
    await fireEvent.click(await screen.findByRole('checkbox', { name: 'printed' }));
    await fireEvent.click(inDialog().getByRole('button', { name: 'Add tags' }));

    expect(await screen.findByText('Could not reach the server.')).toBeTruthy();
    const cancel = inDialog().getByRole('button', { name: 'Cancel' }) as HTMLButtonElement;
    expect(cancel.disabled).toBe(false);
  });

  // Cancelling leaves the message behind, so opening the next dialog has to
  // clear it or it reports a failure the user did not just cause.
  it('does not carry an error into the next dialog', async () => {
    post.mockImplementation(() =>
      Promise.resolve({
        error: { errors: [{ message: 'library: invalid request: unknown tag' }] },
        response: { status: 422 }
      })
    );
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    await pick('Benchy');
    await fireEvent.click(await screen.findByRole('button', { name: 'Add tags' }));
    await fireEvent.click(await screen.findByRole('checkbox', { name: 'printed' }));
    await fireEvent.click(inDialog().getByRole('button', { name: 'Add tags' }));
    expect(await screen.findByText(/unknown tag/)).toBeTruthy();

    await fireEvent.click(inDialog().getByRole('button', { name: 'Cancel' }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Recategorize' }));

    expect(screen.queryByText(/unknown tag/)).toBeNull();
  });

  it('asks before deleting and says what it will destroy', async () => {
    post.mockImplementation((path: string) =>
      Promise.resolve(
        String(path).endsWith('delete-preview')
          ? { data: { models: 2, versions: 1, files: 5 }, response: { status: 200 } }
          : { data: undefined, response: { status: 200 } }
      )
    );
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    await pick('Benchy');
    await pick('Gridfinity');
    await fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));


    expect(
      await screen.findByText(
        '2 models, 1 version, and all 5 files will be deleted. This cannot be undone.'
      )
    ).toBeTruthy();
    // Still nothing deleted. The preview is a question, not a step.
    expect(post.mock.calls.every((c) => String(c[0]).endsWith('delete-preview'))).toBe(true);
  });
});
