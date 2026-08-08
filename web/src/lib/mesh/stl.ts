import type { ParsedMesh } from './geometry';

// An STL is a bag of triangles with no unit, no transform and no object structure, so
// this is the short parser. What it does carry is a format ambiguity - the same
// extension covers a text and a binary encoding, with no reliable magic number - and
// files truncated by an interrupted download, which must raise an error rather than
// render as an empty scene.

/** Every failure funnels into one sentence; the user cannot act on the distinction. */
const CORRUPT = 'This file is corrupt or truncated.';

const HEADER_BYTES = 80;
const COUNT_BYTES = 4;
const FACET_BYTES = 50;

/**
 * Decide whether the bytes are binary or ASCII.
 *
 * A binary STL declares its facet count at byte 80, so 84 + 50n bytes must be present.
 * That self-consistency is the whole test. The more familiar "starts with solid" rule is
 * deliberately not used: it is only a convention for ASCII files, plenty of binary
 * exporters write the word into their 80-byte header, and one that also writes "facet"
 * and three "vertex" lines there parses as a one-triangle ASCII file with the real
 * geometry dropped.
 *
 * An ASCII file cannot pass this test. Byte 83 is a printable character, so the
 * little-endian count reads as at least 0x09000000 facets - a 7 GB file, well past the
 * 100 MB preview cap.
 */
function looksBinary(bytes: Uint8Array): boolean {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const facets = view.getUint32(HEADER_BYTES, true);
  // Trailing bytes past the last facet are harmless and some exporters write them, so
  // this is `<=` rather than an exact match.
  return facets > 0 && HEADER_BYTES + COUNT_BYTES + FACET_BYTES * facets <= bytes.length;
}

function parseBinary(bytes: Uint8Array): Float32Array {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const facets = view.getUint32(HEADER_BYTES, true);

  // The header's count is the only length information a binary STL carries. If the file
  // is shorter than the count promises it was cut short; trailing bytes past the count
  // are harmless and some exporters write them, so only the short case is an error.
  if (facets === 0 || HEADER_BYTES + COUNT_BYTES + FACET_BYTES * facets > bytes.length) {
    throw new Error(CORRUPT);
  }
  const positions = new Float32Array(facets * 9);
  let out = 0;
  for (let f = 0; f < facets; f++) {
    // Each facet is a normal we ignore (three recomputes it), three vertices, and a
    // two-byte attribute count nobody uses.
    let at = HEADER_BYTES + COUNT_BYTES + f * FACET_BYTES + 12;
    for (let v = 0; v < 9; v++, at += 4) {
      const coordinate = view.getFloat32(at, true);
      if (!Number.isFinite(coordinate)) throw new Error(CORRUPT);
      positions[out++] = coordinate;
    }
  }
  return positions;
}

const VERTEX = /vertex\s+(\S+)\s+(\S+)\s+(\S+)/g;

function parseAscii(text: string): Float32Array {
  const coordinates: number[] = [];
  VERTEX.lastIndex = 0;
  for (let m = VERTEX.exec(text); m !== null; m = VERTEX.exec(text)) {
    for (let axis = 1; axis <= 3; axis++) {
      const coordinate = Number(m[axis]);
      // Float32, not Float64: these end up in a Float32Array, where 1e100 is finite going
      // in and Infinity coming out, and Infinity reaches the camera as NaN - a blank
      // canvas with no error rather than "this file is corrupt".
      if (!Number.isFinite(Math.fround(coordinate))) throw new Error(CORRUPT);
      coordinates.push(coordinate);
    }
  }
  // A file cut off mid-triangle leaves a partial one. Dropping it would render a mesh
  // that silently differs from the file, so treat it as the truncation it is.
  if (coordinates.length === 0 || coordinates.length % 9 !== 0) throw new Error(CORRUPT);
  return new Float32Array(coordinates);
}

export function parseStl(buffer: ArrayBuffer): ParsedMesh {
  const bytes = new Uint8Array(buffer);
  if (bytes.length < HEADER_BYTES + COUNT_BYTES) throw new Error(CORRUPT);

  // Anything that is not a self-consistent binary file is read as text, so a binary file
  // truncated mid-download finds no vertices and is reported as corrupt rather than
  // rendering as an empty scene.
  const positions = looksBinary(bytes)
    ? parseBinary(bytes)
    : parseAscii(new TextDecoder().decode(bytes));

  // An STL has no unit field. Millimetres is universal in 3D printing - every slicer
  // assumes it - so the numbers are taken as millimetres unchanged.
  return { positions, objectCount: 1 };
}
