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

  There is deliberately no size cap here, unlike the mesh viewer's. The upload limit is
  500 MB, so a byte cap below it would refuse files this app accepted and one at or above
  it could never fire. What actually costs memory is segments, not bytes - a dense 40 MB
  file is a bigger problem than a sparse 400 MB one - so the refusal lives on
  `SEGMENT_CAP` in the parser, and the panel shows progress until it either draws or says
  the file is too detailed.
*/

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
    // No streaming body: a test double, or a browser that has buffered the whole
    // response already. Parsing it in one go is correct, just without progress.
    parser.push(new Uint8Array(await response.arrayBuffer()));
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
    // Releasing the lock lets an aborted body be collected rather than held by a reader
    // nobody will read again.
    reader.releaseLock();
  }

  return parser.finish();
}

/** One 60 Hz frame. Long enough that yielding is rare, short enough that a click lands. */
const FRAME_MS = 16;

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
