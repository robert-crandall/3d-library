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

const model = {
  id: 1,
  name: 'Benchy',
  fileCount: 3,
  totalSize: 1024 * 1024 * 12,
  createdAt: '2026-01-01T00:00:00Z'
};

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
    get.mockResolvedValue({ data: [model, { ...model, id: 2, name: 'Gridfinity', fileCount: 1 }] });
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
    get.mockResolvedValue({ data: [] });
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

    finishLoad({ data: [model] });
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
    get.mockResolvedValue({ data: [] });
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
    get.mockResolvedValue({ data: [] });
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

    finishReload({ data: [] });
    await waitFor(() =>
      expect((screen.getByRole('button', { name: 'Upload' }) as HTMLButtonElement).disabled).toBe(
        false
      )
    );
    vi.unstubAllGlobals();
  });

  it('opens the upload dialog from the header button', async () => {
    get.mockResolvedValue({ data: [model] });
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
      return Promise.resolve({
        data: uploaded ? [{ id: 9, name: 'Cable clip', fileCount: 1, totalSize: 2048 }] : []
      });
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
    get.mockResolvedValue({ data: [] });
    render(LibraryPage);

    await waitFor(() => expect(get).toHaveBeenCalledWith('/api/models?categoryId=3&tagId=8'));
  });

  // A filter that matches nothing is not an empty library, and the new-user
  // empty state here would be a lie plus an Upload button that adds a model
  // this filter would not show.
  it('separates an empty filter from an empty library', async () => {
    nav.url = new URL('http://localhost/?uncategorized=true');
    get.mockResolvedValue({ data: [] });
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
    get.mockResolvedValue({ data: [model] });
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

    pending['/api/models?categoryId=4']({ data: [{ ...model, id: 2, name: 'Toys model' }] });
    expect(await screen.findByRole('heading', { name: 'Toys model' })).toBeTruthy();

    pending['/api/models?categoryId=3']({ data: [{ ...model, name: 'Functional model' }] });
    // One turn of the event loop is all the stale reply needs to overwrite the
    // grid, so wait for it rather than asserting straight away.
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(screen.queryByRole('heading', { name: 'Functional model' })).toBeNull();
    expect(screen.getByRole('heading', { name: 'Toys model' })).toBeTruthy();
  });

  it('offers no way out when nothing is filtered', async () => {
    get.mockResolvedValue({ data: [model] });
    render(LibraryPage);

    expect(await screen.findByRole('heading', { name: 'All models' })).toBeTruthy();
    expect(screen.queryByRole('link', { name: 'Clear filter' })).toBeNull();
  });
});
