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
 * Decide whether the bytes are binary or ASCII, using three.js's `STLLoader.isBinary`
 * heuristic - not because it is elegant, but because it is the one that has met real
 * files.
 *
 * The exact-length test is the reliable half. The "solid" prefix is the unreliable
 * half: it is only a convention for ASCII files, and plenty of binary exporters write
 * it into the 80-byte header. So a length match wins outright, and the prefix is
 * consulted only when the length does not match - which happens for ASCII files and for
 * binary files with trailing bytes.
 */
function looksBinary(bytes: Uint8Array): boolean {
  if (bytes.length >= HEADER_BYTES + COUNT_BYTES) {
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    const facets = view.getUint32(HEADER_BYTES, true);
    if (HEADER_BYTES + COUNT_BYTES + FACET_BYTES * facets === bytes.length) return true;
  }

  // "solid" may be preceded by whitespace in a text file.
  const SOLID = [0x73, 0x6f, 0x6c, 0x69, 0x64];
  for (let offset = 0; offset < 5; offset++) {
    if (SOLID.every((b, i) => bytes[offset + i] === b)) return false;
  }
  return true;
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
      if (!Number.isFinite(coordinate)) throw new Error(CORRUPT);
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

  let positions: Float32Array;
  if (looksBinary(bytes)) {
    positions = parseBinary(bytes);
  } else {
    // The heuristic cannot classify a binary file that both starts with "solid" and
    // carries trailing bytes: the length test fails and the prefix test says ASCII. Both
    // tokens are required rather than just "vertex", because a binary file's 80-byte
    // header is arbitrary and may contain the word; an ASCII body always has facets too.
    const text = new TextDecoder().decode(bytes);
    if (/facet\s/.test(text) && /vertex\s/.test(text)) {
      try {
        positions = parseAscii(text);
      } catch {
        // A binary file carrying both words in its header lands here. Its facets are the
        // real geometry, so prefer them; a genuinely truncated ASCII file has no plausible
        // facet count at byte 80 either, and still ends up reported as corrupt.
        positions = parseBinary(bytes);
      }
    } else {
      positions = parseBinary(bytes);
    }
  }

  // An STL has no unit field. Millimetres is universal in 3D printing - every slicer
  // assumes it - so the numbers are taken as millimetres unchanged.
  return { positions, objectCount: 1 };
}
