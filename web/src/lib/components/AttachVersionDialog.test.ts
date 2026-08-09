import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import AttachVersionDialog from './AttachVersionDialog.svelte';

const get = vi.fn();

vi.mock('$lib/api/client', () => ({
  api: { GET: (...args: unknown[]) => get(...args) }
}));

function model(id: number, name: string) {
  return { id, name, fileCount: 1, totalSize: 1024, createdAt: '2026-03-12T09:00:00Z' };
}

function page(items: ReturnType<typeof model>[], over: Record<string, number> = {}) {
  return { data: { items, total: items.length, page: 1, pageSize: 24, ...over } };
}

function open(overrides: Record<string, unknown> = {}) {
  const onattach = vi.fn();
  const oncancel = vi.fn();
  render(AttachVersionDialog, { parentId: 7, onattach, oncancel, ...overrides });
  return { onattach, oncancel };
}

/** The last query object the component sent to the list endpoint. */
function lastQuery() {
  const call = get.mock.calls.at(-1);
  return (call?.[1] as { params: { query: Record<string, string> } }).params.query;
}

describe('AttachVersionDialog', () => {
  beforeEach(() => {
    get.mockReset();
    get.mockResolvedValue(page([model(7, 'Filament Dry Box'), model(8, 'Desiccant Tray')]));
  });

  // Unsearched on open, so the common case - a library small enough to see at a
  // glance - needs no typing at all.
  it('lists your models on open', async () => {
    open();
    await waitFor(() => expect(screen.getByLabelText(/Desiccant Tray/)).toBeTruthy());
    expect(lastQuery()).toEqual({});
  });

  // Attaching a model to itself is refused by the server, but there is no
  // reason to offer it: the parent is the one candidate that is never valid.
  it('leaves the parent out of its own candidate list', async () => {
    open();
    await waitFor(() => expect(screen.getByLabelText(/Desiccant Tray/)).toBeTruthy());
    expect(screen.queryByLabelText(/Filament Dry Box/)).toBeNull();
  });

  // The list endpoint sends roots only and one page of 24, so search is the only
  // way to name the 25th model. A dialog without it would silently be unable to
  // attach most of a real library.
  it('searches for a model by name', async () => {
    vi.useFakeTimers();
    try {
      open();
      await vi.waitFor(() => expect(get).toHaveBeenCalled());
      get.mockResolvedValue(page([model(9, 'Hinge v1')]));
      await fireEvent.input(screen.getByLabelText('Search'), { target: { value: 'hinge' } });
      await vi.advanceTimersByTimeAsync(300);
      expect(lastQuery()).toEqual({ q: 'hinge' });
    } finally {
      vi.useRealTimers();
    }
  });

  // One request per pause, not one per keystroke. Without the debounce, typing
  // a six-letter word is six list requests.
  it('waits for a pause before searching', async () => {
    vi.useFakeTimers();
    try {
      open();
      await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(1));
      const box = screen.getByLabelText('Search');
      await fireEvent.input(box, { target: { value: 'h' } });
      await fireEvent.input(box, { target: { value: 'hi' } });
      await fireEvent.input(box, { target: { value: 'hin' } });
      expect(get).toHaveBeenCalledTimes(1);
      await vi.advanceTimersByTimeAsync(300);
      expect(get).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  // Nothing is attached until Add version is pressed, so a picked radio is a
  // choice you can still change or cancel.
  it('attaches the model you picked', async () => {
    const { onattach } = open();
    await waitFor(() => expect(screen.getByLabelText(/Desiccant Tray/)).toBeTruthy());
    expect((screen.getByRole('button', { name: 'Add version' }) as HTMLButtonElement).disabled).toBe(
      true
    );
    await fireEvent.click(screen.getByLabelText(/Desiccant Tray/));
    await fireEvent.click(screen.getByRole('button', { name: 'Add version' }));
    expect(onattach).toHaveBeenCalledWith(8);
  });

  // The server's refusal - "that model has versions of its own" - has to land
  // in the dialog that asked for it, because the page behind it is unchanged
  // and the user is about to pick something else.
  it('shows the server refusal without closing', async () => {
    open({ error: 'That model has versions of its own.' });
    await waitFor(() =>
      expect(screen.getByRole('alert').textContent).toContain('versions of its own')
    );
    expect(screen.getByRole('button', { name: 'Add version' })).toBeTruthy();
  });

  // An empty library and an empty search are different sentences: one says you
  // have nothing to attach, the other says this word matched nothing.
  it('tells an empty list from an empty search', async () => {
    get.mockResolvedValue(page([]));
    open();
    await waitFor(() => expect(screen.getByText(/no other models yet/)).toBeTruthy());

    vi.useFakeTimers();
    try {
      await fireEvent.input(screen.getByLabelText('Search'), { target: { value: 'nope' } });
      await vi.advanceTimersByTimeAsync(300);
    } finally {
      vi.useRealTimers();
    }
    await waitFor(() => expect(screen.getByText(/No models match that/)).toBeTruthy());
  });

  it('reports a failed search', async () => {
    get.mockResolvedValue({ error: { detail: 'nope' }, response: { status: 500 } });
    open();
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('nope'));
  });

  // Type "brack", then "bracket": the shorter search can answer second and
  // overwrite the longer one's results, so the list would show rows that do not
  // match what is in the box.
  it('ignores a search that is overtaken by a newer one', async () => {
    vi.useFakeTimers();
    try {
      let releaseSlow: (value: unknown) => void = () => {};
      get.mockReturnValueOnce(new Promise((resolve) => (releaseSlow = resolve)));
      open();

      get.mockResolvedValue(page([model(9, 'Bracket v2')]));
      await fireEvent.input(screen.getByLabelText('Search'), { target: { value: 'bracket' } });
      await vi.advanceTimersByTimeAsync(300);
      await vi.waitFor(() => expect(screen.getByLabelText(/Bracket v2/)).toBeTruthy());

      // The first, unsearched read finally answers. It is stale, so it must not
      // replace the search that overtook it.
      releaseSlow(page([model(8, 'Desiccant Tray')]));
      await vi.advanceTimersByTimeAsync(0);
      expect(screen.queryByLabelText(/Desiccant Tray/)).toBeNull();
      expect(screen.getByLabelText(/Bracket v2/)).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it('cancels without attaching', async () => {
    const { onattach, oncancel } = open();
    await waitFor(() => expect(screen.getByLabelText(/Desiccant Tray/)).toBeTruthy());
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(oncancel).toHaveBeenCalled();
    expect(onattach).not.toHaveBeenCalled();
  });

  // Pick a model, then type: the results are replaced, and a selection that
  // survived would let Add version attach a model that is no longer on screen.
  it('forgets what you picked when the search changes', async () => {
    vi.useFakeTimers();
    try {
      open();
      await vi.waitFor(() => expect(screen.getByLabelText(/Desiccant Tray/)).toBeTruthy());
      await fireEvent.click(screen.getByLabelText(/Desiccant Tray/));
      const add = screen.getByRole('button', { name: 'Add version' }) as HTMLButtonElement;
      expect(add.disabled).toBe(false);

      get.mockResolvedValue(page([model(9, 'Hinge v1')]));
      await fireEvent.input(screen.getByLabelText('Search'), { target: { value: 'hinge' } });
      // Still inside the debounce, so no request has gone out yet. The choice
      // has to be dropped on the keystroke, not on the response.
      expect(add.disabled).toBe(true);

      await vi.advanceTimersByTimeAsync(300);
      await vi.waitFor(() => expect(screen.getByLabelText(/Hinge v1/)).toBeTruthy());
      expect((screen.getByRole('button', { name: 'Add version' }) as HTMLButtonElement).disabled)
        .toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  // 25 models, one page of 24: without this the 25th cannot be attached at all
  // when no search word separates it from the rest.
  it('walks past the first page', async () => {
    get.mockResolvedValue(page([model(8, 'Desiccant Tray')], { total: 25, pageSize: 1 }));
    open();
    await waitFor(() => expect(screen.getByLabelText(/Desiccant Tray/)).toBeTruthy());

    get.mockResolvedValue(page([model(9, 'Hinge v1')], { total: 25, pageSize: 1, page: 2 }));
    await fireEvent.click(screen.getByRole('button', { name: /Show more/ }));

    await waitFor(() => expect(screen.getByLabelText(/Hinge v1/)).toBeTruthy());
    expect(lastQuery()).toEqual({ page: 2 });
    // Appended, not replaced: paging is for reaching a model, and losing the
    // ones already on screen to reach it is not reaching it.
    expect(screen.getByLabelText(/Desiccant Tray/)).toBeTruthy();
  });

  // The whole list fits, so there is nothing to walk to.
  it('offers no Show more when the page is the whole list', async () => {
    open();
    await waitFor(() => expect(screen.getByLabelText(/Desiccant Tray/)).toBeTruthy());
    expect(screen.queryByRole('button', { name: /Show more/ })).toBeNull();
  });

  // Clearing `chosen` on the keystroke is not enough on its own: the old rows
  // are still on screen for the length of the debounce, so without this the
  // user can simply pick one again and submit a model that is about to vanish
  // from the list.
  it('will not let you pick from results that are being replaced', async () => {
    vi.useFakeTimers();
    try {
      open();
      await vi.waitFor(() => expect(screen.getByLabelText(/Desiccant Tray/)).toBeTruthy());

      get.mockResolvedValue(page([model(9, 'Hinge v1')]));
      await fireEvent.input(screen.getByLabelText('Search'), { target: { value: 'hinge' } });

      // Still inside the debounce, and the old row is still rendered.
      const stale = screen.getByLabelText(/Desiccant Tray/) as HTMLInputElement;
      expect(stale.disabled).toBe(true);

      await vi.advanceTimersByTimeAsync(300);
      await vi.waitFor(() => expect(screen.getByLabelText(/Hinge v1/)).toBeTruthy());
      expect((screen.getByLabelText(/Hinge v1/) as HTMLInputElement).disabled).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  // Click Show more, then type before it answers. Without retiring the in-flight
  // request on the keystroke, its page is appended to a list the new search is
  // about to throw away.
  it('drops a page that arrives after the search moved on', async () => {
    vi.useFakeTimers();
    try {
      get.mockResolvedValue(page([model(8, 'Desiccant Tray')], { total: 25, pageSize: 1 }));
      open();
      await vi.waitFor(() => expect(screen.getByLabelText(/Desiccant Tray/)).toBeTruthy());

      let releasePage: (value: unknown) => void = () => {};
      get.mockReturnValueOnce(new Promise((resolve) => (releasePage = resolve)));
      await fireEvent.click(screen.getByRole('button', { name: /Show more/ }));

      get.mockResolvedValue(page([model(11, 'Hinge v1')]));
      await fireEvent.input(screen.getByLabelText('Search'), { target: { value: 'hinge' } });
      // Show more is off for the pending window too, so a second page cannot be
      // started against a search that is already being replaced.
      expect(screen.queryByRole('button', { name: /Show more/ })).toBeNull();

      releasePage(page([model(9, 'Bracket v9')], { total: 25, pageSize: 1, page: 2 }));
      await vi.advanceTimersByTimeAsync(0);
      // Still inside the debounce. The retired request must not append its page,
      // and - the part that outlives the window - its `finally` must not report
      // the search as settled, which would re-enable the rows it just proved
      // stale.
      expect(screen.queryByLabelText(/Bracket v9/)).toBeNull();
      expect((screen.getByLabelText(/Desiccant Tray/) as HTMLInputElement).disabled).toBe(true);

      await vi.advanceTimersByTimeAsync(300);
      await vi.waitFor(() => expect(screen.getByLabelText(/Hinge v1/)).toBeTruthy());
      expect(screen.queryByLabelText(/Bracket v9/)).toBeNull();
      expect(screen.queryByLabelText(/Desiccant Tray/)).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });
  // A page that fails to load says nothing about the rows already on screen.
  // Clearing them would take the selection with it and leave `chosen` naming a
  // model the list no longer shows - submittable, and invisible.
  it('keeps what you picked when a further page fails to load', async () => {
    get.mockResolvedValue(page([model(8, 'Desiccant Tray')], { total: 25, pageSize: 1 }));
    open();
    await waitFor(() => expect(screen.getByLabelText(/Desiccant Tray/)).toBeTruthy());
    await fireEvent.click(screen.getByLabelText(/Desiccant Tray/));

    get.mockResolvedValueOnce({ error: { detail: 'nope' } });
    await fireEvent.click(screen.getByRole('button', { name: /Show more/ }));

    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('nope'));
    // The row, and the choice made against it, both survive.
    expect((screen.getByLabelText(/Desiccant Tray/) as HTMLInputElement).checked).toBe(true);
    expect((screen.getByRole('button', { name: 'Add version' }) as HTMLButtonElement).disabled).toBe(
      false
    );
  });
  // The same, but the request never answers at all. A separate case because it
  // is a separate branch: the `catch` is not the failure branch, and without
  // its own test, clearing the rows there stays green.
  it('keeps what you picked when a further page cannot be reached', async () => {
    get.mockResolvedValue(page([model(8, 'Desiccant Tray')], { total: 25, pageSize: 1 }));
    open();
    await waitFor(() => expect(screen.getByLabelText(/Desiccant Tray/)).toBeTruthy());
    await fireEvent.click(screen.getByLabelText(/Desiccant Tray/));

    get.mockRejectedValueOnce(new Error('offline'));
    await fireEvent.click(screen.getByRole('button', { name: /Show more/ }));

    await waitFor(() =>
      expect(screen.getByRole('alert').textContent).toContain('Could not reach the server')
    );
    expect((screen.getByLabelText(/Desiccant Tray/) as HTMLInputElement).checked).toBe(true);
    expect((screen.getByRole('button', { name: 'Add version' }) as HTMLButtonElement).disabled).toBe(
      false
    );
  });
});
