import type { ParsedMesh } from './geometry';
import { parseStl } from './stl';
import { parse3mf } from './threemf';

/**
 * The largest file the viewer will download.
 *
 * Everything else in this app reads files through bounded windows - `internal/gcode` and
 * `internal/thumb` take a 16 KB head and a 128 KB tail - but a mesh cannot be windowed:
 * the triangles are the file. So the browser downloads the whole thing, and this is the
 * point at which we decline instead.
 *
 * 100 MB is roughly 2.09M triangles of binary STL, which is more than a printable part
 * needs and about as much as a browser tab will hold without the parse becoming the
 * user's problem. The upload cap is 500 MB, so files above this are accepted, stored and
 * downloadable - just not previewed. The panel says so rather than spinning.
 */
export const MAX_PREVIEW_BYTES = 100 * 1024 * 1024;

export function parseMesh(type: string, buffer: ArrayBuffer): ParsedMesh {
  if (type === 'stl') return parseStl(buffer);
  if (type === '3mf') return parse3mf(buffer);
  throw new Error('This file type cannot be previewed.');
}
