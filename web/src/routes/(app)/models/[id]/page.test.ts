import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ModelPage from './+page.svelte';
import { load } from './+page';
import { coreOnly3mf } from '$lib/mesh/fixtures';

const get = vi.fn();
const put = vi.fn();
const del = vi.fn();
const goto = vi.fn();

vi.mock('$lib/api/client', () => ({
  api: {
    GET: (...args: unknown[]) => get(...args),
    PUT: (...args: unknown[]) => put(...args),
    DELETE: (...args: unknown[]) => del(...args)
  }
}));
vi.mock('$app/navigation', () => ({ goto: (...args: unknown[]) => goto(...args) }));

// The page embeds the mesh viewer, which needs a GL context jsdom does not have and
// downloads the file with a raw fetch. Both are stubbed so the viewer settles quietly:
// what it does with the bytes is MeshViewer.test.ts's job, not this file's.
vi.mock('$lib/mesh/scene', () => ({
  createViewer: () => ({ show: vi.fn(), setShading: vi.fn(), dispose: vi.fn(), resize: vi.fn() })
}));
vi.stubGlobal('fetch', async () => new Response(coreOnly3mf()));

const file = {
  id: 10,
  filename: 'dry-box-body.3mf',
  type: '3mf',
  contentType: 'application/zip',
  size: 4 * 1024 * 1024,
  createdAt: '2026-03-12T09:00:00Z'
};

const model = {
  id: 7,
  name: 'Filament Dry Box',
  fileCount: 1,
  totalSize: 4 * 1024 * 1024,
  createdAt: '2026-03-12T09:00:00Z',
  description: '',
  printTips: '',
  sourceUrl: '',
  files: [file]
};

const data = { id: 7 };

// Only the call records are cleared, never the implementations: a `get` reset
// to its default would return undefined and the component would read `.data`
// off it before the test that follows had a chance to say what it wanted back.
beforeEach(() => {
  get.mockReset();
  put.mockClear();
  del.mockClear();
  goto.mockClear();
});

// The parse in `load` is the only thing between the URL and a request that can
// only ever be refused, so it is worth its own three lines.
describe('model detail load', () => {
  it('refuses a segment that is not a model id', () => {
    // The last one is the reason this is not just a digits check: it is all
    // digits, and Number() turns it into something that is neither a model nor
    // a 404 unless it is caught here.
    for (const id of ['nonsense', '0', '-1', '1.5', '', '7x', '9'.repeat(30)]) {
      expect(() => load({ params: { id } })).toThrow();
    }
  });

  it('passes a real id through as a number', () => {
    expect(load({ params: { id: '7' } })).toEqual({ id: 7 });
  });
});

describe('model detail page', () => {
  it('renders the model, its metadata and its files', async () => {
    get.mockResolvedValue({
      data: {
        ...model,
        description: 'Holds four spools.',
        printTips: 'PETG at 245 C.',
        sourceUrl: 'https://www.printables.com/model/48213'
      }
    });
    render(ModelPage, { data });

    expect(await screen.findByRole('heading', { name: 'Filament Dry Box' })).toBeTruthy();
    // Twice: the header and the files panel both carry it.
    expect(screen.getAllByText('1 file · 4.0 MB')).toHaveLength(2);
    expect(screen.getByText('Added 12 Mar 2026')).toBeTruthy();
    expect(screen.getByText('Holds four spools.')).toBeTruthy();
    expect(screen.getByText('PETG at 245 C.')).toBeTruthy();

    const source = screen.getByRole('link', { name: 'https://www.printables.com/model/48213' });
    expect(source.getAttribute('href')).toBe('https://www.printables.com/model/48213');
    // User-supplied and off-site, so it must not hand the destination a window
    // handle back into the app.
    expect(source.getAttribute('rel')).toContain('noreferrer');
  });

  // One tip per line, as design 1c draws them. A single paragraph would render
  // the same words and lose the list, which is what a screen reader announces
  // as "list, 3 items".
  it('renders print tips one per line', async () => {
    get.mockResolvedValue({
      // The \r is what a tip pasted from a Windows-authored README arrives with,
      // and the stray spaces are what anyone typing a list produces. Splitting
      // on \n alone leaves both in the rendered item.
      data: { ...model, printTips: 'PETG at 245 C.\r\n\r\nBrim on.\n  No supports.  ' }
    });
    render(ModelPage, { data });

    const tips = await screen.findByRole('list');
    const items = within(tips).getAllByRole('listitem');
    expect(items.map((li) => li.textContent)).toEqual(['PETG at 245 C.', 'Brim on.', 'No supports.']);
  });

  // The panels only exist when there is something in them. A heading with
  // nothing under it reads as broken, where an absent panel reads as empty.
  it('omits the description and print tips panels when they are blank', async () => {
    get.mockResolvedValue({ data: model });
    render(ModelPage, { data });

    await screen.findByRole('heading', { name: 'Filament Dry Box' });
    expect(screen.queryByRole('heading', { name: 'Description' })).toBeNull();
    expect(screen.queryByRole('heading', { name: 'Print tips' })).toBeNull();
    expect(screen.queryByRole('link', { name: /printables/ })).toBeNull();
  });

  // A plain link to the download endpoint, not a fetch: the browser streams a
  // 500 MB response to disk on its own, where reading it into a Blob first is
  // how you run out of memory.
  /**
   * The panel is chosen from the files, not sent alongside them, so these three
   * cover the whole of that choice: no G-code means no panel, a G-code file the
   * parser could not attribute means no panel either, and the first readable one
   * wins when there are several.
   */
  it('omits the slice settings panel when no file has any', async () => {
    get.mockResolvedValue({ data: model });
    render(ModelPage, { data });

    await screen.findByRole('heading', { name: 'Filament Dry Box' });
    expect(screen.queryByRole('heading', { name: 'Slice settings' })).toBeNull();
  });

  it('renders slice settings from the G-code file that has them', async () => {
    const gcode = {
      id: 11,
      filename: 'plate-1.gcode',
      type: 'gcode',
      contentType: 'text/plain; charset=utf-8',
      size: 96 * 1024 * 1024,
      createdAt: '2026-03-12T09:05:00Z',
      extractedMeta: {
        slicer: 'OrcaSlicer',
        slicerVersion: '2.3.2-dev',
        layerHeightMm: 0.2,
        nozzleTempC: 210,
        bedTempC: 55
      }
    };
    get.mockResolvedValue({ data: { ...model, files: [file, gcode] } });
    render(ModelPage, { data });

    await screen.findByRole('heading', { name: 'Slice settings' });
    expect(screen.getByText('from plate-1.gcode')).toBeTruthy();
    expect(screen.getByText('0.20 mm')).toBeTruthy();
    expect(screen.getByText('210 °C / 55 °C')).toBeTruthy();
  });

  // A file whose slicer we did not recognise stores no metadata at all, and a
  // header with no rows under it reads as a panel that failed to load.
  it('omits the panel for a G-code file with no readable settings', async () => {
    const unreadable = {
      id: 12,
      filename: 'mystery.gcode',
      type: 'gcode',
      contentType: 'text/plain; charset=utf-8',
      size: 1024,
      createdAt: '2026-03-12T09:05:00Z'
    };
    get.mockResolvedValue({ data: { ...model, files: [unreadable] } });
    render(ModelPage, { data });

    await screen.findByRole('heading', { name: 'Filament Dry Box' });
    expect(screen.queryByRole('heading', { name: 'Slice settings' })).toBeNull();
  });

  it('links each file at its download endpoint', async () => {
    get.mockResolvedValue({ data: model });
    render(ModelPage, { data });

    const link = await screen.findByRole('link', { name: 'dry-box-body.3mf' });
    expect(link.getAttribute('href')).toBe('/api/models/7/files/10');
    expect(link.getAttribute('download')).toBe('dry-box-body.3mf');
  });

  // Reachable by deleting the last file, which is a legal state on purpose.
  it('shows an empty state for a model with no files', async () => {
    get.mockResolvedValue({ data: { ...model, fileCount: 0, totalSize: 0, files: [] } });
    render(ModelPage, { data });

    expect(await screen.findByText('This model has no files.')).toBeTruthy();
    expect(screen.queryByRole('table')).toBeNull();
  });

  // A 404 is not a failed load. Offering Try again for a model that does not
  // exist is a button that can only ever fail.
  it('says a missing model is missing, with no retry', async () => {
    get.mockResolvedValue({ error: { detail: 'not found' }, response: { status: 404 } });
    render(ModelPage, { data });

    expect(await screen.findByRole('heading', { name: 'Model not found' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Try again' })).toBeNull();
    expect(screen.getByRole('link', { name: 'Back to the library' })).toBeTruthy();
  });

  it('offers a retry when the load failed for any other reason', async () => {
    get.mockResolvedValue({ error: { detail: 'boom' }, response: { status: 500 } });
    render(ModelPage, { data });

    expect((await screen.findByRole('alert')).textContent).toContain('boom');
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  it('reports an unreachable server', async () => {
    // Promise.reject rather than an async function that throws: vitest reports
    // the latter as an unhandled error before the component awaits it.
    get.mockImplementation(() => Promise.reject(new TypeError('Failed to fetch')));
    render(ModelPage, { data });

    expect((await screen.findByRole('alert')).textContent).toContain('Could not reach the server');
  });

  it('saves an edit and renders the response without re-reading', async () => {
    get.mockResolvedValue({ data: model });
    put.mockResolvedValue({ data: { ...model, name: 'Dry Box v2', description: 'Now taller.' } });
    render(ModelPage, { data });

    await screen.findByRole('heading', { name: 'Filament Dry Box' });
    const readsBefore = get.mock.calls.length;
    await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));

    const dialog = screen.getByRole('dialog');
    await fireEvent.input(within(dialog).getByLabelText('Name'), {
      target: { value: 'Dry Box v2' }
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    expect(await screen.findByRole('heading', { name: 'Dry Box v2' })).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(put.mock.calls[0][1].body.name).toBe('Dry Box v2');
    // The PUT response is the saved model, so a second GET could only be a
    // chance for the two to disagree.
    expect(get.mock.calls.length).toBe(readsBefore);
  });

  // A refused edit has to keep the dialog open with what the user typed still
  // in it. Closing it would throw away the work and leave them guessing.
  it('keeps the edit dialog open when the server refuses', async () => {
    get.mockResolvedValue({ data: model });
    put.mockResolvedValue({ error: { detail: 'source URL must be an http:// address' } });
    render(ModelPage, { data });

    await screen.findByRole('heading', { name: 'Filament Dry Box' });
    await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Save' }));

    const alert = await within(screen.getByRole('dialog')).findByRole('alert');
    expect(alert.textContent).toContain('source URL must be an http:// address');
    expect(screen.getByRole('dialog')).toBeTruthy();
  });

  it('re-reads the model after a file is deleted', async () => {
    get.mockResolvedValueOnce({ data: model });
    get.mockResolvedValue({ data: { ...model, fileCount: 0, totalSize: 0, files: [] } });
    del.mockResolvedValue({ data: undefined });
    render(ModelPage, { data });

    await screen.findByRole('link', { name: 'dry-box-body.3mf' });
    await fireEvent.click(screen.getByRole('button', { name: /Delete dry-box-body\.3mf/ }));
    // Names the file. A confirmation that says "this file" is one the user
    // cannot check before agreeing to it.
    expect(screen.getByRole('dialog').textContent).toContain('dry-box-body.3mf will be deleted');
    await fireEvent.click(screen.getByRole('button', { name: 'Delete file' }));

    expect(await screen.findByText('This model has no files.')).toBeTruthy();
    expect(del.mock.calls[0][1].params.path).toEqual({ id: 7, fileId: 10 });
  });

  // The distinction the whole flow turns on: the delete landed, the re-read did
  // not. Leaving the dialog open would offer to delete a file that is already
  // gone, so the dialog closes and the page says it cannot show the result.
  it('closes the dialog when a delete succeeds but the re-read fails', async () => {
    get.mockResolvedValueOnce({ data: model });
    get.mockResolvedValue({ error: { detail: 'boom' }, response: { status: 500 } });
    del.mockResolvedValue({ data: undefined });
    render(ModelPage, { data });

    await screen.findByRole('link', { name: 'dry-box-body.3mf' });
    await fireEvent.click(screen.getByRole('button', { name: /Delete dry-box-body\.3mf/ }));
    await fireEvent.click(screen.getByRole('button', { name: 'Delete file' }));

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  it('keeps the delete dialog open when the delete itself fails', async () => {
    get.mockResolvedValue({ data: model });
    del.mockResolvedValue({ error: { detail: 'nope' } });
    render(ModelPage, { data });

    await screen.findByRole('link', { name: 'dry-box-body.3mf' });
    await fireEvent.click(screen.getByRole('button', { name: /Delete dry-box-body\.3mf/ }));
    await fireEvent.click(screen.getByRole('button', { name: 'Delete file' }));

    const dialog = screen.getByRole('dialog');
    expect((await within(dialog).findByRole('alert')).textContent).toContain('nope');
    expect(within(dialog).getByRole('button', { name: 'Delete file' })).toBeTruthy();
  });

  // Deleting the model is the one action that cannot be undone and cannot be
  // reached by accident, so it goes through a confirmation that names the model.
  it('confirms before deleting the model, then leaves for the library', async () => {
    get.mockResolvedValue({ data: model });
    del.mockResolvedValue({ data: undefined });
    render(ModelPage, { data });

    await screen.findByRole('heading', { name: 'Filament Dry Box' });
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    const dialog = screen.getByRole('dialog');
    expect(dialog.textContent).toContain('Filament Dry Box');
    expect(del).not.toHaveBeenCalled();

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Delete model' }));
    await waitFor(() => expect(goto).toHaveBeenCalledWith('/'));
  });

  it('stays on the page when deleting the model fails', async () => {
    get.mockResolvedValue({ data: model });
    del.mockResolvedValue({ error: { detail: 'still in use' } });
    render(ModelPage, { data });

    await screen.findByRole('heading', { name: 'Filament Dry Box' });
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Delete model' }));

    expect((await screen.findByRole('alert')).textContent).toContain('still in use');
    expect(goto).not.toHaveBeenCalled();
  });

  // A stale error from a dialog the user gave up on must not greet them in the
  // next one.
  it('does not carry an error from one dialog into the next', async () => {
    get.mockResolvedValue({ data: model });
    put.mockResolvedValue({ error: { detail: 'refused' } });
    render(ModelPage, { data });

    await screen.findByRole('heading', { name: 'Filament Dry Box' });
    await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Save' }));
    await within(screen.getByRole('dialog')).findByRole('alert');
    await fireEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' })
    );

    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect(within(screen.getByRole('dialog')).queryByRole('alert')).toBeNull();
  });

  // The viewer is a section like the description and the source link: present when
  // there is something to put in it, absent otherwise. It also decides whether three.js
  // is fetched at all, because `MeshViewer` imports it on mount - so a panel that
  // rendered for every model would spend 130 KB telling a G-code-only model that it has
  // nothing to show.
  it('leaves the viewer out of a model with no mesh file', async () => {
    get.mockResolvedValue({
      data: {
        ...model,
        files: [{ ...file, id: 21, filename: 'plate-1.gcode', type: 'gcode' }]
      }
    });
    render(ModelPage, { data });

    await screen.findByRole('link', { name: 'plate-1.gcode' });
    expect(screen.queryByTestId('mesh-viewer')).toBeNull();
  });

  it('shows the viewer for a model that has one', async () => {
    get.mockResolvedValue({ data: model });
    render(ModelPage, { data });

    expect(await screen.findByTestId('mesh-viewer')).not.toBeNull();
  });
});

// The thumbnail controls are the only place the user can override what the
// server picked, so what they say and when they appear is the whole feature.
describe('model detail thumbnails', () => {
  const withThumbs = {
    ...model,
    fileCount: 2,
    thumbnailFileId: 10,
    thumbnailAutomatic: true,
    files: [
      { ...file, hasThumbnail: true },
      {
        id: 11,
        filename: 'printed.png',
        type: 'image',
        contentType: 'image/png',
        size: 200 * 1024,
        createdAt: '2026-03-12T09:01:00Z',
        hasThumbnail: true
      }
    ]
  };

  // A weaker test would assert the badge exists. The word "automatic" is the
  // distinction it is there to draw: this pick moves on its own when a better
  // file arrives, and a pinned one does not.
  it('says when the thumbnail was chosen for you', async () => {
    get.mockResolvedValue({ data: withThumbs });
    render(ModelPage, { data });

    expect(await screen.findByText('Thumbnail (automatic)')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Use as thumbnail' })).toBeTruthy();
    // Nothing to undo on an automatic pick.
    expect(screen.queryByRole('button', { name: 'Use automatic' })).toBeNull();
  });

  it('pins a file and renders what the server resolved', async () => {
    get.mockResolvedValue({ data: withThumbs });
    put.mockResolvedValue({
      data: { ...withThumbs, thumbnailFileId: 11, thumbnailAutomatic: false }
    });
    render(ModelPage, { data });

    await fireEvent.click(await screen.findByRole('button', { name: 'Use as thumbnail' }));

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(put.mock.calls[0][0]).toBe('/api/models/{id}/thumbnail');
    expect(put.mock.calls[0][1].body).toEqual({ fileId: 11 });
    // The response is the resolved model, so no second GET is needed and none
    // is made - one read, one truth.
    expect(get).toHaveBeenCalledTimes(1);

    // Scoped to the row, because the column header carries the same word for
    // screen readers.
    const row = await waitFor(() => {
      const cell = screen.getByTitle('printed.png').closest('tr');
      if (!cell) throw new Error('no row');
      return cell;
    });
    await waitFor(() => expect(within(row).getByText('Thumbnail')).toBeTruthy());
    expect(screen.queryByText('Thumbnail (automatic)')).toBeNull();
    expect(screen.getByRole('button', { name: 'Use automatic' })).toBeTruthy();
  });

  // null, not an omitted key: the column is nullable and clearing it is the
  // entire request. Leaving it out would be a no-op the user reads as a bug.
  it('clears a pin by sending null', async () => {
    get.mockResolvedValue({
      data: { ...withThumbs, thumbnailFileId: 11, thumbnailAutomatic: false }
    });
    put.mockResolvedValue({ data: withThumbs });
    render(ModelPage, { data });

    await fireEvent.click(await screen.findByRole('button', { name: 'Use automatic' }));

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(put.mock.calls[0][1].body).toEqual({ fileId: null });
  });

  // A file with no thumbnail can never be the model's, so offering the button
  // would be offering a guaranteed 422.
  it('does not offer to pin a file that has no thumbnail', async () => {
    get.mockResolvedValue({
      data: {
        ...withThumbs,
        files: [
          { ...file, hasThumbnail: true },
          {
            id: 11,
            filename: 'notes.txt',
            type: 'document',
            contentType: 'text/plain',
            size: 400,
            createdAt: '2026-03-12T09:01:00Z',
            hasThumbnail: false
          }
        ]
      }
    });
    render(ModelPage, { data });

    expect(await screen.findByRole('link', { name: 'notes.txt' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Use as thumbnail' })).toBeNull();
  });

  // The failure goes on the page, not in a dialog, because the row that caused
  // it is still on screen. What matters is that the page survives it: a refused
  // write is not a failed read, so the model must not be discarded.
  it('reports a refused pin and keeps the model on screen', async () => {
    get.mockResolvedValue({ data: withThumbs });
    put.mockResolvedValue({
      error: { title: 'Unprocessable Entity', errors: [{ message: 'that file has no thumbnail' }] }
    });
    render(ModelPage, { data });

    await fireEvent.click(await screen.findByRole('button', { name: 'Use as thumbnail' }));

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('that file has no thumbnail');
    expect(screen.getByRole('link', { name: 'dry-box-body.3mf' })).toBeTruthy();
  });

  // Every mutation is disabled while any one is in flight. Without this a pin
  // and a delete overlap and the later response wins, leaving the page showing
  // a file the server no longer has.
  // Both thumbnail states, because they offer different buttons: "Use
  // automatic" only exists once something is pinned, so a table driven only by
  // the automatic fixture never renders it and never notices it staying live.
  //
  // Naming every button individually matters too. Asserting "some button is
  // disabled" would survive dropping the attribute from any single control.
  for (const tc of [
    { state: 'an automatic pick', pinned: false, click: 'Use as thumbnail' },
    { state: 'a pinned file', pinned: true, click: 'Use automatic' }
  ]) {
    it(`locks the other mutations while a pin is in flight from ${tc.state}`, async () => {
      const state = { ...withThumbs, thumbnailAutomatic: !tc.pinned };
      get.mockResolvedValue({ data: state });
      let release: (value: unknown) => void = () => {};
      put.mockReturnValue(new Promise((resolve) => (release = resolve)));
      render(ModelPage, { data });

      await fireEvent.click(await screen.findByRole('button', { name: tc.click }));

      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'Edit' }).hasAttribute('disabled')).toBe(true)
      );
      const locked = [
        'Add files',
        'Delete',
        'Use as thumbnail',
        'Delete dry-box-body.3mf',
        'Delete printed.png'
      ];
      if (tc.pinned) locked.push('Use automatic');
      for (const name of locked) {
        expect(screen.getByRole('button', { name }).hasAttribute('disabled')).toBe(true);
      }

      release({ data: state });

      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'Edit' }).hasAttribute('disabled')).toBe(false)
      );
    });
  }
});
