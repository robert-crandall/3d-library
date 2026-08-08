import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import LibraryPage from './+page.svelte';

const get = vi.fn();
vi.mock('$lib/api/client', () => ({ api: { GET: (...args: unknown[]) => get(...args) } }));

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

describe('library page', () => {
  it('renders a tile per model with its name, file count and size', async () => {
    get.mockResolvedValue({ data: [model, { ...model, id: 2, name: 'Gridfinity', fileCount: 1 }] });
    render(LibraryPage);

    expect(await screen.findByRole('heading', { name: 'Benchy' })).toBeTruthy();
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
    const dialog = screen.getByRole('form', { name: 'Upload a model' });
    await fireEvent.change(within(dialog).getByLabelText('Files'), {
      target: { files: [new File(['solid'], 'clip.stl')] }
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Upload' }));

    const reload = await within(dialog).findByRole('button', { name: 'Reload library' });
    const before = get.mock.calls.length;
    await fireEvent.click(reload);

    await waitFor(() => expect(get.mock.calls.length).toBe(before + 1));
    expect(screen.queryByRole('form', { name: 'Upload a model' })).toBeNull();
    vi.unstubAllGlobals();
  });

  it('opens the upload dialog from the header button', async () => {
    get.mockResolvedValue({ data: [model] });
    render(LibraryPage);
    await screen.findByRole('heading', { name: 'Benchy' });

    await fireEvent.click(screen.getByRole('button', { name: 'Upload' }));

    expect(screen.getByRole('form', { name: 'Upload a model' })).toBeTruthy();
  });

  // The seam that matters: a finished upload has to appear in the grid without
  // a reload, and the dialog has to close.
  it('adds an uploaded model to the grid', async () => {
    get.mockResolvedValue({ data: [] });
    const fetch = vi.fn(async (url: string, init?: RequestInit) => {
      if (!init) {
        return {
          ok: true,
          json: async () => ({ id: 9, name: 'Cable clip', fileCount: 1, totalSize: 2048 })
        } as Response;
      }
      return { ok: true, status: 201, json: async () => ({ id: 9, name: 'Cable clip' }) } as Response;
    });
    vi.stubGlobal('fetch', fetch);

    render(LibraryPage);
    await fireEvent.click(await screen.findByRole('button', { name: 'Upload your first model' }));

    const dialog = screen.getByRole('form', { name: 'Upload a model' });
    await fireEvent.change(within(dialog).getByLabelText('Files'), {
      target: { files: [new File(['solid'], 'clip.stl')] }
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Upload' }));

    await waitFor(() => expect(screen.getByRole('heading', { name: 'Cable clip' })).toBeTruthy());
    expect(screen.getByText('1 file · 2.0 KB')).toBeTruthy();
    expect(screen.queryByRole('form', { name: 'Upload a model' })).toBeNull();
    vi.unstubAllGlobals();
  });
});
