import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import MeshViewer from './MeshViewer.svelte';
import { asciiStl, binaryStl, boxTriangles, coreOnly3mf } from '$lib/mesh/fixtures';
import { MAX_PREVIEW_BYTES } from '$lib/mesh/parse';

// three.js needs a GL context, which jsdom does not have. Stubbing the whole module is
// what lets the component's own decisions - which file, which state, what the readout
// says - be tested at all; what is stubbed away is exercised by using the app.
const show = vi.fn();
const setShading = vi.fn();
const dispose = vi.fn();
const resize = vi.fn();
let viewerThrows = false;
vi.mock('$lib/mesh/scene', () => ({
  createViewer: () => {
    if (viewerThrows) throw new Error('Error creating WebGL context.');
    return { show, setShading, dispose, resize };
  },
}));

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

function ok(buffer: ArrayBuffer) {
  return { ok: true, arrayBuffer: async () => buffer };
}

const BOX = asciiStl(boxTriangles(20, 10, 5));

beforeEach(() => {
  viewerThrows = false;
  fetchMock.mockReset();
  show.mockClear();
  setShading.mockClear();
  dispose.mockClear();
  resize.mockClear();
  fetchMock.mockResolvedValue(ok(BOX));
});

describe('MeshViewer', () => {
  it('loads the first mesh file and reports its dimensions', async () => {
    render(MeshViewer, { modelId: 7, files: [stl] });
    await waitFor(() =>
      expect(screen.getByTestId('mesh-readout').textContent).toMatch('20 × 10 × 5 mm · 1 object',
      ));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0][0]).toBe('/api/models/7/files/10');
    await waitFor(() => expect(show).toHaveBeenCalledTimes(1));
  });

  it('reports the object count a 3MF declares, not one per file', async () => {
    fetchMock.mockResolvedValue(
      ok(
        coreOnly3mf({
          items:
            '<item objectid="1"/><item objectid="1" transform="1 0 0 0 1 0 0 0 1 50 0 0"/>',
        }),
      ),
    );
    render(MeshViewer, { modelId: 7, files: [threemf] });
    await waitFor(() =>
      expect(screen.getByTestId('mesh-readout').textContent).toMatch('70 × 10 × 5 mm · 2 objects',
      ));
  });

  it('says so when the model has nothing previewable, and does not fetch', async () => {
    render(MeshViewer, { modelId: 7, files: [gcode] });
    expect(await screen.findByText(/no STL or 3MF file to preview/)).not.toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('hides the shading controls when there is nothing to shade', async () => {
    // Three buttons that cannot change anything are noise a screen reader still reads
    // out. Markup that is merely hidden would satisfy a `toBeVisible` check and fail
    // this one.
    render(MeshViewer, { modelId: 7, files: [gcode] });
    await screen.findByText(/no STL or 3MF file to preview/);
    expect(screen.queryByRole('group', { name: 'Shading' })).toBeNull();
  });

  it('shows the shading controls once a mesh is on screen', async () => {
    render(MeshViewer, { modelId: 7, files: [stl] });
    await waitFor(() => expect(show).toHaveBeenCalled());
    expect(screen.queryByRole('group', { name: 'Shading' })).not.toBeNull();
  });

  it('keeps the shading controls up while a mesh is still downloading', async () => {
    // The controls have to survive `loading`, because choosing a mode before the first
    // mesh arrives is supported and the renderer builds it in that mode.
    let release: (value: unknown) => void = () => {};
    fetchMock.mockReturnValue(new Promise((resolve) => (release = resolve)));
    render(MeshViewer, { modelId: 7, files: [stl] });
    await screen.findByText(/Loading preview/);
    expect(screen.queryByRole('group', { name: 'Shading' })).not.toBeNull();
    release(ok(binaryStl(boxTriangles(20, 10, 5))));
  });

  it('refuses a file over the size cap before downloading it', async () => {
    // The point of the cap is not to spend the bandwidth, so a request here would mean
    // it had already failed.
    const huge = { ...stl, size: MAX_PREVIEW_BYTES + 1 };
    render(MeshViewer, { modelId: 7, files: [huge] });
    expect((await screen.findByRole('alert')).textContent).toMatch(/too large to preview/);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('reports a 404 as a failure to load, not as a corrupt file', async () => {
    // fetch resolves for a 404, so without the response.ok check the error document
    // gets parsed as a mesh and the user is told their file is corrupt.
    fetchMock.mockResolvedValue({ ok: false, status: 404, arrayBuffer: async () => new ArrayBuffer(0) });
    render(MeshViewer, { modelId: 7, files: [stl] });
    expect((await screen.findByRole('alert')).textContent).toMatch(/could not be loaded/);
    expect(show).not.toHaveBeenCalled();
  });

  it('reports a corrupt file with the parser\'s own words', async () => {
    fetchMock.mockResolvedValue(ok(new TextEncoder().encode('junk'.repeat(40)).buffer));
    render(MeshViewer, { modelId: 7, files: [stl] });
    expect((await screen.findByRole('alert')).textContent).toMatch(/corrupt or truncated/);
  });

  it('switches file when the strip is used, and only shows the strip for a choice', async () => {
    const { rerender } = render(MeshViewer, { modelId: 7, files: [stl] });
    // One mesh file is not a choice, so there is nothing to click.
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole('button', { name: 'bracket.stl' })).toBeNull();

    await rerender({ modelId: 7, files: [stl, threemf, gcode] });
    fetchMock.mockResolvedValue(ok(coreOnly3mf()));

    // The G-code file is not offered: the strip lists what can be previewed.
    expect(screen.queryByRole('button', { name: 'plate.gcode' })).toBeNull();
    const first = screen.getByRole('button', { name: 'bracket.stl' });
    expect(first.getAttribute('aria-pressed')).toBe('true');

    await fireEvent.click(screen.getByRole('button', { name: 'plate.3mf' }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(fetchMock.mock.calls[1][0]).toBe('/api/models/7/files/11');
    expect(screen.getByRole('button', { name: 'plate.3mf' }).getAttribute('aria-pressed')).toBe('true');
    expect(first.getAttribute('aria-pressed')).toBe('false');
  });

  it('does not let a slow response paint over a newer one', async () => {
    // Switching files while the first is still in flight. Without the request counter
    // the first response lands last and the panel shows the wrong file's dimensions
    // under the second file's name.
    let releaseSlow: (value: unknown) => void = () => {};
    fetchMock.mockImplementationOnce(
      () => new Promise((resolve) => (releaseSlow = resolve)),
    );
    fetchMock.mockResolvedValue(ok(asciiStl(boxTriangles(1, 2, 3))));

    render(MeshViewer, { modelId: 7, files: [stl, lid] });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

    await fireEvent.click(screen.getByRole('button', { name: 'lid.stl' }));
    await waitFor(() =>
      expect(screen.getByTestId('mesh-readout').textContent).toMatch('1 × 2 × 3 mm'));

    releaseSlow(ok(asciiStl(boxTriangles(99, 99, 99))));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.getByTestId('mesh-readout').textContent).toMatch('1 × 2 × 3 mm');
  });

  it('does not let a slow failure replace a mesh that loaded', async () => {
    // The failure path settles in its own microtask, so it needs the same guard as the
    // success path or a late 404 turns a working preview into an error message.
    let rejectSlow: (reason: unknown) => void = () => {};
    fetchMock.mockImplementationOnce(
      () => new Promise((_resolve, reject) => (rejectSlow = reject)),
    );
    fetchMock.mockResolvedValue(ok(asciiStl(boxTriangles(1, 2, 3))));

    render(MeshViewer, { modelId: 7, files: [stl, lid] });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

    await fireEvent.click(screen.getByRole('button', { name: 'lid.stl' }));
    await waitFor(() =>
      expect(screen.getByTestId('mesh-readout').textContent).toMatch('1 × 2 × 3 mm'));

    rejectSlow(new Error('network went away'));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getByTestId('mesh-readout').textContent).toMatch('1 × 2 × 3 mm');
  });

  it('changes shading without re-downloading the file', async () => {
    render(MeshViewer, { modelId: 7, files: [stl] });
    await waitFor(() => expect(show).toHaveBeenCalled());
    setShading.mockClear();

    const wireframe = screen.getByRole('button', { name: 'Wireframe' });
    expect(screen.getByRole('button', { name: 'Solid' }).getAttribute('aria-pressed')).toBe('true');

    await fireEvent.click(wireframe);
    expect(setShading).toHaveBeenLastCalledWith('wireframe');
    expect(wireframe.getAttribute('aria-pressed')).toBe('true');

    await fireEvent.click(screen.getByRole('button', { name: 'X-ray' }));
    expect(setShading).toHaveBeenLastCalledWith('xray');
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('releases the GL context when it unmounts', async () => {
    // Browsers cap live WebGL contexts at around 16, so a page that mounts the viewer
    // without ever disposing it eventually renders nothing at all.
    const { unmount } = render(MeshViewer, { modelId: 7, files: [stl] });
    await waitFor(() => expect(show).toHaveBeenCalled());
    unmount();
    expect(dispose).toHaveBeenCalledTimes(1);
  });

  it('says so when the browser cannot start a viewer, instead of loading forever', async () => {
    viewerThrows = true;
    fetchMock.mockResolvedValue(ok(asciiStl(boxTriangles(1, 2, 3))));

    render(MeshViewer, { modelId: 7, files: [stl] });

    expect((await screen.findByRole('alert')).textContent).toMatch(/could not start the 3D preview/);
    expect(screen.queryByTestId('mesh-readout')).toBeNull();
  });

  it('applies a shading chosen before the mesh arrives', async () => {
    // The buttons work while the file is still downloading. The viewer has to be told the
    // choice before it is handed a mesh, because the mesh is built with whichever material
    // is active at the time - otherwise the button reads X-ray over a solid render.
    let release: (value: unknown) => void = () => {};
    fetchMock.mockReturnValue(new Promise((resolve) => (release = resolve)));

    render(MeshViewer, { modelId: 7, files: [stl] });
    // Wait for the viewer to exist, so the mount's own setShading('solid') has already
    // landed and the click below is provably a second, later call.
    await waitFor(() => expect(setShading).toHaveBeenCalledWith('solid'));
    await fireEvent.click(screen.getByRole('button', { name: 'X-ray' }));
    release(ok(asciiStl(boxTriangles(1, 2, 3))));

    await waitFor(() => expect(show).toHaveBeenCalled());
    // The X-ray call specifically. Mount already called setShading with the default - the
    // assertion below proves it - so an ordering claim about the *first* call is a claim
    // about 'solid', and would hold even if X-ray never reached the viewer at all.
    expect(setShading.mock.calls[0]).toEqual(['solid']);
    const xray = setShading.mock.calls.findIndex(([shading]) => shading === 'xray');
    expect(xray).toBeGreaterThan(0);
    expect(setShading.mock.invocationCallOrder[xray]).toBeLessThan(
      show.mock.invocationCallOrder[0],
    );
  });
});
