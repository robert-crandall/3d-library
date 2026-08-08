import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import UploadDialog from './UploadDialog.svelte';

vi.mock('$lib/upload', async () => {
  const actual = await vi.importActual<typeof import('$lib/upload')>('$lib/upload');
  return { ...actual, uploadModel: vi.fn() };
});

import { uploadModel } from '$lib/upload';

const mocked = vi.mocked(uploadModel);

// The file input is populated directly rather than through a click: jsdom has
// no file picker, and `files` is the only thing `pick` reads.
async function pickAFile() {
  const input = screen.getByLabelText('Files') as HTMLInputElement;
  Object.defineProperty(input, 'files', {
    value: [new File(['solid'], 'a.stl', { type: 'model/stl' })],
    configurable: true
  });
  await fireEvent.change(input);
}

async function submit() {
  await fireEvent.click(screen.getByRole('button', { name: 'Upload' }));
}

describe('UploadDialog', () => {
  // The case that used to lose data. uploadModel resolves with a model *and* a
  // list of files that did not make it; the model exists on the server, so the
  // dialog has to hand it to the grid. The old version threw the model away,
  // and the only thing the user could do next was press Upload again and make a
  // second copy that this milestone cannot delete.
  it('hands a partially uploaded model to the grid and offers no retry', async () => {
    mocked.mockImplementation(async () => ({
      model: { id: 4, name: 'Half', fileCount: 1 } as never,
      failed: ['b.stl']
    }));

    const onuploaded = vi.fn();
    render(UploadDialog, { onclose: vi.fn(), onuploaded });

    await pickAFile();
    await submit();

    await waitFor(() => expect(onuploaded).toHaveBeenCalled());
    expect(onuploaded.mock.calls[0][0]).toMatchObject({ id: 4 });
    // keepOpen, so the dialog can say what is missing rather than vanishing.
    expect(onuploaded.mock.calls[0][1]).toEqual({ keepOpen: true });

    expect((await screen.findByRole('alert')).textContent).toContain('b.stl');
    expect(screen.queryByRole('button', { name: 'Upload' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Done' })).not.toBeNull();
  });

  // The other half: when nothing was created, uploadModel throws, and offering
  // a retry is exactly right because there is no model to duplicate.
  it('keeps the Upload button when the whole upload failed', async () => {
    mocked.mockImplementation(() => Promise.reject(new Error('Could not reach the server.')));

    render(UploadDialog, { onclose: vi.fn(), onuploaded: vi.fn() });

    await pickAFile();
    await submit();

    expect((await screen.findByRole('alert')).textContent).toContain('Could not reach the server.');
    expect((screen.getByRole('button', { name: 'Upload' }) as HTMLButtonElement).disabled).toBe(
      false
    );
  });
});
