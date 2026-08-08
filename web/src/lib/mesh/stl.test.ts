import { describe, expect, it } from 'vitest';
import { boundsOf, sizeOf } from './geometry';
import { asciiStl, binaryStl, boxTriangles } from './fixtures';
import { parseStl } from './stl';

const BOX = boxTriangles(20, 10, 5, [3, -4, 7]);

function sizeOfStl(buffer: ArrayBuffer): [number, number, number] {
  return sizeOf(boundsOf(parseStl(buffer).positions));
}

describe('parseStl', () => {
  it('reads an ASCII box', () => {
    const parsed = parseStl(asciiStl(BOX));
    expect(parsed.positions.length).toBe(12 * 9);
    expect(parsed.objectCount).toBe(1);
    expect(sizeOf(boundsOf(parsed.positions))).toEqual([20, 10, 5]);
  });

  it('reads a binary box', () => {
    expect(sizeOfStl(binaryStl(BOX))).toEqual([20, 10, 5]);
  });

  it('reads the numbers as millimetres, unscaled', () => {
    // An STL carries no unit. If the parser ever grew a scale factor this is the test
    // that would notice.
    const parsed = parseStl(binaryStl([0, 0, 0, 25.4, 0, 0, 0, 12.7, 0]));
    expect(parsed.positions[3]).toBeCloseTo(25.4, 4);
    expect(parsed.positions[7]).toBeCloseTo(12.7, 4);
  });

  it('reads a binary file whose header starts with "solid"', () => {
    // Several exporters do this, and a parser that trusts the prefix reads the header
    // as text and finds no triangles at all.
    expect(sizeOfStl(binaryStl(BOX, { header: 'solid exported by something' }))).toEqual([
      20, 10, 5,
    ]);
  });

  it('reads a binary file with trailing bytes', () => {
    // The exact-length test fails here, so classification falls to the prefix.
    expect(sizeOfStl(binaryStl(BOX, { trailing: 64 }))).toEqual([20, 10, 5]);
  });

  it('reads a binary file that both starts with "solid" and has trailing bytes', () => {
    // Neither half of the heuristic gets this right: the length does not match and the
    // prefix says ASCII. Only the "decoded to text with no vertex line" retry saves it.
    expect(
      sizeOfStl(binaryStl(BOX, { header: 'solid v1', trailing: 32 })),
    ).toEqual([20, 10, 5]);
  });

  it('refuses a binary file whose header promises more facets than it holds', () => {
    // This is what an interrupted download looks like. Reading the facets that are
    // present would render a mesh that quietly differs from the file.
    expect(() => parseStl(binaryStl(BOX, { declaredFacets: 999 }))).toThrow(
      /corrupt or truncated/,
    );
  });

  it('refuses a binary file declaring no facets', () => {
    expect(() => parseStl(binaryStl([]))).toThrow(/corrupt or truncated/);
  });

  it('refuses an ASCII file cut off mid-triangle', () => {
    const text = new TextDecoder().decode(asciiStl(BOX));
    const cut = text.slice(0, text.indexOf('vertex', text.length - 400) + 20);
    expect(() => parseStl(new TextEncoder().encode(cut).buffer as ArrayBuffer)).toThrow(
      /corrupt or truncated/,
    );
  });

  it('refuses an ASCII file with a non-numeric coordinate', () => {
    const text = new TextDecoder()
      .decode(asciiStl(BOX))
      .replace('vertex 3 ', 'vertex NaN ');
    expect(() => parseStl(new TextEncoder().encode(text).buffer as ArrayBuffer)).toThrow(
      /corrupt or truncated/,
    );
  });

  it('refuses a binary file with a non-finite coordinate', () => {
    const buffer = binaryStl(BOX);
    new DataView(buffer).setFloat32(84 + 12, Number.NaN, true);
    expect(() => parseStl(buffer)).toThrow(/corrupt or truncated/);
  });

  it('refuses a file too short to hold a header', () => {
    expect(() => parseStl(new ArrayBuffer(12))).toThrow(/corrupt or truncated/);
  });

  it('refuses an empty ASCII solid', () => {
    const text = 'solid empty\nendsolid empty\n'.padEnd(200, ' ');
    expect(() => parseStl(new TextEncoder().encode(text).buffer as ArrayBuffer)).toThrow(
      /corrupt or truncated/,
    );
  });

  it('reads an ASCII file with CRLF line endings and exponent notation', () => {
    const text = new TextDecoder()
      .decode(asciiStl([0, 0, 0, 1, 0, 0, 0, 1, 0]))
      .replace(/\n/g, '\r\n')
      .replace('vertex 1 0 0', 'vertex 1.0e0 0 0');
    const parsed = parseStl(new TextEncoder().encode(text).buffer as ArrayBuffer);
    expect(Array.from(parsed.positions)).toEqual([0, 0, 0, 1, 0, 0, 0, 1, 0]);
  });
});
