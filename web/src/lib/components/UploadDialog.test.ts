import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import UploadDialog from './UploadDialog.svelte';
import { MAX_FILE_BYTES } from '$lib/upload';

vi.mock('$lib/upload', async () => {
  const actual = await vi.importActual<typeof import('$lib/upload')>('$lib/upload');
  return { ...actual, uploadModel: vi.fn() };
});

import { UploadFailed, uploadModel } from '$lib/upload';

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
  // Screen readers need to be told this is a modal; the markup is a plain div,
  // so nothing says so unless it is said explicitly.
  it('announces itself as a modal dialog', () => {
    render(UploadDialog, { onclose: vi.fn(), onuploaded: vi.fn() });

    const dialog = screen.getByRole('dialog');
    expect(dialog.getAttribute('aria-modal')).toBe('true');
    expect(dialog.getAttribute('aria-labelledby')).toBe('upload-title');
  });

  // aria-modal is a promise that the rest of the page is unreachable. These
  // three tests are that promise: focus starts inside, Tab does not leave, and
  // Escape is the way out.
  it('puts focus in the dialog when it opens', () => {
    render(UploadDialog, { onclose: vi.fn(), onuploaded: vi.fn() });

    expect(document.activeElement).toBe(screen.getByLabelText('Name'));
  });

  it('keeps Tab inside the dialog', async () => {
    render(UploadDialog, { onclose: vi.fn(), onuploaded: vi.fn() });

    const name = screen.getByLabelText('Name');
    const cancel = screen.getByRole('button', { name: 'Cancel' });

    // Upload is disabled with an empty queue, so Cancel is the last stop.
    cancel.focus();
    await fireEvent.keyDown(window, { key: 'Tab' });
    expect(document.activeElement).toBe(name);

    await fireEvent.keyDown(window, { key: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(cancel);
  });

  it('closes on Escape, but not mid-upload', async () => {
    const onclose = vi.fn();
    let finish!: () => void;
    mocked.mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = () => resolve({ model: { id: 1, name: 'A', fileCount: 1 } as never, failed: [] });
        })
    );

    render(UploadDialog, { onclose, onuploaded: vi.fn() });

    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(onclose).toHaveBeenCalledTimes(1);

    await pickAFile();
    await submit();
    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(onclose).toHaveBeenCalledTimes(1);

    finish();
  });

  // changed the path. Keying the list on the filename made Svelte treat them as
  // the same row, so one file's progress overwrote the other's.
  it('gives same-named files their own rows', async () => {
    // Never resolves: the point is the state *during* the upload, and a
    // resolved upload closes the dialog before there is anything to look at.
    mocked.mockImplementation(
      (_name, _files, onState) =>
        new Promise(() => {
          onState(0, 'done');
        })
    );

    render(UploadDialog, { onclose: vi.fn(), onuploaded: vi.fn() });

    const input = screen.getByLabelText('Files') as HTMLInputElement;
    Object.defineProperty(input, 'files', {
      value: [
        new File(['solid'], 'part.stl', { type: 'model/stl' }),
        new File(['solid'], 'part.stl', { type: 'model/stl' })
      ],
      configurable: true
    });
    await fireEvent.change(input);

    expect(screen.getAllByText('part.stl')).toHaveLength(2);

    await submit();
    // The first file is uploaded and the second is still queued. Keyed on the
    // filename, both rows were the same row and read the same.
    await waitFor(() => expect(screen.getAllByText('Uploaded')).toHaveLength(1));
    expect(screen.getAllByText('Queued')).toHaveLength(1);
  });

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
    // Plain Done: the model is already in the grid and nothing is in doubt.
    expect(screen.queryByRole('button', { name: 'Done' })).not.toBeNull();
  });

  // The other half: the server *answered* with a refusal, so nothing was
  // created and a retry cannot duplicate anything. Offering one is right.
  it('keeps the Upload button when the server refused the upload', async () => {
    mocked.mockImplementation(() => Promise.reject(new UploadFailed('That file is not a model.', true)));

    render(UploadDialog, { onclose: vi.fn(), onuploaded: vi.fn() });

    await pickAFile();
    await submit();

    expect((await screen.findByRole('alert')).textContent).toContain('That file is not a model.');
    expect((screen.getByRole('button', { name: 'Upload' }) as HTMLButtonElement).disabled).toBe(
      false
    );
  });

  // A dropped connection does not mean the upload failed - the request may
  // have landed and the response been lost. Pressing Upload again there is how
  // you get two copies of a model nothing in this milestone can delete, so the
  // dialog has to stop offering it and send the user to look instead.
  it('offers no retry when the failure does not prove nothing was created', async () => {
    mocked.mockImplementation(() =>
      Promise.reject(
        new UploadFailed('Could not reach the server. The model may still have been created.', false)
      )
    );

    const closed = vi.fn();
    render(UploadDialog, { onclose: closed, onuploaded: vi.fn() });

    await pickAFile();
    await submit();

    const alert = await screen.findByRole('alert');
    // Shown verbatim: only the throw site knows whether the model might exist
    // or definitely does, so the dialog does not add a sentence of its own.
    expect(alert.textContent).toContain('Could not reach the server.');
    expect(alert.textContent).toContain('may still have been created');
    expect(screen.queryByRole('button', { name: 'Upload' })).toBeNull();
    // Not "Done". Closing on its own would put the user back on a page whose
    // Upload button is right there, and the question of whether the model
    // exists would still be open. Re-reading the library is what answers it.
    expect(screen.queryByRole('button', { name: 'Done' })).toBeNull();
    const onclose = vi.mocked(closed);
    await fireEvent.click(screen.getByRole('button', { name: 'Reload library' }));
    expect(onclose).toHaveBeenCalledWith({ reload: true });
  });

  // The Upload button is gone once the dialog goes terminal, but the name field
  // is still there and Enter in a text input submits the form. Without a guard
  // that is a second upload, and a second copy of the model.
  it('does not upload again when Enter is pressed after a partial upload', async () => {
    mocked.mockImplementation(async () => ({
      model: { id: 7, name: 'Half', fileCount: 1 } as never,
      failed: ['b.stl']
    }));

    render(UploadDialog, { onclose: vi.fn(), onuploaded: vi.fn() });

    await pickAFile();
    await submit();
    await screen.findByRole('alert');
    // Counted from a baseline rather than from zero: the mock is deliberately
    // not reset between tests, because resetting it in a top-level beforeEach
    // makes vitest report a deliberately-rejected upload as an unhandled
    // rejection before the component's own catch ever sees it.
    const sent = mocked.mock.calls.length;

    await fireEvent.submit(screen.getByLabelText('Name').closest('form') as HTMLFormElement);

    expect(mocked.mock.calls.length).toBe(sent);
  });

  // A rejected selection has to clear the accepted one too. Leaving the earlier
  // pick uploadable under a message about a different file uploads the wrong
  // thing, quietly.
  it('drops an earlier selection when a later one is rejected', async () => {
    render(UploadDialog, { onclose: vi.fn(), onuploaded: vi.fn() });

    await pickAFile();
    expect((screen.getByRole('button', { name: 'Upload' }) as HTMLButtonElement).disabled).toBe(
      false
    );

    const input = screen.getByLabelText('Files') as HTMLInputElement;
    const huge = new File(['x'], 'huge.stl', { type: 'model/stl' });
    Object.defineProperty(huge, 'size', { value: MAX_FILE_BYTES + 1 });
    Object.defineProperty(input, 'files', { value: [huge], configurable: true });
    await fireEvent.change(input);

    expect((await screen.findByRole('alert')).textContent).toContain('huge.stl');
    expect((screen.getByRole('button', { name: 'Upload' }) as HTMLButtonElement).disabled).toBe(
      true
    );
  });
});
