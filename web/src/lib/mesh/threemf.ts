import { unzipSync } from 'fflate';
import type { ParsedMesh } from './geometry';

// A 3MF is a zip of XML. Unlike an STL it has units, a resource graph and a build plate,
// so most of this file is about resolving references correctly and refusing files that
// would cost the tab more memory than a preview is worth.
//
// Three.js ships a 3MFLoader, and it is not used here: it reads `<model unit>` and never
// applies it, so an inch-unit file renders 25.4x too small and reports the wrong
// dimensions. Getting the unit wrong is worse than showing no number at all.

const CORE_NS = 'http://schemas.microsoft.com/3dmanufacturing/core/2015/02';
const PRODUCTION_NS = 'http://schemas.microsoft.com/3dmanufacturing/production/2015/06';
const RELS_NS = 'http://schemas.openxmlformats.org/package/2006/relationships';

/**
 * Total bytes of zip entries we are willing to hold. The largest real project file in
 * the sample corpus needs 16.8 MB of model XML; the DOM built from that is several times
 * larger again, which is why this sits well under the fetch cap.
 */
export const MAX_MODEL_BYTES = 32 * 1024 * 1024;

/**
 * Triangles we are willing to emit. The byte cap does not bound this: components
 * multiply, so an object referencing another a thousand times, twice over, expands a few
 * kilobytes of XML into millions of triangles. Set just above what the fetch cap allows a
 * binary STL to carry (~2.09M), so the two formats fail at comparable sizes.
 */
export const MAX_TRIANGLES = 3_000_000;

/** Deeply nested components are legal; unbounded recursion is not. */
const MAX_DEPTH = 32;

const TOO_LARGE = 'This 3MF is too large to preview.';
const CORRUPT = 'This 3MF file is corrupt or unreadable.';

/** 3MF core spec unit names, as a multiplier to millimetres. */
const UNITS: Record<string, number> = {
  micron: 0.001,
  millimeter: 1,
  centimeter: 10,
  inch: 25.4,
  foot: 304.8,
  meter: 1000,
};

/**
 * Resolve an OPC part reference to a zip entry name.
 *
 * Relationship targets are URI *references*: `/3D/3dmodel.model` (what every real file
 * writes), or `./Models/root.model`, or a bare relative path. Zip entry names carry no
 * leading slash, so normalise to that. A reference that climbs out of the package with
 * `..` cannot name an entry, so it resolves to nothing rather than to something
 * plausible.
 */
function resolvePart(reference: string): string | null {
  const segments: string[] = [];
  for (const segment of reference.replace(/^\//, '').split('/')) {
    if (segment === '' || segment === '.') continue;
    if (segment === '..') {
      if (segments.length === 0) return null;
      segments.pop();
      continue;
    }
    segments.push(segment);
  }
  return segments.length > 0 ? segments.join('/') : null;
}

/**
 * Read every entry we might need, refusing the file once they exceed the byte cap.
 *
 * fflate calls the filter before it allocates anything for an entry, so this is a real
 * bound rather than a courtesy. It counts `max(size, originalSize)` because fflate copies
 * a *stored* entry at its compressed size and only inflates a *deflated* one into a
 * buffer of its original size; counting `originalSize` alone would let a stored entry
 * declaring zero slip through.
 */
function readParts(bytes: Uint8Array): Record<string, Uint8Array> {
  let budget = MAX_MODEL_BYTES;
  let exceeded = false;

  const parts = unzipSync(bytes, {
    filter: (file) => {
      if (exceeded) return false;
      const wanted = file.name.endsWith('.model') || file.name === '_rels/.rels';
      if (!wanted) return false;
      budget -= Math.max(file.size, file.originalSize);
      if (budget < 0) {
        exceeded = true;
        return false;
      }
      return true;
    },
  });

  if (exceeded) throw new Error(TOO_LARGE);
  return parts;
}

function parseXml(bytes: Uint8Array, what: string): Document {
  const document = new DOMParser().parseFromString(
    new TextDecoder().decode(bytes),
    'text/xml',
  );
  if (document.getElementsByTagName('parsererror').length > 0) {
    throw new Error(`${CORRUPT} (${what})`);
  }
  return document;
}

/**
 * Which part holds the root model. OPC defines this by the StartPart relationship, and
 * the production extension's own examples root somewhere other than the conventional
 * path, so the relationship is read first and the convention is only the fallback.
 */
function findRootPart(parts: Record<string, Uint8Array>): string {
  const rels = parts['_rels/.rels'];
  if (rels) {
    const document = parseXml(rels, '_rels/.rels');
    // By namespace, not by tag name: the OPC prefix is the producer's choice, so a
    // package using `<r:Relationship>` is legal and getElementsByTagName misses it.
    const declared = Array.from(
      document.getElementsByTagNameNS(RELS_NS, 'Relationship'),
    ).filter((rel) => rel.getAttribute('Type')?.endsWith('/3dmodel'));

    if (declared.length > 0) {
      for (const rel of declared) {
        const target = rel.getAttribute('Target');
        const resolved = target ? resolvePart(target) : null;
        if (resolved && parts[resolved]) return resolved;
      }
      // The package said where its model is and that part is not here. Falling through to
      // the conventional path would render whatever happens to sit there - a different
      // mesh, with different dimensions, presented as if it were this file.
      throw new Error(CORRUPT);
    }
  }
  if (parts['3D/3dmodel.model']) return '3D/3dmodel.model';
  throw new Error(CORRUPT);
}

type Matrix = number[]; // 12 numbers: a row-major 4x3 affine transform.

const IDENTITY: Matrix = [1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0];

function parseMatrix(value: string | null): Matrix {
  // A blank `transform=""` is not identity, it is a malformed attribute, so only an
  // absent one falls through to identity.
  if (value === null) return IDENTITY;
  const numbers = value.trim().split(/\s+/).map(Number);
  if (numbers.length !== 12 || numbers.some((n) => !Number.isFinite(n))) {
    throw new Error(CORRUPT);
  }
  return numbers;
}

/** `child` applied first, then `parent`. Both are row-major, translation in the last row. */
function compose(parent: Matrix, child: Matrix): Matrix {
  const out = new Array<number>(12);
  for (let row = 0; row < 4; row++) {
    for (let column = 0; column < 3; column++) {
      let sum = 0;
      for (let k = 0; k < 3; k++) sum += child[row * 3 + k] * parent[k * 3 + column];
      // The translation row also picks up the parent's translation.
      out[row * 3 + column] = row === 3 ? sum + parent[9 + column] : sum;
    }
  }
  return out;
}

type Mesh = { vertices: Float64Array; triangles: Int32Array };

/**
 * Direct children in the core namespace with this local name.
 *
 * Deliberately not getElementsByTagNameNS: that returns a live collection, and jsdom
 * walks the subtree on every access to one. On a 1.1 MB Bambu 3MF those calls alone were
 * 4.7 of a 4.8 second parse. Walking children is also the stricter reading - `<mesh>`
 * means this object's own mesh, not any mesh anywhere beneath it.
 */
function childrenNamed(parent: Element, name: string): Element[] {
  const out: Element[] = [];
  for (let node = parent.firstElementChild; node; node = node.nextElementSibling) {
    if (node.localName === name && node.namespaceURI === CORE_NS) out.push(node);
  }
  return out;
}

/** Grandchildren: `<parent><wrapper><name/>…</wrapper></parent>`, flattened. */
function nestedChildren(parent: Element, wrapper: string, name: string): Element[] {
  const out: Element[] = [];
  for (const node of childrenNamed(parent, wrapper)) {
    // Appended one at a time, not spread: `push(...items)` passes every element as an
    // argument, and a real 3.8 MB Bambu 3MF has enough vertices in one part to overflow
    // the call stack that way.
    for (const child of childrenNamed(node, name)) out.push(child);
  }
  return out;
}

/**
 * A required numeric attribute.
 *
 * `Number(null)` and `Number('')` are both 0, so an absent `z` would silently place the
 * vertex on the origin plane and an absent `v1` would resolve to a real vertex.
 *
 * The magnitude is deliberately not checked here. A vertex is only ever seen through a
 * transform and a unit scale, so whether it fits the Float32 buffer is not knowable until
 * `emit` has multiplied it out.
 */
function requiredNumber(node: Element, name: string): number {
  const raw = node.getAttribute(name);
  if (raw === null || raw.trim() === '') throw new Error(CORRUPT);
  const value = Number(raw);
  if (!Number.isFinite(value)) throw new Error(CORRUPT);
  return value;
}

function readMesh(mesh: Element): Mesh {
  const vertexNodes = nestedChildren(mesh, 'vertices', 'vertex');
  const vertices = new Float64Array(vertexNodes.length * 3);
  for (let i = 0; i < vertexNodes.length; i++) {
    const node = vertexNodes[i];
    for (const [axis, name] of (['x', 'y', 'z'] as const).entries()) {
      vertices[i * 3 + axis] = requiredNumber(node, name);
    }
  }

  const triangleNodes = nestedChildren(mesh, 'triangles', 'triangle');
  const triangles = new Int32Array(triangleNodes.length * 3);
  for (let i = 0; i < triangleNodes.length; i++) {
    const node = triangleNodes[i];
    for (const [corner, name] of (['v1', 'v2', 'v3'] as const).entries()) {
      const index = requiredNumber(node, name);
      // An out-of-range index would read undefined out of the vertex array and put NaN
      // into the buffer, which shows up as a blank canvas and NaN dimensions rather than
      // as an error - so check it here, where we can still say what went wrong.
      if (!Number.isInteger(index) || index < 0 || index >= vertexNodes.length) {
        throw new Error(CORRUPT);
      }
      triangles[i * 3 + corner] = index;
    }
  }
  return { vertices, triangles };
}

type Part = {
  /** Objects in this part, by id. */
  objects: Map<string, Element>;
  document: Document;
};

/** A component reference with its part already resolved. */
type Component = { path: string; id: string; matrix: Matrix };

/** An object's contents, read out of the DOM once. */
type Resolved = { mesh?: Mesh; components: Component[] };

export function parse3mf(buffer: ArrayBuffer): ParsedMesh {
  const parts = readParts(new Uint8Array(buffer));
  const rootPath = findRootPart(parts);

  const loaded = new Map<string, Part>();
  const loadPart = (path: string): Part => {
    const cached = loaded.get(path);
    if (cached) return cached;

    const bytes = parts[path];
    if (!bytes) throw new Error(CORRUPT);
    const document = parseXml(bytes, path);

    const objects = new Map<string, Element>();
    for (const object of nestedChildren(document.documentElement, 'resources', 'object')) {
      const id = object.getAttribute('id');
      // Ids are scoped to their part, so two parts may both contain an id of 1 - which
      // the sampled Bambu files really do. Keying per part is what keeps them apart.
      if (id) objects.set(id, object);
    }

    const part = { objects, document };
    loaded.set(path, part);
    return part;
  };

  const unitOf = (part: Part): number => {
    const model = part.document.documentElement;
    // A missing unit is millimetre per the core spec, so compare effective units - a
    // millimetre root with a child that simply omits the attribute is legal.
    const name = model.getAttribute('unit') ?? 'millimeter';
    const scale = UNITS[name];
    if (scale === undefined) {
      throw new Error(`This 3MF uses an unsupported unit (${name}).`);
    }
    return scale;
  };

  const rootPart = loadPart(rootPath);
  const scale = unitOf(rootPart);

  // A plain array would box every coordinate at 8 bytes; at the triangle cap that is
  // most of a gigabyte for a buffer that ends up as 108 MB of Float32. Grow by doubling
  // instead - the emitted count is not known until the walk finishes, because components
  // multiply.
  let positions = new Float32Array(3 * 1024);
  let used = 0;
  let triangleCount = 0;

  const emit = (mesh: Mesh, matrix: Matrix) => {
    // The buffer is Float32, so a coordinate that is finite in a double can still land as
    // Infinity - and Infinity reaches the camera as NaN, which shows as a blank canvas
    // with no error. Vertices, transforms and the unit scale all multiply, so this is the
    // first point at which the final magnitude is known. Checked by reading the slot back
    // rather than by `Math.fround`, because what matters is what the buffer stores.
    const put = (value: number) => {
      positions[used] = value;
      if (!Number.isFinite(positions[used])) throw new Error(CORRUPT);
      used++;
    };

    for (let t = 0; t < mesh.triangles.length; t += 3) {
      // Checked before appending, so the buffer never grows past the cap.
      if (++triangleCount > MAX_TRIANGLES) throw new Error(TOO_LARGE);
      if (used + 9 > positions.length) {
        const grown = new Float32Array(Math.max(positions.length * 2, used + 9));
        grown.set(positions.subarray(0, used));
        positions = grown;
      }
      for (let corner = 0; corner < 3; corner++) {
        const v = mesh.triangles[t + corner] * 3;
        const x = mesh.vertices[v];
        const y = mesh.vertices[v + 1];
        const z = mesh.vertices[v + 2];
        put((x * matrix[0] + y * matrix[3] + z * matrix[6] + matrix[9]) * scale);
        put((x * matrix[1] + y * matrix[4] + z * matrix[7] + matrix[10]) * scale);
        put((x * matrix[2] + y * matrix[5] + z * matrix[8] + matrix[11]) * scale);
      }
    }
  };

  // `visiting` catches cycles; `depth` catches nesting that is deep but acyclic, which a
  // visited set alone would happily recurse to a stack overflow.
  const visiting = new Set<string>();

  // Read out of the DOM once per object rather than once per placement. Reuse is the
  // whole point of components - the sampled MakerWorld pack places one object 124 times -
  // and re-reading its vertices from the DOM on each visit turns a small file into
  // seconds of parsing.
  const resolved = new Map<Element, Resolved>();
  const resolveObject = (object: Element, partPath: string): Resolved => {
    const cached = resolved.get(object);
    if (cached) return cached;

    let mesh: Mesh | undefined;
    for (const node of childrenNamed(object, 'mesh')) mesh = readMesh(node);

    const components: Component[] = [];
    for (const node of nestedChildren(object, 'components', 'component')) {
      const id = node.getAttribute('objectid');
      if (!id) throw new Error(CORRUPT);
      // Read by namespace, not by the `p:` prefix: the prefix is a convention of the
      // producer, and the namespace is what the spec actually fixes.
      const path = node.getAttributeNS(PRODUCTION_NS, 'path');
      const childPath = path ? resolvePart(path) : partPath;
      if (!childPath) throw new Error(CORRUPT);
      components.push({
        path: childPath,
        id,
        matrix: parseMatrix(node.getAttribute('transform')),
      });
    }

    const value = { mesh, components };
    resolved.set(object, value);
    return value;
  };

  const walk = (partPath: string, objectId: string, matrix: Matrix, depth: number) => {
    if (depth > MAX_DEPTH) throw new Error(CORRUPT);

    const key = `${partPath}#${objectId}`;
    if (visiting.has(key)) throw new Error(CORRUPT);
    visiting.add(key);

    const part = loadPart(partPath);
    if (unitOf(part) !== scale) {
      throw new Error('This 3MF mixes units between its parts.');
    }

    const object = part.objects.get(objectId);
    if (!object) throw new Error(CORRUPT);

    const { mesh, components } = resolveObject(object, partPath);
    if (mesh) emit(mesh, matrix);
    for (const component of components) {
      walk(component.path, component.id, compose(matrix, component.matrix), depth + 1);
    }

    visiting.delete(key);
  };

  const items = nestedChildren(rootPart.document.documentElement, 'build', 'item');

  for (const item of items) {
    const objectId = item.getAttribute('objectid');
    if (!objectId) throw new Error(CORRUPT);
    const path = item.getAttributeNS(PRODUCTION_NS, 'path');
    const itemPath = path ? resolvePart(path) : rootPath;
    if (!itemPath) throw new Error(CORRUPT);
    walk(itemPath, objectId, parseMatrix(item.getAttribute('transform')), 0);
  }

  if (used === 0) throw new Error('This 3MF contains no printable geometry.');

  // One build item is one object on the plate, which is what a slicer shows and so what
  // the user counts. Placing the same object twice counts twice; a component assembly
  // nested under one item counts once.
  return { positions: positions.subarray(0, used), objectCount: items.length };
}
