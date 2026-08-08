import { zipSync, type Zippable } from 'fflate';

// Test-only builders. Real STL and 3MF files are megabytes of geometry whose exact
// bounds nobody can assert by hand, so the tests build small files whose answer is known
// by construction. Kept out of the *.test.ts files because the STL builders are shared
// and the 3MF ones are long enough to bury the assertions.

/** The twelve triangles of an axis-aligned box, as raw vertex coordinates. */
export function boxTriangles(
  sx: number,
  sy: number,
  sz: number,
  origin: [number, number, number] = [0, 0, 0],
): number[] {
  const [ox, oy, oz] = origin;
  const x0 = ox;
  const x1 = ox + sx;
  const y0 = oy;
  const y1 = oy + sy;
  const z0 = oz;
  const z1 = oz + sz;
  const c: Record<string, [number, number, number]> = {
    a: [x0, y0, z0],
    b: [x1, y0, z0],
    c: [x1, y1, z0],
    d: [x0, y1, z0],
    e: [x0, y0, z1],
    f: [x1, y0, z1],
    g: [x1, y1, z1],
    h: [x0, y1, z1],
  };
  const faces = 'abc acd efg egh abf afe dcg dgh bcg bgf adh ahe'.split(' ');
  return faces.flatMap((face) => [...c[face[0]], ...c[face[1]], ...c[face[2]]]);
}

export function asciiStl(coordinates: number[]): ArrayBuffer {
  let text = 'solid test\n';
  for (let i = 0; i < coordinates.length; i += 9) {
    text += '  facet normal 0 0 0\n    outer loop\n';
    for (let v = 0; v < 3; v++) {
      const at = i + v * 3;
      text += `      vertex ${coordinates[at]} ${coordinates[at + 1]} ${coordinates[at + 2]}\n`;
    }
    text += '    endloop\n  endfacet\n';
  }
  text += 'endsolid test\n';
  return new TextEncoder().encode(text).buffer as ArrayBuffer;
}

export function binaryStl(
  coordinates: number[],
  options: { header?: string; declaredFacets?: number; trailing?: number } = {},
): ArrayBuffer {
  const facets = coordinates.length / 9;
  const buffer = new ArrayBuffer(84 + facets * 50 + (options.trailing ?? 0));
  const view = new DataView(buffer);
  if (options.header) {
    new Uint8Array(buffer).set(new TextEncoder().encode(options.header).slice(0, 80));
  }
  view.setUint32(80, options.declaredFacets ?? facets, true);
  for (let f = 0; f < facets; f++) {
    let at = 84 + f * 50 + 12;
    for (let v = 0; v < 9; v++, at += 4) {
      view.setFloat32(at, coordinates[f * 9 + v], true);
    }
  }
  return buffer;
}

const CORE_NS = 'http://schemas.microsoft.com/3dmanufacturing/core/2015/02';
const PRODUCTION_NS = 'http://schemas.microsoft.com/3dmanufacturing/production/2015/06';

/** A `<mesh>` element covering an axis-aligned box, as a single object's body. */
export function boxMeshXml(sx: number, sy: number, sz: number): string {
  const coordinates = boxTriangles(sx, sy, sz);
  const vertices: string[] = [];
  const triangles: string[] = [];
  for (let i = 0; i < coordinates.length; i += 3) {
    vertices.push(
      `<vertex x="${coordinates[i]}" y="${coordinates[i + 1]}" z="${coordinates[i + 2]}"/>`,
    );
  }
  for (let t = 0; t < vertices.length; t += 3) {
    triangles.push(`<triangle v1="${t}" v2="${t + 1}" v3="${t + 2}"/>`);
  }
  return `<mesh><vertices>${vertices.join('')}</vertices><triangles>${triangles.join('')}</triangles></mesh>`;
}

export function modelXml(
  body: string,
  options: { unit?: string | null; production?: boolean } = {},
): string {
  const unit = options.unit === null ? '' : ` unit="${options.unit ?? 'millimeter'}"`;
  const production =
    options.production === false ? '' : ` xmlns:p="${PRODUCTION_NS}"`;
  return `<?xml version="1.0" encoding="UTF-8"?><model${unit} xml:lang="en-US" xmlns="${CORE_NS}"${production}>${body}</model>`;
}

/** `_rels/.rels` naming `target` as the package's start part. */
export function relsXml(target: string): string {
  return `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rel0" Target="${target}" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/></Relationships>`;
}

export function zip3mf(
  entries: Record<string, string>,
  options: { level?: 0 } = {},
): ArrayBuffer {
  const zippable: Zippable = {};
  const encoder = new TextEncoder();
  for (const [name, content] of Object.entries(entries)) {
    zippable[name] = encoder.encode(content);
  }
  const bytes = zipSync(zippable, options.level === 0 ? { level: 0 } : {});
  return bytes.buffer.slice(
    bytes.byteOffset,
    bytes.byteOffset + bytes.byteLength,
  ) as ArrayBuffer;
}

/**
 * Rewrite a zip's central directory so every entry claims an uncompressed size of zero
 * while keeping its real payload.
 *
 * Hand-crafted, because no producer writes this: it is what a hostile file looks like.
 * fflate copies a *stored* entry at its compressed size, so an entry declaring zero
 * still costs its full length - which is why the byte census cannot trust
 * `originalSize` alone.
 */
export function lieAboutUncompressedSize(buffer: ArrayBuffer): ArrayBuffer {
  const view = new DataView(buffer);
  for (let at = 0; at + 46 <= buffer.byteLength; at++) {
    if (view.getUint32(at, true) === 0x02014b50) view.setUint32(at + 24, 0, true);
  }
  return buffer;
}

export function coreOnly3mf(
  options: { unit?: string; items?: string; objects?: string } = {},
): ArrayBuffer {
  const objects =
    options.objects ??
    `<object id="1" type="model">${boxMeshXml(20, 10, 5)}</object>`;
  const items = options.items ?? '<item objectid="1"/>';
  return zip3mf({
    '_rels/.rels': relsXml('/3D/3dmodel.model'),
    '3D/3dmodel.model': modelXml(
      `<resources>${objects}</resources><build>${items}</build>`,
      { unit: options.unit },
    ),
  });
}
