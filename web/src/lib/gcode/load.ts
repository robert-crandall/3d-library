import { createToolpathParser, type Toolpath } from './toolpath';

/*
  Getting a G-code file's bytes into a `Toolpath` without locking up the tab.

  Unlike a mesh, a G-code file is read as a stream: several hundred megabytes is a
  normal size for one, and `arrayBuffer()` on that is a copy of the whole file in memory
  before a single line is parsed. Reading it chunk by chunk means peak memory is the
  parsed geometry rather than the geometry plus the text.

  Kept out of the component so the progress arithmetic and the abort handling can be
  tested against a fake response, which is where their bugs are - a stalled progress bar
  and a cancelled load that paints anyway both look fine in a canvas.

  There are two caps and they cover different things. `SEGMENT_CAP` in the parser is the
  one that matches what actually costs memory - a dense 40 MB file is a bigger problem
  than a sparse 400 MB one - but it can only fire once the whole file has arrived and
  been read. `MAX_GCODE_BYTES` is checked against the size the API already reported, so a
  file too big to be worth trying is refused before a byte of it is fetched rather than
  after a quarter-gigabyte download that ends in "too detailed".
*/

/**
 * The largest G-code file the viewer will fetch.
 *
 * Real slicer output runs 33-37k segments per megabyte, so `SEGMENT_CAP` bites at around
 * 230 MB of it: this sits just above that, which is what keeps the two caps from being
 * the same cap twice. A denser file than that is stopped by segments after parsing; a
 * sparser one - mostly comments, or a hand-written file - is stopped here before the
 * download. It is well under the 500 MB upload limit on purpose, because being able to
 * store a file has never meant a browser can draw it; the mesh viewer draws the same
 * distinction with `MAX_PREVIEW_BYTES`.
 */
export const MAX_GCODE_BYTES = 256 * 1024 * 1024;

export type LoadOptions = {
  readonly signal?: AbortSignal;
  /** Fraction in `[0, 1]`, or undefined when the response declared no length. */
  onProgress?(fraction: number | undefined): void;
};

/**
 * Fetch and parse, reporting progress as the bytes arrive.
 *
 * Progress is measured in bytes read rather than in anything the parser reports,
 * because bytes are the part with a known total. A file served without a
 * `Content-Length` - which is what a chunked response is - reports undefined rather than
 * a fraction of an unknown, and the caller shows an indeterminate state.
 */
export async function loadToolpath(url: string, options: LoadOptions = {}): Promise<Toolpath> {
  const response = await fetch(url, { signal: options.signal });
  // fetch only rejects on a transport failure, so a 404 or a 500 arrives here as a
  // perfectly good response whose body is an error document. Parsing that as G-code
  // gives a confusing "this file has no toolpaths" for a file that is simply not there.
  if (!response.ok) throw new Error('This file could not be loaded.');

  // `Number(null)` is 0 for a missing header and `Number('chunked')` is NaN, and the
  // one comparison below rejects both, so neither needs a check of its own.
  const total = Number(response.headers.get('content-length'));

  const parser = createToolpathParser();
  const body = response.body;
  if (!body) {
    // No streaming body: a test double, or an environment without streaming fetch.
    // Fed in slices the size of a real stream's chunks rather than in one piece,
    // because `push` decodes what it is given into a string - handing it 256 MB of
    // bytes would transiently cost that again in text. The parser already handles a
    // command split across a chunk boundary, since a real stream splits them too.
    const bytes = new Uint8Array(await response.arrayBuffer());
    for (let at = 0; at < bytes.byteLength; at += FALLBACK_CHUNK_BYTES) {
      parser.push(bytes.subarray(at, at + FALLBACK_CHUNK_BYTES));
    }
    return parser.finish();
  }

  const reader = body.getReader();
  let read = 0;
  let worked = now();
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      if (!value) continue;
      read += value.byteLength;
      parser.push(value);
      options.onProgress?.(total > 0 ? Math.min(read / total, 1) : undefined);

      // Hand the main thread back, but only once a frame's worth of work has piled up.
      // Both halves matter. Without any yield a several-hundred-megabyte file parses in
      // one unbroken run of microtasks and the page stops responding to clicks -
      // `await reader.read()` does not yield on its own once the body is buffered,
      // because a resolved promise is a microtask. Yielding on *every* chunk is just as
      // bad the other way: a stream arrives in tens of kilobytes at a time, so a 300 MB
      // file is about ten thousand chunks, and `setTimeout` is clamped to roughly 4 ms
      // once it is nested, which is forty seconds of doing nothing.
      if (now() - worked >= FRAME_MS) {
        await yieldToBrowser();
        worked = now();
      }
    }
  } finally {
    // A parser error - the segment cap, a line over a megabyte, binary G-code - leaves
    // the rest of the body still arriving, and at 256 MB that is worth stopping. Only
    // `cancel` stops it; releasing the lock does not. Unconditional because cancelling
    // an already-closed stream is defined to be a no-op, so the success path needs no
    // flag to skip it. The `catch` is for a stream that has already errored, where
    // `cancel` rejects - not a failure worth reporting over the one being thrown.
    void reader.cancel().catch(() => {});
    reader.releaseLock();
  }

  return parser.finish();
}

/** One 60 Hz frame. Long enough that yielding is rare, short enough that a click lands. */
const FRAME_MS = 16;

/** What a real stream hands over at a time, so the buffered path behaves like one. */
const FALLBACK_CHUNK_BYTES = 64 * 1024;

function now(): number {
  return typeof performance === 'undefined' ? Date.now() : performance.now();
}

/**
 * Yield to the event loop, not just to the microtask queue.
 *
 * `setTimeout` rather than `queueMicrotask` on purpose: a microtask runs before the
 * browser gets a chance to handle input, so awaiting one keeps the page just as frozen.
 */
function yieldToBrowser(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}
