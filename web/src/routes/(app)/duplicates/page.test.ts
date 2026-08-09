import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { tick } from 'svelte';
import DuplicatesPage from './+page.svelte';

const get = vi.fn();
const post = vi.fn();
const del = vi.fn();
vi.mock('$lib/api/client', () => ({
  api: {
    GET: (...args: unknown[]) => get(...args),
    POST: (...args: unknown[]) => post(...args),
    DELETE: (...args: unknown[]) => del(...args)
  }
}));

type Status = {
  running?: boolean;
  hashed?: number;
  total?: number;
  pending?: number;
  scannedAt?: string | null;
  error?: string;
};

/** A duplicates response. Defaults are "scanned, fully hashed, nothing found",
 *  so each test only says the one thing it is about. */
function answer(groups: unknown[] = [], status: Status = {}) {
  return {
    data: {
      groups,
      status: {
        running: false,
        hashed: 0,
        total: 0,
        pending: 0,
        scannedAt: '2026-03-12T00:00:00Z',
        error: '',
        ...status
      }
    }
  };
}

const group = {
  hash: 'abc123',
  size: 1024 * 1024 * 4,
  reclaimable: 1024 * 1024 * 4,
  files: [
    { fileId: 1, filename: 'benchy.stl', modelId: 10, modelName: 'Benchy' },
    { fileId: 2, filename: '3dbenchy.stl', modelId: 11, modelName: 'Benchy (again)' }
  ]
};

describe('duplicates page', () => {
  // Every test sets its own answers and several count calls, so a shared
  // history would let a previous test satisfy an assertion about this one.
  // `resetAllMocks`, not `clearAllMocks`: clearing keeps implementations, so a
  // test that never configures POST silently inherits the previous test's - and
  // passes for a reason it does not state.
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it('lists every copy in a group with the model it belongs to', async () => {
    get.mockResolvedValue(answer([group]));
    render(DuplicatesPage);

    // The model name, not just the filename: the two copies are usually named
    // the same, so a list of filenames alone cannot tell them apart - which is
    // the entire reason this screen shows a model per file.
    expect(await screen.findByRole('link', { name: 'Benchy' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Benchy (again)' })).toBeTruthy();
    expect(screen.getByText(/· benchy\.stl/)).toBeTruthy();
    expect(screen.getByText(/· 3dbenchy\.stl/)).toBeTruthy();
    expect(screen.getByText(/4\.0 MB to reclaim/)).toBeTruthy();
  });

  it('says nothing was found only when the whole library has been read', async () => {
    get.mockResolvedValue(answer([], { pending: 0 }));
    render(DuplicatesPage);

    expect(await screen.findByText(/No duplicate files/)).toBeTruthy();
  });

  it('does not claim a clean library while files are still unread', async () => {
    // The failure this pins: an empty list plus a timestamp looks exactly like
    // "nothing found", and a run that hit an unreadable blob - or any upload
    // since the last scan - produces exactly that. Reporting it as clean tells
    // the user their library is deduplicated when nobody has looked.
    get.mockResolvedValue(answer([], { pending: 3 }));
    render(DuplicatesPage);

    expect(await screen.findByText(/3 files have not been read yet/)).toBeTruthy();
    expect(screen.queryByText(/No duplicate files/)).toBeNull();
  });

  it('offers a first scan, not a rescan, before anything has been scanned', async () => {
    get.mockResolvedValue(answer([], { scannedAt: null }));
    render(DuplicatesPage);

    expect(await screen.findByRole('button', { name: 'Scan for duplicates' })).toBeTruthy();
    // Not "no duplicates": nothing has been compared, so there is no answer yet.
    expect(screen.queryByText(/No duplicate files/)).toBeNull();
  });

  it('shows progress and keeps the button disabled while a scan runs', async () => {
    get.mockResolvedValue(answer([], { running: true, hashed: 4, total: 10, pending: 6 }));
    render(DuplicatesPage);

    expect(await screen.findByText(/Reading 10 files · 4 done/)).toBeTruthy();
    const button = screen.getByRole('button', { name: 'Scan again' });
    expect((button as HTMLButtonElement).disabled).toBe(true);
  });

  it('surfaces a scan that could not read some files', async () => {
    get.mockResolvedValue(answer([], { pending: 1, error: '1 files could not be read' }));
    render(DuplicatesPage);

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('could not be read');
  });

  it('confirms before deleting, then deletes that one file and re-reads', async () => {
    get.mockResolvedValue(answer([group]));
    del.mockResolvedValue({ data: undefined, error: undefined });
    render(DuplicatesPage);

    await screen.findByRole('link', { name: 'Benchy' });
    await fireEvent.click(screen.getAllByRole('button', { name: 'Delete' })[0]);

    // The dialog names the file and says what survives, because there is no
    // undo: a bare "are you sure" leaves the user guessing which copy goes.
    const dialog = await screen.findByRole('dialog');
    expect(dialog.textContent).toContain('benchy.stl');
    expect(dialog.textContent).toContain('The other 1 copy stays');

    await fireEvent.click(screen.getByRole('button', { name: 'Delete file' }));

    await waitFor(() => expect(del).toHaveBeenCalled());
    expect(del.mock.calls[0][1]).toEqual({ params: { path: { id: 10, fileId: 1 } } });
    // Re-read, not spliced locally: a group that drops to one file stops being
    // a group and only the server knows that.
    await waitFor(() => expect(get.mock.calls.length).toBeGreaterThan(1));
  });

  it('keeps the dialog open and shows the server’s words when a delete fails', async () => {
    get.mockResolvedValue(answer([group]));
    del.mockResolvedValue({ error: { detail: 'that file is gone' } });
    render(DuplicatesPage);

    await screen.findByRole('link', { name: 'Benchy' });
    await fireEvent.click(screen.getAllByRole('button', { name: 'Delete' })[0]);
    await fireEvent.click(await screen.findByRole('button', { name: 'Delete file' }));

    expect(await screen.findByText('that file is gone')).toBeTruthy();
    expect(screen.getByRole('dialog')).toBeTruthy();
  });

  it('starts a scan and reads the result', async () => {
    get.mockResolvedValue(answer([], { scannedAt: null }));
    post.mockResolvedValue({ error: undefined });
    render(DuplicatesPage);

    await fireEvent.click(await screen.findByRole('button', { name: 'Scan for duplicates' }));

    await waitFor(() => expect(post).toHaveBeenCalledWith('/api/duplicates/scan'));
    await waitFor(() => expect(get.mock.calls.length).toBeGreaterThan(1));
  });

  // The state a crash mid-scan leaves behind: hashes committed, watermark never
  // written. A weaker page orders "never scanned" above the group list and hides
  // real duplicates behind copy saying nothing has been compared.
  it('shows groups it already found even with no scan timestamp', async () => {
    get.mockResolvedValue(answer([group], { scannedAt: null, pending: 4 }));
    render(DuplicatesPage);

    expect(await screen.findByRole('link', { name: 'Benchy' })).toBeTruthy();
    expect(screen.queryByText(/Nothing has been compared yet/)).toBeNull();
    // And says the list may be short, because it is.
    expect(screen.getByText(/4 files have not been read yet/)).toBeTruthy();
  });

  it('drops a stale clean verdict when a refresh fails', async () => {
    // The dangerous stale render: the first read says the library is clean, the
    // next one fails, and leaving the old answer up tells the user a library
    // nobody has looked at since is deduplicated.
    get.mockResolvedValueOnce(answer([], { pending: 0 }));
    post.mockResolvedValue({ error: undefined });
    render(DuplicatesPage);
    expect(await screen.findByText(/No duplicate files/)).toBeTruthy();

    get.mockResolvedValue({ error: { detail: 'server went away' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Scan again' }));

    expect(await screen.findByText('server went away')).toBeTruthy();
    await waitFor(() => expect(screen.queryByText(/No duplicate files/)).toBeNull());
  });

  // The poll survives more than one failure. Inferring "keep going" from the
  // last response cannot work, because a failed read throws that response away:
  // the second failure would see nothing running and quietly stop, leaving a
  // scan that is still going to look finished until someone reloads.
  it('keeps polling through two failed reads and settles on the result', async () => {
    vi.useFakeTimers();
    try {
      get.mockResolvedValueOnce(answer([], { scannedAt: null }));
      post.mockResolvedValue({ error: undefined });
      render(DuplicatesPage);
      await vi.waitFor(() =>
        expect(screen.getByRole('button', { name: 'Scan for duplicates' })).toBeTruthy()
      );

      // The scan is running from here on. Both reads after it fail, and the
      // second one is the interesting one: the page has already thrown away the
      // response that said "running", so anything inferring the decision from
      // the last answer stops here and the scan looks finished.
      get.mockResolvedValue({ error: { detail: 'blip' } });
      await fireEvent.click(screen.getByRole('button', { name: 'Scan for duplicates' }));
      await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(2));

      await vi.advanceTimersByTimeAsync(800);
      await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(3));

      get.mockResolvedValue(answer([group]));
      await vi.advanceTimersByTimeAsync(800);
      await vi.waitFor(() => expect(screen.queryByText('blip')).toBeNull());
      expect(screen.getByRole('link', { name: 'Benchy' })).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it('offers a way back when a read fails', async () => {
    // The failed answer is dropped, and the scan button goes with it. Without a
    // retry here the only way out of a transient failure is reloading the page.
    get.mockResolvedValueOnce({ error: { detail: 'blip' } });
    render(DuplicatesPage);

    get.mockResolvedValue(answer([group]));
    await fireEvent.click(await screen.findByRole('button', { name: 'Try again' }));

    expect(await screen.findByRole('link', { name: 'Benchy' })).toBeTruthy();
  });

  // openapi-fetch resolves with an `error` for an HTTP error but rejects when
  // fetch itself fails. Unhandled, the rejection leaves the last answer onscreen
  // - so a library that has gone offline still reads "No duplicate files".
  it('drops a stale clean verdict when the network itself fails', async () => {
    get.mockResolvedValueOnce(answer([], { pending: 0 }));
    post.mockResolvedValue({ error: undefined });
    render(DuplicatesPage);
    expect(await screen.findByText(/No duplicate files/)).toBeTruthy();

    get.mockRejectedValue(new TypeError('Failed to fetch'));
    await fireEvent.click(screen.getByRole('button', { name: 'Scan again' }));

    expect(await screen.findByText(/Could not read the duplicate list/)).toBeTruthy();
    expect(screen.queryByText(/No duplicate files/)).toBeNull();
  });

  it('recovers the scan button when the network itself fails', async () => {
    get.mockResolvedValue(answer([], { scannedAt: null }));
    post.mockRejectedValue(new TypeError('Failed to fetch'));
    render(DuplicatesPage);

    await fireEvent.click(await screen.findByRole('button', { name: 'Scan for duplicates' }));
    expect(await screen.findByText(/Could not start the scan/)).toBeTruthy();

    // And the way out is a read, not a second POST: a dropped connection says
    // nothing about whether the server started scanning.
    await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    const button = await screen.findByRole('button', { name: 'Scan for duplicates' });
    // Enabled, explicitly. `getByRole` finds disabled buttons too, so asserting
    // it exists would pass with the page stuck mid-request.
    expect((button as HTMLButtonElement).disabled).toBe(false);
    expect(post).toHaveBeenCalledTimes(1);
  });

  it('does not leave a stale clean verdict up when the scan request fails', async () => {
    get.mockResolvedValue(answer([], { pending: 0 }));
    post.mockResolvedValue({ error: { detail: 'scan is already running' } });
    render(DuplicatesPage);
    expect(await screen.findByText(/No duplicate files/)).toBeTruthy();

    await fireEvent.click(screen.getByRole('button', { name: 'Scan again' }));

    expect(await screen.findByText('scan is already running')).toBeTruthy();
    expect(screen.queryByText(/No duplicate files/)).toBeNull();
  });

  it('does not poll when nothing is known to be running', async () => {
    vi.useFakeTimers();
    try {
      // The failure path retries only for a scan it knows about. Retrying every
      // failed read would make the consecutive-failure test above pass for the
      // wrong reason, and would hammer a server that is already unwell.
      get.mockResolvedValue({ error: { detail: 'blip' } });
      render(DuplicatesPage);
      await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(1));

      await vi.advanceTimersByTimeAsync(5000);
      expect(get).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('ignores a slow read that lands after a newer one', async () => {
    // Reads overlap - a poll, a retry and the read after a delete can all be in
    // flight. Without a generation the slowest wins, and an older clean answer
    // lands on top of a newer list of duplicates.
    let releaseSlow: (value: unknown) => void = () => {};
    const slow = new Promise((resolve) => (releaseSlow = resolve));
    get.mockResolvedValueOnce({ error: { detail: 'blip' } });
    get.mockReturnValueOnce(slow);
    get.mockResolvedValue(answer([group]));
    render(DuplicatesPage);

    const retry = await screen.findByRole('button', { name: 'Try again' });
    await fireEvent.click(retry);
    await fireEvent.click(retry);
    expect(await screen.findByRole('link', { name: 'Benchy' })).toBeTruthy();

    releaseSlow(answer([], { pending: 0 }));
    // Wait for the stale response to actually be processed before asserting -
    // otherwise this passes simply by looking too early, which is how it passed
    // with the generation guard removed.
    await slow;
    await new Promise((resolve) => setTimeout(resolve, 0));
    await tick();
    expect(screen.queryByText(/No duplicate files/)).toBeNull();
    expect(screen.getByRole('link', { name: 'Benchy' })).toBeTruthy();
  });

  it('does not let a read started before a scan put a stale answer back', async () => {
    // The interleaving: a delete kicks off a re-read, that read is slow, and the
    // user hits Scan again while it is still out. Dropping the answer is not
    // enough on its own - the read already in flight has to be retired too, or
    // it lands mid-request and puts "No duplicate files" back on a library
    // nobody has finished looking at.
    let releaseSlow: (value: unknown) => void = () => {};
    const slow = new Promise((resolve) => (releaseSlow = resolve));
    get.mockResolvedValueOnce(answer([group]));
    get.mockReturnValueOnce(slow);
    del.mockResolvedValue({ error: undefined });
    post.mockResolvedValue({ error: { detail: 'scan is already running' } });
    render(DuplicatesPage);

    await fireEvent.click((await screen.findAllByRole('button', { name: 'Delete' }))[0]);
    await fireEvent.click(await screen.findByRole('button', { name: 'Delete file' }));
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));

    await fireEvent.click(screen.getByRole('button', { name: 'Scan again' }));
    releaseSlow(answer([], { pending: 0 }));
    await slow;
    await new Promise((resolve) => setTimeout(resolve, 0));
    await tick();

    expect(screen.queryByText(/No duplicate files/)).toBeNull();
    expect(await screen.findByText('scan is already running')).toBeTruthy();
  });

  it('keeps the delete dialog usable when the network itself fails', async () => {
    get.mockResolvedValue(answer([group]));
    del.mockRejectedValue(new TypeError('Failed to fetch'));
    render(DuplicatesPage);

    await fireEvent.click((await screen.findAllByRole('button', { name: 'Delete' }))[0]);
    await fireEvent.click(await screen.findByRole('button', { name: 'Delete file' }));

    expect(await screen.findByText(/Could not delete that file/)).toBeTruthy();
    expect(screen.getByRole('dialog')).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Delete file' }) as HTMLButtonElement).disabled).toBe(
      false
    );
  });

  it('keeps polling through a failed read while a scan is running', async () => {
    vi.useFakeTimers();
    try {
      get.mockResolvedValueOnce(answer([], { running: true, total: 5, hashed: 1 }));
      render(DuplicatesPage);
      await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(1));

      // A single failed poll must not freeze the page mid-scan until a reload.
      get.mockResolvedValue({ error: { detail: 'blip' } });
      await vi.advanceTimersByTimeAsync(800);
      await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(2));
      await vi.advanceTimersByTimeAsync(800);
      await vi.waitFor(() => expect(get.mock.calls.length).toBeGreaterThan(2));
    } finally {
      vi.useRealTimers();
    }
  });

  it('reports a failed load instead of rendering an empty library', async () => {
    // Without this the page falls through to its "nothing found" copy and tells
    // the user their library is clean when the request never succeeded.
    get.mockResolvedValue({ error: { detail: 'nope' } });
    render(DuplicatesPage);

    expect(await screen.findByText('nope')).toBeTruthy();
    expect(screen.queryByText(/No duplicate files/)).toBeNull();
  });
});
