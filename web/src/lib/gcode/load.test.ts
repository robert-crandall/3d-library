import { afterEach, describe, expect, it, vi } from 'vitest';
import { loadToolpath } from './load';

/*
  A fake fetch, because the interesting parts of `loadToolpath` are the ones a real
  server makes hard to arrange: a response with no `Content-Length`, a body that arrives
  in several chunks, an abort part-way through, and an error status whose body is HTML.
*/

const PRINT = ['G90', 'M83', 'G1 X0 Y0 Z0.2', ';LAYER_CHANGE', 'G1 X10 E1', 'G1 X20 E1'].join(
  '\n',
);

function respond(
  chunks: readonly string[],
  init: { status?: number; length?: number | null } = {},
): Response {
  const encoder = new TextEncoder();
  const headers = new Headers();
  const length =
    init.length === undefined
      ? chunks.reduce((sum, chunk) => sum + encoder.encode(chunk).byteLength, 0)
      : init.length;
  if (length !== null) headers.set('content-length', String(length));

  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
      controller.close();
    },
  });
  return new Response(body, { status: init.status ?? 200, headers });
}

function stub(response: Response | (() => Promise<Response>)) {
  const fetch = vi.fn<(url: string, init?: RequestInit) => Promise<Response>>(
    typeof response === 'function' ? response : async () => response,
  );
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

/** Cut a string into `count` roughly equal pieces, the way a network would. */
function split(text: string, count: number): string[] {
  const size = Math.ceil(text.length / count);
  return Array.from({ length: count }, (_, index) => text.slice(index * size, (index + 1) * size));
}

/** A `performance.now` that jumps `step` milliseconds on every reading. */
function advancingBy(step: number): () => number {
  let time = 0;
  return () => (time += step);
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('loadToolpath', () => {
  it('parses a body that arrives in one piece', async () => {
    stub(respond([PRINT]));
    const toolpath = await loadToolpath('/f.gcode');
    expect(toolpath.layers).toHaveLength(1);
    expect(toolpath.extrusionSegments).toBe(2);
  });

  it('parses a body split across chunks, including mid-line', async () => {
    // A stream boundary lands wherever the network puts it, which is regularly in the
    // middle of a command. Splitting `G1 X10 E1` in half is the case that would silently
    // drop a segment if the parser did not carry its remainder between pushes.
    const half = Math.floor(PRINT.length / 2);
    stub(respond([PRINT.slice(0, half), PRINT.slice(half)]));
    const toolpath = await loadToolpath('/f.gcode');
    expect(toolpath.extrusionSegments).toBe(2);
  });

  it('reports progress as a fraction when the length is declared', async () => {
    const chunks = [PRINT.slice(0, 20), PRINT.slice(20)];
    stub(respond(chunks));
    const seen: (number | undefined)[] = [];
    await loadToolpath('/f.gcode', { onProgress: (value) => seen.push(value) });
    expect(seen).toHaveLength(2);
    expect(seen[0]).toBeGreaterThan(0);
    expect(seen[0]).toBeLessThan(1);
    expect(seen.at(-1)).toBe(1);
  });

  it.each([
    { name: 'no header at all', length: null },
    { name: 'a zero length', length: 0 },
  ])('reports undefined progress with $name', async ({ length }) => {
    // A chunked response declares no length. Showing a fraction of an unknown total is
    // worse than showing an indeterminate bar, so the caller is told there is no number.
    stub(respond([PRINT], { length }));
    const seen: (number | undefined)[] = [];
    await loadToolpath('/f.gcode', { onProgress: (value) => seen.push(value) });
    expect(seen.length).toBeGreaterThan(0);
    expect(seen.every((value) => value === undefined)).toBe(true);
  });

  it('never reports more than 1 when the length undercounts', async () => {
    stub(respond([PRINT], { length: 4 }));
    const seen: (number | undefined)[] = [];
    await loadToolpath('/f.gcode', { onProgress: (value) => seen.push(value) });
    expect(seen.every((value) => value !== undefined && value <= 1)).toBe(true);
  });

  it.each([404, 500])('rejects on a %s rather than parsing the error page', async (status) => {
    // fetch only rejects on a transport failure, so an error status arrives as a
    // perfectly good response whose body is HTML. Parsing that gives "no toolpaths",
    // which reads as "this file is empty" for a file that is not there at all.
    stub(respond(['<!doctype html><h1>Not found</h1>'], { status }));
    await expect(loadToolpath('/f.gcode')).rejects.toThrow(/could not be loaded/);
  });

  it('passes the abort signal to fetch', async () => {
    const fetch = stub(respond([PRINT]));
    const controller = new AbortController();
    await loadToolpath('/f.gcode', { signal: controller.signal });
    expect(fetch.mock.calls[0][1]).toMatchObject({ signal: controller.signal });
  });

  it('rejects when the request is already aborted', async () => {
    stub(async () => {
      throw new DOMException('aborted', 'AbortError');
    });
    const controller = new AbortController();
    controller.abort();
    await expect(loadToolpath('/f.gcode', { signal: controller.signal })).rejects.toThrow();
  });

  it('yields to the browser once a frame of parsing has piled up', async () => {
    // Without this the page stops responding to clicks for the whole of a several-
    // hundred-megabyte parse, because `await reader.read()` on a buffered body resolves
    // as a microtask and microtasks run before the browser handles input.
    // Five chunks, each costing 6 ms: the budget is only spent on the third, and then
    // only if the clock is restarted afterwards. Counting rather than asserting "at
    // least one" is what makes this fail when the clock is left running, which would
    // yield on every chunk from the third onwards.
    stub(respond(split(PRINT, 5)));
    vi.spyOn(performance, 'now').mockImplementation(advancingBy(6));
    const timeout = vi.spyOn(globalThis, 'setTimeout');

    await loadToolpath('/f.gcode');

    expect(timeout).toHaveBeenCalledTimes(1);
  });

  it('does not yield on every chunk', async () => {
    // The other half of the same decision: a stream arrives in tens of kilobytes at a
    // time, and a nested `setTimeout` is clamped to about 4 ms, so yielding per chunk
    // would add tens of seconds to a large file for no benefit.
    stub(respond(split(PRINT, 5)));
    vi.spyOn(performance, 'now').mockImplementation(advancingBy(0));
    const timeout = vi.spyOn(globalThis, 'setTimeout');

    await loadToolpath('/f.gcode');

    expect(timeout).not.toHaveBeenCalled();
  });

  it('falls back to a buffered read when the response has no stream', async () => {
    // Not every environment gives `response.body` - a test double is the usual one.
    // Buffering is correct there, just without progress, and is better than reporting
    // an empty file.
    const response = new Response(PRINT);
    Object.defineProperty(response, 'body', { value: null });
    stub(response);
    const toolpath = await loadToolpath('/f.gcode');
    expect(toolpath.extrusionSegments).toBe(2);
  });

  it('cancels the download when the parser gives up on the file', async () => {
    // Releasing the reader's lock does not stop the body arriving; only `cancel` does.
    // The parser gives up part-way through for real reasons - the segment cap, a line
    // over a megabyte, binary G-code - and at 256 MB the rest of a file nobody will
    // draw is worth stopping.
    let cancelled = false;
    const encoder = new TextEncoder();
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode('GCDE\x00\x01\x02\x03'));
      },
      cancel() {
        cancelled = true;
      },
    });
    stub(new Response(body));
    await expect(loadToolpath('/f.gcode')).rejects.toThrow(/binary G-code/);
    expect(cancelled).toBe(true);
  });

  it('does not cancel a body that finished on its own', async () => {
    // The unconditional `cancel` in the loader's `finally` leans on the streams spec
    // saying a cancel of an already-closed stream never reaches the underlying source.
    // If that were wrong, every successful load would look to the server like a client
    // that gave up on a file it in fact read completely.
    let cancelled = false;
    const encoder = new TextEncoder();
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode(PRINT));
        controller.close();
      },
      cancel() {
        cancelled = true;
      },
    });
    stub(new Response(body));
    await loadToolpath('/f.gcode');
    expect(cancelled).toBe(false);
  });
});
