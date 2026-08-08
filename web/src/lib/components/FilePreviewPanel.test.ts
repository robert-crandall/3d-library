import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import FilePreviewPanel from './FilePreviewPanel.svelte';
import { asciiStl, boxTriangles, coreOnly3mf } from '$lib/mesh/fixtures';
import { MAX_PREVIEW_BYTES } from '$lib/mesh/parse';

/*
  What the panel decides: which file opens by default, what the strip does, and which of
  the two viewers - or neither - gets to draw. The viewers themselves are real here, so
  these are the end-to-end selection paths; what each one does with a file it has been
  handed is tested next to that component.
*/

// three.js needs a GL context, which jsdom does not have.
const meshViewer = { show: vi.fn(), setShading: vi.fn(), dispose: vi.fn(), resize: vi.fn() };
vi.mock('$lib/mesh/scene', () => ({ createViewer: () => meshViewer }));

const gcodeViewer = {
  show: vi.fn(),
  setLayer: vi.fn(),
  setTravelVisible: vi.fn(),
  dispose: vi.fn(),
  resize: vi.fn(),
};
vi.mock('$lib/gcode/scene', () => ({ createViewer: () => gcodeViewer }));

const fetchMock = vi.fn();
vi.stubGlobal('fetch', (...args: unknown[]) => fetchMock(...args));

const stl = {
  id: 10,
  filename: 'bracket.stl',
  type: 'stl',
  contentType: 'model/stl',
  size: 4 * 1024,
  createdAt: '2026-03-12T09:00:00Z',
  hasThumbnail: false,
};
const threemf = { ...stl, id: 11, filename: 'plate.3mf', type: '3mf' };
const gcode = { ...stl, id: 12, filename: 'plate.gcode', type: 'gcode' };
const lid = { ...stl, id: 13, filename: 'lid.stl' };
const photo = { ...stl, id: 14, filename: 'printed.jpg', type: 'image' };

const BOX = asciiStl(boxTriangles(20, 10, 5));
const PRINT = ['G90', 'M83', 'G1 X0 Y0 Z0.2', ';LAYER_CHANGE', 'G1 X10 E1', 'G1 X10 Y5 E1'].join(
  '\n',
);

function mesh(buffer: ArrayBuffer) {
  return { ok: true, headers: new Headers(), body: null, arrayBuffer: async () => buffer };
}

function text(body: string) {
  const bytes = new TextEncoder().encode(body);
  return {
    ok: true,
    headers: new Headers({ 'content-length': String(bytes.byteLength) }),
    body: null,
    arrayBuffer: async () => bytes.buffer,
  };
}

beforeEach(() => {
  fetchMock.mockReset();
  for (const spy of [...Object.values(meshViewer), ...Object.values(gcodeViewer)]) spy.mockClear();
  fetchMock.mockImplementation(async (url: string) =>
    url.endsWith('/12') ? text(PRINT) : mesh(BOX),
  );
});

describe('FilePreviewPanel', () => {
  it('only shows the strip when there is a choice to make', async () => {
    // A control with a single option is a label pretending to be a button.
    const { rerender } = render(FilePreviewPanel, { modelId: 7, files: [stl] });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole('button', { name: 'bracket.stl' })).toBeNull();

    await rerender({ modelId: 7, files: [stl, lid] });
    expect(screen.queryByRole('button', { name: 'bracket.stl' })).not.toBeNull();
  });

  it('switches file when the strip is used', async () => {
    render(FilePreviewPanel, { modelId: 7, files: [stl, lid] });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const first = screen.getByRole('button', { name: 'bracket.stl' });
    expect(first.getAttribute('aria-pressed')).toBe('true');

    await fireEvent.click(screen.getByRole('button', { name: 'lid.stl' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(fetchMock.mock.calls[1][0]).toBe('/api/models/7/files/13');
    expect(screen.getByRole('button', { name: 'lid.stl' }).getAttribute('aria-pressed')).toBe(
      'true',
    );
    expect(first.getAttribute('aria-pressed')).toBe('false');
  });

  it('opens on the 3MF when a model has both, whatever order they are listed in', async () => {
    // The server lists files by id, which is upload order. Taking the first previewable
    // one would make the default depend on which the user happened to drop in first, so
    // this fixture deliberately puts the STL ahead of the 3MF.
    fetchMock.mockResolvedValue(mesh(coreOnly3mf()));
    render(FilePreviewPanel, { modelId: 7, files: [stl, threemf] });

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(fetchMock.mock.calls[0][0]).toBe('/api/models/7/files/11');
  });

  it('opens on the mesh rather than the G-code that came out of it', async () => {
    // The mesh is the thing that was modelled; the G-code is one slicing of it for one
    // printer. Opening on somebody's draft profile misrepresents the model.
    render(FilePreviewPanel, { modelId: 7, files: [gcode, stl] });

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(fetchMock.mock.calls[0][0]).toBe('/api/models/7/files/10');
    expect(screen.queryByTestId('mesh-viewer')).not.toBeNull();
    expect(screen.queryByTestId('gcode-viewer')).toBeNull();
  });

  it('opens on the G-code when that is all there is', async () => {
    render(FilePreviewPanel, { modelId: 7, files: [photo, gcode] });

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(fetchMock.mock.calls[0][0]).toBe('/api/models/7/files/12');
    expect(screen.queryByTestId('gcode-viewer')).not.toBeNull();
    expect(screen.queryByTestId('mesh-viewer')).toBeNull();
  });

  it('does not open on a mesh it would refuse when a smaller one is there', async () => {
    // A 3MF project file carrying every plate can pass the cap where the STL export of
    // one part does not. Preferring the 3MF unconditionally shows "too large" to someone
    // whose model previews perfectly well from the file next to it.
    render(FilePreviewPanel, {
      modelId: 7,
      files: [{ ...threemf, size: MAX_PREVIEW_BYTES + 1 }, stl],
    });

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(fetchMock.mock.calls[0][0]).toBe('/api/models/7/files/10');
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('still refuses when every mesh is over the cap', async () => {
    // Nothing better to fall back to, so the refusal is the honest answer rather than an
    // empty panel - and it is still the 3MF that gets named.
    render(FilePreviewPanel, {
      modelId: 7,
      files: [
        { ...stl, size: MAX_PREVIEW_BYTES + 1 },
        { ...threemf, size: MAX_PREVIEW_BYTES + 2 },
      ],
    });

    expect((await screen.findByRole('alert')).textContent).toMatch(/too large to preview/);
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'plate.3mf' }).getAttribute('aria-pressed')).toBe(
      'true',
    );
  });

  it('offers files it cannot draw and says so when one is picked', async () => {
    // Acceptance criterion 5. The strip lists everything the model has, so choosing the
    // photo is a thing a user can actually do - and doing it has to say why there is no
    // preview rather than leave an empty box. Filtering the strip down to previewable
    // files would make this state unreachable, which is the bug this pins.
    render(FilePreviewPanel, { modelId: 7, files: [stl, photo] });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

    await fireEvent.click(screen.getByRole('button', { name: 'printed.jpg' }));

    expect((await screen.findByText(/no preview for printed\.jpg/)).textContent).toMatch(
      /STL and 3MF meshes and\s+G-code toolpaths/,
    );
    // Not downloaded: there is nothing this panel could do with the bytes.
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId('mesh-viewer')).toBeNull();
    expect(screen.queryByTestId('gcode-viewer')).toBeNull();
  });

  it('releases the mesh GL context when the G-code viewer takes over', async () => {
    // Browsers cap live contexts at around 16, so switching kinds back and forth has to
    // hand the old one back rather than leave it to age out.
    render(FilePreviewPanel, { modelId: 7, files: [stl, gcode] });
    await waitFor(() => expect(meshViewer.show).toHaveBeenCalled());

    await fireEvent.click(screen.getByRole('button', { name: 'plate.gcode' }));

    await waitFor(() => expect(gcodeViewer.show).toHaveBeenCalled());
    expect(meshViewer.dispose).toHaveBeenCalledTimes(1);
  });

  it('keeps one GL context across a switch between two files of the same kind', async () => {
    // The counterpart: staying inside one branch has to reuse the component, or every
    // click in the strip costs a context.
    render(FilePreviewPanel, { modelId: 7, files: [stl, lid] });
    await waitFor(() => expect(meshViewer.show).toHaveBeenCalledTimes(1));

    await fireEvent.click(screen.getByRole('button', { name: 'lid.stl' }));

    await waitFor(() => expect(meshViewer.show).toHaveBeenCalledTimes(2));
    expect(meshViewer.dispose).not.toHaveBeenCalled();
  });
});
