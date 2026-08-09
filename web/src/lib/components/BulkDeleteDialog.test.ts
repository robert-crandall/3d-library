import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import BulkDeleteDialog from './BulkDeleteDialog.svelte';

const post = vi.fn();
vi.mock('$lib/api/client', () => ({ api: { POST: (...args: unknown[]) => post(...args) } }));

function open(overrides: Record<string, unknown> = {}) {
  const ondeleted = vi.fn();
  const oncancel = vi.fn();
  render(BulkDeleteDialog, { modelIds: [1, 2, 3], ondeleted, oncancel, ...overrides });
  return { ondeleted, oncancel };
}

function ok(data: unknown) {
  return Promise.resolve({ data, response: { status: 200 } });
}

function failed(status: number, message: string) {
  return Promise.resolve({
    error: { title: 'error', errors: [{ message }] },
    response: { status }
  });
}

/*
  What a weaker version of this file would miss: asserting the sentence appears
  would pass against a dialog that counted from the grid. Deleting a root takes
  its versions and their files, and the grid only ever knows a root's own file
  count - so the numbers must come from the server, and they must be handed back
  with the delete so a change in another tab cannot make the sentence a lie.
*/
describe('BulkDeleteDialog', () => {
  // Every test sets its own implementation; this is only so the call list one
  // test reads is that test's calls.
  beforeEach(() => post.mockReset());

  it('counts what will be destroyed before offering the button', async () => {
    post.mockImplementation(() => ok({ models: 3, versions: 2, files: 17 }));
    open();

    expect(screen.getByText(/Checking what will be deleted/)).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Delete' }) as HTMLButtonElement).disabled).toBe(
      true
    );

    expect(
      await screen.findByText(
        '3 models, 2 versions, and all 17 files will be deleted. This cannot be undone.'
      )
    ).toBeTruthy();
    expect(post).toHaveBeenCalledWith('/api/models/bulk/delete-preview', {
      body: { modelIds: [1, 2, 3] }
    });
  });

  // "and its 0 versions" is noise on the common case, and a sentence nobody
  // believes is a sentence nobody reads.
  it('leaves versions out when there are none', async () => {
    post.mockImplementation(() => ok({ models: 1, versions: 0, files: 4 }));
    open({ modelIds: [1] });

    expect(
      await screen.findByText('1 model, and all 4 files will be deleted. This cannot be undone.')
    ).toBeTruthy();
  });

  // The counts go back with the delete. Without them the server cannot tell
  // that a version was attached while this dialog was open, and the user agrees
  // to one sentence and gets a different deletion.
  it('sends back the numbers it showed', async () => {
    post.mockImplementation((path: string) =>
      String(path).endsWith('delete-preview')
        ? ok({ models: 3, versions: 2, files: 17 })
        : ok(undefined)
    );
    const { ondeleted } = open();

    await screen.findByText(/17 files/);
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(ondeleted).toHaveBeenCalledTimes(1));
    expect(post).toHaveBeenLastCalledWith('/api/models/bulk/delete', {
      body: { modelIds: [1, 2, 3], expectVersions: 2, expectFiles: 17 }
    });
  });

  // A 409 is the refusal the user can act on: it means the set moved. Deleting
  // anyway would be the silent partial destruction this whole handshake exists
  // to prevent, so it re-reads and asks again.
  it('asks again with fresh numbers when the set changed underneath', async () => {
    let previews = 0;
    post.mockImplementation((path: string) => {
      if (String(path).endsWith('delete-preview')) {
        previews += 1;
        return ok(previews === 1 ? { models: 3, versions: 2, files: 17 } : {
          models: 3,
          versions: 3,
          files: 21
        });
      }
      return failed(409, 'the selection changed');
    });
    const { ondeleted } = open();

    await screen.findByText(/17 files/);
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    expect(await screen.findByText(/The selection changed while this was open/)).toBeTruthy();
    expect(await screen.findByText(/3 versions, and all 21 files/)).toBeTruthy();
    expect(ondeleted).not.toHaveBeenCalled();
  });

  // Any other refusal is not something a second press fixes, so it is reported
  // rather than retried, and the dialog stays open with its button still there.
  it('reports a refusal it cannot retry', async () => {
    post.mockImplementation((path: string) =>
      String(path).endsWith('delete-preview')
        ? ok({ models: 3, versions: 0, files: 5 })
        : failed(404, 'model not found')
    );
    const { ondeleted } = open();

    await screen.findByText(/5 files/);
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    expect(await screen.findByRole('alert')).toBeTruthy();
    expect(screen.getByRole('alert').textContent).toContain('model not found');
    expect(ondeleted).not.toHaveBeenCalled();
  });

  // Cancel must not delete anything, and it must not have deleted anything on
  // the way to being cancelled.
  it('deletes nothing on cancel', async () => {
    post.mockImplementation(() => ok({ models: 3, versions: 0, files: 5 }));
    const { oncancel, ondeleted } = open();

    await screen.findByText(/5 files/);
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(oncancel).toHaveBeenCalledTimes(1);
    expect(ondeleted).not.toHaveBeenCalled();
    expect(post.mock.calls.every((call) => String(call[0]).endsWith('delete-preview'))).toBe(true);
  });

  // A failed preview leaves the button unusable rather than offering a delete
  // over a set nobody could count.
  it('will not delete when it could not count', async () => {
    post.mockImplementation(() => failed(500, 'something went wrong'));
    open();

    expect(await screen.findByRole('alert')).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Delete' }) as HTMLButtonElement).disabled).toBe(
      true
    );
  });
});
