import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import LibraryPage from './+page.svelte';
import { nav } from '$lib/testing/nav.svelte';

const get = vi.fn();
vi.mock('$lib/api/client', () => ({ api: { GET: (...args: unknown[]) => get(...args) } }));

// The filter is the URL's, so a test that wants a filtered library says so by
// setting the URL rather than by poking at component state. `nav` is reactive,
// so assigning to it mid-test is a navigation the page notices.
vi.mock('$app/state', async () => ({ page: (await import('$lib/testing/nav.svelte')).nav }));
const goto = vi.fn((..._args: unknown[]) => Promise.resolve());
vi.mock('$app/navigation', () => ({ goto: (...args: unknown[]) => goto(...args) }));

const model = {
  id: 1,
  name: 'Benchy',
  fileCount: 3,
  totalSize: 1024 * 1024 * 12,
  createdAt: '2026-01-01T00:00:00Z'
};

/** A list response. The endpoint returns an envelope rather than a bare array,
 *  because the count line and the pager need the total and the page served. */
function listed(
  models: unknown[] = [],
  extra: { total?: number; page?: number; pageSize?: number } = {}
) {
  return {
    data: {
      items: models,
      total: extra.total ?? models.length,
      page: extra.page ?? 1,
      pageSize: extra.pageSize ?? 24
    }
  };
}

/** Answers the model list with an envelope and everything else - the taxonomy
 *  the shared store reads - with a bare list. */
function answers(models: unknown[] = [], extra?: { total?: number; page?: number }) {
  return (path: unknown) =>
    Promise.resolve(String(path).startsWith('/api/models') ? listed(models, extra) : { data: [] });
}

// No shared reset: every test sets its own implementation, and a top-level
// beforeEach that resets the mock makes vitest report the deliberately-rejected
// promise below as an unhandled error instead of letting the component catch it.

/** The page reads the model list and the shared taxonomy store reads four more
 *  endpoints, all through the same mock. Counting only the model reads keeps
 *  these assertions about the thing under test. */
function modelReads() {
  return get.mock.calls.filter((call) => String(call[0]).startsWith('/api/models')).length;
}

describe('library page', () => {
  beforeEach(() => {
    nav.url = new URL('http://localhost/');
  });

  it('renders a tile per model with its name, file count and size', async () => {
    get.mockImplementation(answers([model, { ...model, id: 2, name: 'Gridfinity', fileCount: 1 }]));
    render(LibraryPage);

    expect(await screen.findByRole('heading', { name: 'Benchy' })).toBeTruthy();
    // The href, not just the presence of a link: this is the only way into the
    // model page, and a tile that renders but points at the wrong place looks
    // identical in every other assertion.
    expect(screen.getByRole('link', { name: /Benchy/ }).getAttribute('href')).toBe('/models/1');
    expect(screen.getByText('3 files · 12 MB')).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Gridfinity' })).toBeTruthy();
    expect(screen.getByText('1 file · 12 MB')).toBeTruthy();
    expect(screen.getAllByRole('listitem')).toHaveLength(2);
  });

  // The first screen a new user sees. A blank area under the header would read
  // as a failed load, so the empty state has to say something.
  it('shows an empty state instead of a blank grid', async () => {
    get.mockImplementation(answers([]));
    render(LibraryPage);

    expect(await screen.findByText('Nothing here yet')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Upload your first model' })).toBeTruthy();
    expect(screen.queryAllByRole('listitem')).toHaveLength(0);
  });

  it('reports a failed load rather than showing an empty library', async () => {
    get.mockResolvedValue({ error: { detail: 'boom' } });
    render(LibraryPage);

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('boom');
    // Critically not the empty state: "you have no models" and "we could not
    // ask" are different things and must not look the same.
    expect(screen.queryByText('Nothing here yet')).toBeNull();
  });

  it('reports an unreachable server', async () => {
    // Promise.reject rather than an async function that throws: vitest reports
    // the latter as an unhandled error before the component awaits it.
    get.mockImplementation(() => Promise.reject(new TypeError('Failed to fetch')));
    render(LibraryPage);

    expect((await screen.findByRole('alert')).textContent).toContain('Could not reach the server');
  });

  // Both of the ways an upload could disagree with a load are closed by the
  // same rule, so there is no reconciliation to get right: you can only add to
  // a library you have actually seen. Without it, a load that lands after an
  // upload either erases the new model or is itself thrown away, erasing
  // everything that was already there.
  it('does not offer Upload until the library has loaded', async () => {
    let finishLoad!: (value: unknown) => void;
    get.mockImplementation(() => new Promise((resolve) => (finishLoad = resolve)));
    render(LibraryPage);

    expect((screen.getByRole('button', { name: 'Upload' }) as HTMLButtonElement).disabled).toBe(
      true
    );

    finishLoad(listed([model]));
    await screen.findByRole('heading', { name: 'Benchy' });
    expect((screen.getByRole('button', { name: 'Upload' }) as HTMLButtonElement).disabled).toBe(
      false
    );
  });

  it('does not offer Upload when the library failed to load', async () => {
    get.mockResolvedValue({ error: { detail: 'boom' } });
    render(LibraryPage);

    await screen.findByRole('alert');
    expect((screen.getByRole('button', { name: 'Upload' }) as HTMLButtonElement).disabled).toBe(
      true
    );
    // The way back in is to load the library, not to upload over it.
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  // Closing a dialog that could not tell what the server did has to re-read the
  // library. Otherwise Upload is available again with the question still open,
  // and the answer the user guesses at is a second copy.
  it('re-reads the library when an upload could not be confirmed', async () => {
    get.mockImplementation(answers([]));
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new TypeError('Failed to fetch')))
    );

    render(LibraryPage);
    await fireEvent.click(await screen.findByRole('button', { name: 'Upload your first model' }));
    const dialog = screen.getByRole('dialog', { name: 'Upload a model' });
    await fireEvent.change(within(dialog).getByLabelText('Files'), {
      target: { files: [new File(['solid'], 'clip.stl')] }
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Upload' }));

    const reload = await within(dialog).findByRole('button', { name: 'Reload library' });
    const before = modelReads();
    await fireEvent.click(reload);

    await waitFor(() => expect(modelReads()).toBe(before + 1));
    expect(screen.queryByRole('dialog', { name: 'Upload a model' })).toBeNull();
    vi.unstubAllGlobals();
  });

  // The reload is what settles whether the model exists, so Upload has to stay
  // shut for its whole duration - not only on the very first load. Otherwise
  // there is a window, right after asking for the reload, where the user can
  // upload the same model a second time.
  it('does not offer Upload while the confirming reload is in flight', async () => {
    get.mockImplementation(answers([]));
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new TypeError('Failed to fetch')))
    );

    render(LibraryPage);
    await fireEvent.click(await screen.findByRole('button', { name: 'Upload your first model' }));
    const dialog = screen.getByRole('dialog', { name: 'Upload a model' });
    await fireEvent.change(within(dialog).getByLabelText('Files'), {
      target: { files: [new File(['solid'], 'clip.stl')] }
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Upload' }));

    let finishReload!: (value: unknown) => void;
    // Only the model read is held open. The taxonomy reads answer immediately,
    // because what this test is about is the grid not being uploadable while
    // its own contents are unknown.
    get.mockImplementation((path: string) =>
      String(path).startsWith('/api/models')
        ? new Promise((resolve) => (finishReload = resolve))
        : Promise.resolve({ data: [] })
    );
    await fireEvent.click(await within(dialog).findByRole('button', { name: 'Reload library' }));

    expect((screen.getByRole('button', { name: 'Upload' }) as HTMLButtonElement).disabled).toBe(
      true
    );

    finishReload(listed());
    await waitFor(() =>
      expect((screen.getByRole('button', { name: 'Upload' }) as HTMLButtonElement).disabled).toBe(
        false
      )
    );
    vi.unstubAllGlobals();
  });

  it('opens the upload dialog from the header button', async () => {
    get.mockImplementation(answers([model]));
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    await fireEvent.click(screen.getByRole('button', { name: 'Upload' }));

    expect(screen.getByRole('dialog', { name: 'Upload a model' })).toBeTruthy();
  });

  // The seam that matters: a finished upload has to appear in the grid without
  // a reload, and the dialog has to close. The grid re-reads rather than
  // prepending the response, because under a filter the new model - which has
  // no category and no tags yet - may not belong in the list it was added from.
  it('re-reads the library after an upload', async () => {
    let uploaded = false;
    get.mockImplementation((path: string) => {
      if (!String(path).startsWith('/api/models')) return Promise.resolve({ data: [] });
      return Promise.resolve(
        listed(uploaded ? [{ id: 9, name: 'Cable clip', fileCount: 1, totalSize: 2048 }] : [])
      );
    });
    const fetch = vi.fn(async (url: string, init?: RequestInit) => {
      if (!init) {
        return {
          ok: true,
          json: async () => ({ id: 9, name: 'Cable clip', fileCount: 1, totalSize: 2048 })
        } as Response;
      }
      uploaded = true;
      return { ok: true, status: 201, json: async () => ({ id: 9, name: 'Cable clip' }) } as Response;
    });
    vi.stubGlobal('fetch', fetch);

    render(LibraryPage);
    await fireEvent.click(await screen.findByRole('button', { name: 'Upload your first model' }));

    const dialog = screen.getByRole('dialog', { name: 'Upload a model' });
    await fireEvent.change(within(dialog).getByLabelText('Files'), {
      target: { files: [new File(['solid'], 'clip.stl')] }
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Upload' }));

    await waitFor(() => expect(screen.getByRole('heading', { name: 'Cable clip' })).toBeTruthy());
    expect(screen.getByText('1 file · 2.0 KB')).toBeTruthy();
    expect(screen.queryByRole('dialog', { name: 'Upload a model' })).toBeNull();
    vi.unstubAllGlobals();
  });
});

// Filtering is entirely the URL's: the sidebar links, and this page reads what
// the link said. Anything else would need the two to agree by other means.
describe('library page filtering', () => {
  beforeEach(() => {
    nav.url = new URL('http://localhost/');
  });

  it('asks the server for exactly the filter in the URL', async () => {
    nav.url = new URL('http://localhost/?categoryId=3&tagId=8');
    get.mockImplementation(answers([]));
    render(LibraryPage);

    await waitFor(() => expect(get).toHaveBeenCalledWith('/api/models?categoryId=3&tagId=8'));
  });

  // A filter that matches nothing is not an empty library, and the new-user
  // empty state here would be a lie plus an Upload button that adds a model
  // this filter would not show.
  it('separates an empty filter from an empty library', async () => {
    nav.url = new URL('http://localhost/?uncategorized=true');
    get.mockImplementation(answers([]));
    render(LibraryPage);

    expect(await screen.findByText('Nothing matches this filter')).toBeTruthy();
    expect(screen.queryByText('Nothing here yet')).toBeNull();
    expect(screen.getByRole('link', { name: 'Show all models' }).getAttribute('href')).toBe('/');
  });

  // The heading has to name the filter. Without it a filtered grid and a small
  // library look the same, and the user reads three tiles as their whole
  // collection.
  it('names the active filter and offers a way out of it', async () => {
    nav.url = new URL('http://localhost/?uncategorized=true');
    get.mockImplementation(answers([model]));
    render(LibraryPage);

    expect(await screen.findByRole('heading', { name: 'Uncategorized' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Clear filter' }).getAttribute('href')).toBe('/');
  });

  // Two clicks in the sidebar are two GETs, and the second can answer first.
  // Without a guard the grid ends up showing the first filter's models under the
  // second filter's heading, which is the worst kind of wrong: it looks right.
  it('keeps the newest filter when an older answer arrives late', async () => {
    const pending: Record<string, (value: unknown) => void> = {};
    get.mockImplementation((path: string) =>
      String(path).startsWith('/api/models')
        ? new Promise((resolve) => (pending[String(path)] = resolve))
        : Promise.resolve({ data: [] })
    );

    nav.url = new URL('http://localhost/?categoryId=3');
    render(LibraryPage);
    await waitFor(() => expect(pending['/api/models?categoryId=3']).toBeTruthy());

    nav.url = new URL('http://localhost/?categoryId=4');
    await waitFor(() => expect(pending['/api/models?categoryId=4']).toBeTruthy());

    pending['/api/models?categoryId=4'](listed([{ ...model, id: 2, name: 'Toys model' }]));
    expect(await screen.findByRole('heading', { name: 'Toys model' })).toBeTruthy();

    pending['/api/models?categoryId=3'](listed([{ ...model, name: 'Functional model' }]));
    // One turn of the event loop is all the stale reply needs to overwrite the
    // grid, so wait for it rather than asserting straight away.
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(screen.queryByRole('heading', { name: 'Functional model' })).toBeNull();
    expect(screen.getByRole('heading', { name: 'Toys model' })).toBeTruthy();
  });

  it('offers no way out when nothing is filtered', async () => {
    get.mockImplementation(answers([model]));
    render(LibraryPage);

    expect(await screen.findByRole('heading', { name: 'All models' })).toBeTruthy();
    expect(screen.queryByRole('link', { name: 'Clear filter' })).toBeNull();
  });
});

// Search, sort and paging are the same mechanism as filtering - a URL the page
// reads - so what is worth testing is the part that is not: the count line and
// the pager come from the response, and the response is allowed to disagree
// with the URL.
describe('library page, searching and paging', () => {
  beforeEach(() => {
    nav.url = new URL('http://localhost/');
    goto.mockClear();
    // Only the recorded calls, not the implementation: every test here sets its
    // own answer first thing, and clearing the implementation would leave a
    // pending promise from the previous test unhandled.
    get.mockClear();
  });

  it('asks for exactly what the URL says', async () => {
    get.mockImplementation(answers([model]));
    nav.url = new URL('http://localhost/?q=bin&sort=name&page=2&categoryId=3');
    render(LibraryPage);

    await waitFor(() => expect(modelReads()).toBe(1));
    expect(get.mock.calls.at(-1)?.[0]).toBe('/api/models?categoryId=3&q=bin&sort=name&page=2');
  });

  // The count is of matches, not of tiles: a full first page of a 60-model
  // search and a full page of the whole library look identical without it.
  it('counts the matches and says which page of how many', async () => {
    get.mockImplementation(answers(Array.from({ length: 24 }, (_, i) => ({ ...model, id: i + 1 })), { total: 60, page: 2 }));
    nav.url = new URL('http://localhost/?page=2');
    render(LibraryPage);

    expect(await screen.findByText('60 models · page 2 of 3')).toBeTruthy();
  });

  // One page is the common case and "page 1 of 1" is noise, and one model is
  // not "1 models".
  it('says neither a page number nor a plural it does not need', async () => {
    get.mockImplementation(answers([model]));
    render(LibraryPage);

    expect(await screen.findByText('1 model')).toBeTruthy();
    expect(screen.queryByLabelText('Pagination')).toBeNull();
  });

  // Page 1 of 0 is what ceil(0/24) gives you, and it is on screen the moment a
  // search matches nothing.
  it('does not offer a page count of zero when nothing matches', async () => {
    get.mockImplementation(answers([], { total: 0 }));
    nav.url = new URL('http://localhost/?q=nothing');
    render(LibraryPage);

    expect(await screen.findByText('0 models')).toBeTruthy();
  });

  // Naming the term matters after a debounce, when the box may already hold
  // something else, and the way out keeps the filter you were searching inside
  // rather than dumping you at the whole library.
  it('names the term when a search matches nothing, and clears only the search', async () => {
    get.mockImplementation(answers([], { total: 0 }));
    nav.url = new URL('http://localhost/?q=bin&categoryId=3&sort=name&page=2');
    render(LibraryPage);

    expect(await screen.findByRole('heading', { name: 'No models match “bin”' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Clear search' }).getAttribute('href')).toBe(
      '/?categoryId=3&sort=name'
    );
    // Not the filter's empty state, and certainly not the empty library's.
    expect(screen.queryByText('Nothing matches this filter')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Upload your first model' })).toBeNull();
  });

  // An empty filter is still not an empty search, so the earlier state must not
  // swallow this one. The way out of it drops the filter and nothing else: a
  // hardcoded "/" here would quietly reset an ordering the user chose, and this
  // is the only one of the three exits that is easy to write that way.
  it('keeps the filter and the library empty states apart from the search one', async () => {
    get.mockImplementation(answers([], { total: 0 }));
    nav.url = new URL('http://localhost/?categoryId=3&sort=name');
    render(LibraryPage);

    expect(await screen.findByText('Nothing matches this filter')).toBeTruthy();
    expect(screen.queryByRole('heading', { name: /No models match/ })).toBeNull();
    expect(screen.getByRole('link', { name: 'Show all models' }).getAttribute('href')).toBe(
      '/?sort=name'
    );
  });

  it('offers only the direction that exists at each end', async () => {
    const full = Array.from({ length: 24 }, (_, i) => ({ ...model, id: i + 1 }));
    get.mockImplementation(answers(full, { total: 60, page: 1 }));
    render(LibraryPage);

    const nav1 = await screen.findByLabelText('Pagination');
    expect(within(nav1).queryByRole('link', { name: 'Previous' })).toBeNull();
    expect(within(nav1).getByRole('link', { name: 'Next' }).getAttribute('href')).toBe('/?page=2');

    get.mockImplementation(answers(full, { total: 60, page: 3 }));
    nav.url = new URL('http://localhost/?page=3');
    await waitFor(() =>
      expect(
        within(screen.getByLabelText('Pagination')).queryByRole('link', { name: 'Next' })
      ).toBeNull()
    );
    expect(
      within(screen.getByLabelText('Pagination'))
        .getByRole('link', { name: 'Previous' })
        .getAttribute('href')
    ).toBe('/?page=2');
  });

  // The URL can ask for a page that does not exist - a bookmark taken before
  // models were deleted, or a hand-edited address. The server serves the last
  // page instead of failing, and the pager has to agree with what is on screen:
  // built from the URL it would offer 98 and 100 of a two-page library.
  it('builds the pager from the page the server served, not the one asked for', async () => {
    get.mockImplementation(
      answers(Array.from({ length: 12 }, (_, i) => ({ ...model, id: i + 1 })), {
        total: 36,
        page: 2
      })
    );
    nav.url = new URL('http://localhost/?page=99&q=bin');
    render(LibraryPage);

    const pager = await screen.findByLabelText('Pagination');
    expect(within(pager).getByRole('link', { name: 'Previous' }).getAttribute('href')).toBe(
      '/?q=bin'
    );
    expect(within(pager).queryByRole('link', { name: 'Next' })).toBeNull();
    expect(screen.getByText('36 models · page 2 of 2')).toBeTruthy();
  });

  // Paging keeps the search and the ordering, or page 2 of a search is page 2
  // of the library.
  it('keeps the search and the ordering in the pager links', async () => {
    get.mockImplementation(
      answers(Array.from({ length: 24 }, (_, i) => ({ ...model, id: i + 1 })), {
        total: 60,
        page: 2
      })
    );
    nav.url = new URL('http://localhost/?q=bin&sort=name&page=2&tagId=8');
    render(LibraryPage);

    const pager = await screen.findByLabelText('Pagination');
    expect(within(pager).getByRole('link', { name: 'Next' }).getAttribute('href')).toBe(
      '/?tagId=8&q=bin&sort=name&page=3'
    );
    expect(within(pager).getByRole('link', { name: 'Previous' }).getAttribute('href')).toBe(
      '/?tagId=8&q=bin&sort=name'
    );
  });

  // The mirror of the stale-success case, and the one that is easy to leave
  // out: a filter that fails after a later one has already painted must not put
  // an error over a grid that loaded fine. Nothing on screen would explain it,
  // and Try again would re-read the filter that worked.
  it('ignores a stale read that fails after a newer one succeeded', async () => {
    const pending: Record<string, (value: unknown) => void> = {};
    const rejects: Record<string, (reason: unknown) => void> = {};
    get.mockImplementation((path: string) =>
      String(path).startsWith('/api/models')
        ? new Promise((resolve, reject) => {
            pending[String(path)] = resolve;
            rejects[String(path)] = reject;
          })
        : Promise.resolve({ data: [] })
    );

    nav.url = new URL('http://localhost/?q=old');
    render(LibraryPage);
    await waitFor(() => expect(pending['/api/models?q=old']).toBeTruthy());

    nav.url = new URL('http://localhost/?q=new');
    await waitFor(() => expect(pending['/api/models?q=new']).toBeTruthy());
    pending['/api/models?q=new'](listed([model]));
    expect(await screen.findByRole('heading', { name: 'Benchy' })).toBeTruthy();

    rejects['/api/models?q=old'](new TypeError('Failed to fetch'));
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getByRole('heading', { name: 'Benchy' })).toBeTruthy();
  });

  it('shows the ordering the URL asked for', async () => {
    get.mockImplementation(answers([model]));
    nav.url = new URL('http://localhost/?sort=name-desc');
    render(LibraryPage);

    await screen.findByRole('heading', { name: 'Benchy' });
    expect((screen.getByLabelText('Sort') as HTMLSelectElement).value).toBe('name-desc');
  });

  // Re-sorting page 3 of a search and staying on page 3 shows a different
  // twenty-four models with no relation to what was on screen, so the sort
  // control returns to the first page.
  it('goes back to the first page when the ordering changes', async () => {
    get.mockImplementation(answers([model], { total: 60, page: 3 }));
    nav.url = new URL('http://localhost/?q=bin&page=3');
    render(LibraryPage);

    await screen.findByRole('heading', { name: 'Benchy' });
    await fireEvent.change(screen.getByLabelText('Sort'), { target: { value: 'name' } });

    expect(goto).toHaveBeenCalledTimes(1);
    expect(goto.mock.calls[0][0]).toBe('/?q=bin&sort=name');
  });
});
