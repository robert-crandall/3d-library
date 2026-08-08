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
    // Some exporters pad past the last facet. The declared count still fits, so this is
    // still a binary file.
    expect(sizeOfStl(binaryStl(BOX, { trailing: 64 }))).toEqual([20, 10, 5]);
  });

  it('reads a binary file that both starts with "solid" and has trailing bytes', () => {
    // The familiar "starts with solid means ASCII" rule votes the wrong way here.
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

  it('reads a binary file whose header says "solid" and mentions vertices', () => {
    // The 80-byte header is arbitrary text, and a file that starts with "solid" and
    // happens to carry the word "vertex" defeats the prefix rule.
    const buffer = binaryStl(boxTriangles(3, 5, 7), {
      header: 'solid exported by a tool that writes vertex counts here',
      trailing: 6,
    });

    const size = sizeOf(boundsOf(parseStl(buffer).positions));

    expect(size[0]).toBeCloseTo(3);
    expect(size[1]).toBeCloseTo(5);
    expect(size[2]).toBeCloseTo(7);
  });

  it('reads a binary file whose header is itself a parseable ASCII triangle', () => {
    // The worst case for any token-sniffing rule: 80 bytes that carry "solid", "facet"
    // and three complete vertex lines, so a text read of the file succeeds and returns
    // the header's triangle instead of the real geometry - silently, with plausible
    // dimensions. The declared facet count is what settles it.
    const buffer = binaryStl(boxTriangles(3, 5, 7), {
      // Space-padded to the full 80 bytes, which is what an exporter that pads its header
      // with spaces rather than nulls produces - and what makes the last number parseable.
      header: 'solid facet vertex 1 2 3 vertex 4 5 6 vertex 7 8 9'.padEnd(80, ' '),
      trailing: 6,
    });

    const size = sizeOf(boundsOf(parseStl(buffer).positions));

    expect(size[0]).toBeCloseTo(3);
    expect(size[1]).toBeCloseTo(5);
    expect(size[2]).toBeCloseTo(7);
  });

  it('refuses an ASCII coordinate too large for the Float32 buffer', () => {
    // 1e100 is a perfectly finite double and Infinity in a Float32Array. Left alone it
    // reaches the camera as NaN: a blank canvas with no error, which reads as a broken
    // viewer rather than as a broken file.
    const text = [
      'solid overflow',
      'facet normal 0 0 0',
      'outer loop',
      'vertex 0 0 0',
      'vertex 1e100 0 0',
      'vertex 0 1 0',
      'endloop',
      'endfacet',
      'endsolid overflow',
      // Padding: a file under 84 bytes is refused before it is classified.
      '# '.padEnd(80, 'x'),
    ].join('\n');

    expect(() => parseStl(new TextEncoder().encode(text).buffer as ArrayBuffer)).toThrow(
      /corrupt or truncated/,
    );
  });
});
