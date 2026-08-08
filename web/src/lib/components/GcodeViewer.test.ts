import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import GcodeViewer from './GcodeViewer.svelte';
import { MAX_GCODE_BYTES } from '$lib/gcode/load';
import { DEFAULT_VOLUME } from '$lib/gcode/printer';

/*
  What this component decides: what it downloads, what state it lands in, where the
  slider starts, what the readouts say, and which of a pair of overlapping responses
  wins. The renderer is stubbed - it needs a real GL context - so what is checked here is
  the arguments it is handed, which is the part with the arithmetic in it.
*/

const show = vi.fn();
const setLayer = vi.fn();
const clear = vi.fn();
const setTravelVisible = vi.fn();
const dispose = vi.fn();
const resize = vi.fn();
let viewerThrows = false;
vi.mock('$lib/gcode/scene', () => ({
  createViewer: () => {
    if (viewerThrows) throw new Error('Error creating WebGL context.');
    return { show, clear, setLayer, setTravelVisible, dispose, resize };
  },
}));

const fetchMock = vi.fn();
vi.stubGlobal('fetch', (...args: unknown[]) => fetchMock(...args));

const file = {
  id: 12,
  filename: 'plate.gcode',
  type: 'gcode',
  contentType: 'text/x.gcode',
  size: 900,
  createdAt: '2026-03-12T09:00:00Z',
  hasThumbnail: false,
};

// Three layers of one extrusion each, 10 mm apart in X and 0.2 mm apart in Z, so the
// dimensions and the per-layer Z are both things a reader can check by eye.
const PRINT = [
  'G90',
  'M83',
  'G1 X0 Y0 Z0.2',
  ';LAYER_CHANGE',
  ';Z:0.2',
  'G1 X10 Y0 E1',
  ';LAYER_CHANGE',
  ';Z:0.4',
  'G1 Z0.4',
  'G1 X10 Y20 E1',
  ';LAYER_CHANGE',
  ';Z:0.6',
  'G1 Z0.6',
  'G1 X0 Y20 E1',
].join('\n');

function respond(body: string, init: { ok?: boolean; length?: number | null } = {}) {
  const bytes = new TextEncoder().encode(body);
  const headers = new Headers();
  const length = init.length === undefined ? bytes.byteLength : init.length;
  if (length !== null) headers.set('content-length', String(length));
  return {
    ok: init.ok ?? true,
    headers,
    body: new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(bytes);
        controller.close();
      },
    }),
    arrayBuffer: async () => bytes.buffer,
  };
}

beforeEach(() => {
  viewerThrows = false;
  fetchMock.mockReset();
  for (const spy of [show, setLayer, setTravelVisible, dispose, resize]) spy.mockClear();
  fetchMock.mockResolvedValue(respond(PRINT));
});

describe('GcodeViewer', () => {
  it('reads the file it is given and reports its dimensions', async () => {
    // The height is 0.6, not 0.4. A toolpath's Z is the nozzle height, which is the top
    // of the material it is laying down, so the print stands 0.6 mm off a plate it sits
    // on - measuring between the lowest and highest toolpath loses the first layer.
    // Asserting the whole string rather than the X and Y is the point: `10 × 20` passed
    // just as happily when the height was wrong.
    render(GcodeViewer, { modelId: 7, file });

    await waitFor(() =>
      expect(screen.getByTestId('gcode-readout').textContent).toMatch('10 × 20 × 0.6 mm'),
    );
    expect(fetchMock.mock.calls[0][0]).toBe('/api/models/7/files/12');
    await waitFor(() => expect(show).toHaveBeenCalledTimes(1));
  });

  it('reports the height of a print only one layer tall', async () => {
    // The degenerate case of measuring Z between the lowest and highest toolpath: with
    // one layer they are the same number, and a print that exists reported 0 mm tall.
    fetchMock.mockResolvedValue(respond(['G90', 'M83', 'G1 X0 Y0 Z0.3', 'G1 X10 E1'].join('\n')));
    render(GcodeViewer, { modelId: 7, file });

    await waitFor(() =>
      expect(screen.getByTestId('gcode-readout').textContent).toMatch('10 × 0 × 0.3 mm'),
    );
  });

  it('takes the height from the slicer when the coordinates are not heights', async () => {
    // A belt printer prints on a 45-degree belt, so its Z is not a height above the bed:
    // the IdeaFormer fixture runs `G1 Y988.179 Z-987.979` and its highest coordinate is
    // -988. The slicer still declares the true height in `;Z:`, which is what a layer's
    // z is when the file announces one. Both are here so a formula that reads the
    // coordinate cannot pass by accident.
    fetchMock.mockResolvedValue(
      respond(
        [
          'G90',
          'M83',
          ';LAYER_CHANGE',
          ';Z:0.2',
          'G1 X0 Y900 Z-900',
          'G1 X10 E1',
          ';LAYER_CHANGE',
          ';Z:0.4',
          'G1 X0 Y900.2 Z-899.8',
          'G1 X10 E1',
        ].join('\n'),
      ),
    );
    render(GcodeViewer, { modelId: 7, file });

    await waitFor(() =>
      expect(screen.getByTestId('gcode-readout').textContent).toMatch('10 × 0.2 × 0.4 mm'),
    );
  });

  it('drops the previous print before parsing the next', async () => {
    // Not after: the scene holds the previous toolpath's buffers, up to 204 MB of them
    // at the segment cap, and keeping them through the next parse peaks at twice what
    // the cap was chosen to allow. Two large plates of one project is the pair that
    // reaches this. Ordering is the whole assertion - `clear` being called eventually
    // is what the code did before.
    const { rerender } = render(GcodeViewer, { modelId: 7, file });
    await waitFor(() => expect(show).toHaveBeenCalledTimes(1));
    clear.mockClear();
    fetchMock.mockClear();

    await rerender({ modelId: 7, file: { ...file, id: 13 } });

    await waitFor(() => expect(clear).toHaveBeenCalledTimes(1));
    expect(clear.mock.invocationCallOrder[0]).toBeLessThan(fetchMock.mock.invocationCallOrder[0]);
  });

  it('drops the previous print when the next file is refused unread', async () => {
    // The two early returns run before any of the loading state is set, so a release
    // that lives with that state never happens on this path. The 204 MB then sits
    // behind the refusal message for as long as it is on screen, which is until the
    // reader picks a different file - and picking the oversized one is exactly what
    // someone does when they are short of memory.
    const { rerender } = render(GcodeViewer, { modelId: 7, file });
    await waitFor(() => expect(show).toHaveBeenCalledTimes(1));
    clear.mockClear();

    await rerender({ modelId: 7, file: { ...file, id: 13, size: MAX_GCODE_BYTES + 1 } });

    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('too large'));
    expect(clear).toHaveBeenCalledTimes(1);
  });

  it('opens on the finished print, not on the first layer', async () => {
    // The whole object is what someone recognises. The slider is for taking it apart
    // afterwards, so starting at the bottom shows a single outline of something
    // unidentifiable and looks like a broken parse.
    render(GcodeViewer, { modelId: 7, file });

    await waitFor(() => expect(show).toHaveBeenCalled());
    expect(screen.getByTestId('gcode-layer-readout').textContent).toMatch('Layer 3 of 3');
    expect(setLayer).toHaveBeenLastCalledWith(2);
  });

  it('scrubs to a layer and reports that layer\'s Z', async () => {
    render(GcodeViewer, { modelId: 7, file });
    await waitFor(() => expect(show).toHaveBeenCalled());
    setLayer.mockClear();

    await fireEvent.input(screen.getByTestId('gcode-layer'), { target: { value: '0' } });

    expect(setLayer).toHaveBeenLastCalledWith(0);
    // The Z the file labelled the layer with, not a count multiplied by a layer height -
    // the first layer is routinely thicker than the rest.
    expect(screen.getByTestId('gcode-layer-readout').textContent).toMatch('Layer 1 of 3 · Z 0.20');
  });

  it('does not offer a slider for a single-layer print', async () => {
    // A control with one stop cannot do anything, and is still something a screen reader
    // reads out and a keyboard user tabs through.
    fetchMock.mockResolvedValue(respond(['G90', 'M83', 'G1 X0 Y0 Z0.2', 'G1 X10 E1'].join('\n')));
    render(GcodeViewer, { modelId: 7, file });

    await waitFor(() => expect(show).toHaveBeenCalled());
    expect(screen.queryByTestId('gcode-layer')).toBeNull();
    expect(screen.getByTestId('gcode-layer-readout').textContent).toMatch('Layer 1 of 1');
  });

  it('starts with travel moves off and toggles them without re-reading the file', async () => {
    // Travel is the majority of the lines in a G-code file and none of the printed
    // object, so a viewer that shows it by default shows a ball of string.
    render(GcodeViewer, { modelId: 7, file });
    await waitFor(() => expect(show).toHaveBeenCalled());
    const toggle = screen.getByRole('button', { name: 'Travel moves' });
    expect(toggle.getAttribute('aria-pressed')).toBe('false');
    setTravelVisible.mockClear();

    await fireEvent.click(toggle);

    expect(setTravelVisible).toHaveBeenLastCalledWith(true);
    expect(toggle.getAttribute('aria-pressed')).toBe('true');
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('falls back to a default build volume and says it is a guess', async () => {
    // Acceptance criterion 4: never invent a printer. A file that says nothing gets a
    // plate drawn at a common size and a readout that does not claim to know.
    render(GcodeViewer, { modelId: 7, file });

    await waitFor(() => expect(show).toHaveBeenCalled());
    expect(show.mock.calls[0][1].volume).toEqual(DEFAULT_VOLUME);
    expect(screen.getByTestId('gcode-readout').textContent).toMatch(/Unknown printer/);
  });

  it('draws the plate the file declares, and names the printer', async () => {
    render(GcodeViewer, {
      modelId: 7,
      file: {
        ...file,
        extractedMeta: {
          printerModel: 'Bambu Lab X1 Carbon',
          buildVolume: {
            minXMm: 0,
            minYMm: 0,
            maxXMm: 256,
            maxYMm: 256,
            heightMm: 256,
          },
        },
      },
    });

    await waitFor(() => expect(show).toHaveBeenCalled());
    expect(show.mock.calls[0][1].volume).toEqual({ x: 256, y: 256, z: 256, originX: 0, originY: 0 });
    expect(screen.getByTestId('gcode-readout').textContent).toMatch('Bambu Lab X1 Carbon');
  });

  it('draws in the filament colour the file declares', async () => {
    render(GcodeViewer, {
      modelId: 7,
      file: { ...file, extractedMeta: { filamentColor: '#1F8A4C' } },
    });

    await waitFor(() => expect(show).toHaveBeenCalled());
    expect(show.mock.calls[0][1].color).toBe(0x1f8a4c);
  });

  it('lifts a filament colour too dark to see against the panel', async () => {
    // Black is the most common filament there is, and the renderer clears to transparent
    // so the panel shows through - which is near-black in dark mode. Clamping lightness
    // keeps the hue, which is the checkable half of "matches the filament colour".
    render(GcodeViewer, {
      modelId: 7,
      file: { ...file, extractedMeta: { filamentColor: '#000000' } },
    });

    await waitFor(() => expect(show).toHaveBeenCalled());
    expect(show.mock.calls[0][1].color).toBeGreaterThan(0x000000);
  });

  it('shows a determinate bar when the response declares a length', async () => {
    let release: (value: unknown) => void = () => {};
    fetchMock.mockReturnValue(new Promise((resolve) => (release = resolve)));
    render(GcodeViewer, { modelId: 7, file });

    const bar = await screen.findByTestId('gcode-progress');
    expect(bar.getAttribute('value')).toBeNull();

    release(respond(PRINT));
    await waitFor(() => expect(show).toHaveBeenCalled());
  });

  it('reports a 404 as a failure to load, not as an empty file', async () => {
    // fetch resolves for a 404, so without the response.ok check the error document gets
    // parsed as G-code and the user is told their file has no toolpaths.
    fetchMock.mockResolvedValue(respond('<!doctype html><h1>Not found</h1>', { ok: false }));
    render(GcodeViewer, { modelId: 7, file });

    expect((await screen.findByRole('alert')).textContent).toMatch(/could not be loaded/);
    expect(show).not.toHaveBeenCalled();
  });

  it("reports an unreadable file with the parser's own words", async () => {
    fetchMock.mockResolvedValue(respond('; just a comment\n; and another\n'));
    render(GcodeViewer, { modelId: 7, file });

    expect((await screen.findByRole('alert')).textContent).toMatch(/no toolpaths/i);
  });

  it('says so when the browser cannot start a viewer, instead of loading forever', async () => {
    viewerThrows = true;
    render(GcodeViewer, { modelId: 7, file });

    expect((await screen.findByRole('alert')).textContent).toMatch(
      /could not start the 3D preview/,
    );
    expect(screen.queryByTestId('gcode-readout')).toBeNull();
  });

  it('releases the GL context when it unmounts', async () => {
    // Browsers cap live WebGL contexts at around 16, so a page that mounts the viewer
    // without ever disposing it eventually renders nothing at all.
    const { unmount } = render(GcodeViewer, { modelId: 7, file });
    await waitFor(() => expect(show).toHaveBeenCalled());

    unmount();

    expect(dispose).toHaveBeenCalledTimes(1);
  });

  it('does not let a slow response paint over a newer one', async () => {
    // Switching files while the first is still being read. Without the request counter
    // the first response lands last and the panel shows the wrong file's toolpaths under
    // the second file's name.
    let releaseSlow: (value: unknown) => void = () => {};
    fetchMock.mockImplementationOnce(() => new Promise((resolve) => (releaseSlow = resolve)));
    fetchMock.mockResolvedValue(
      respond(['G90', 'M83', 'G1 X0 Y0 Z0.2', 'G1 X1 Y2 E1'].join('\n')),
    );

    const { rerender } = render(GcodeViewer, { modelId: 7, file });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

    await rerender({ modelId: 7, file: { ...file, id: 13, filename: 'other.gcode' } });
    await waitFor(() => expect(show).toHaveBeenCalledTimes(1));

    releaseSlow(respond(PRINT));
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(show).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('gcode-layer-readout').textContent).toMatch('Layer 1 of 1');
  });

  it('does not let a slow failure replace a print that loaded', async () => {
    // The failure path settles in its own microtask, so it needs the same guard as the
    // success path or a late 404 turns a working preview into an error message.
    let rejectSlow: (reason: unknown) => void = () => {};
    fetchMock.mockImplementationOnce(
      () => new Promise((_resolve, reject) => (rejectSlow = reject)),
    );
    fetchMock.mockResolvedValue(respond(PRINT));

    const { rerender } = render(GcodeViewer, { modelId: 7, file });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

    await rerender({ modelId: 7, file: { ...file, id: 13 } });
    await waitFor(() => expect(show).toHaveBeenCalled());

    rejectSlow(new Error('network went away'));
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getByTestId('gcode-readout')).not.toBeNull();
  });
  it('refuses an oversized file without downloading it', () => {
    // The parser's segment cap cannot do this: it counts segments, so it needs the
    // bytes first. A quarter-gigabyte download that ends in "too detailed" is the
    // failure this exists to avoid, and asserting the message alone would not catch a
    // version that fetched anyway.
    render(GcodeViewer, {
      modelId: 7,
      file: { ...file, size: MAX_GCODE_BYTES + 1 },
    });

    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.getByRole('alert').textContent).toContain('too large to preview');
  });

  it('does not download a file it has no renderer for', async () => {
    // Once WebGL is gone every later file in the strip would otherwise stream in full
    // to arrive at the same sentence.
    viewerThrows = true;
    const { rerender } = render(GcodeViewer, { modelId: 7, file });
    await waitFor(() => expect(screen.getByRole('alert')).not.toBeNull());
    const before = fetchMock.mock.calls.length;

    await rerender({ modelId: 7, file: { ...file, id: 13 } });
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(fetchMock.mock.calls.length).toBe(before);
    expect(screen.getByRole('alert').textContent).toContain('could not start');
  });
});