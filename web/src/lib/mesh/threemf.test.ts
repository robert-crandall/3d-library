import { describe, expect, it } from 'vitest';
import { boundsOf, sizeOf } from '$lib/viewer/framing';
import {
  boxMeshXml,
  coreOnly3mf,
  lieAboutUncompressedSize,
  modelXml,
  relsXml,
  zip3mf,
} from './fixtures';
import { MAX_TRIANGLES, parse3mf } from './threemf';

function sizeOf3mf(buffer: ArrayBuffer): [number, number, number] {
  return sizeOf(boundsOf(parse3mf(buffer).positions));
}

const BOX = boxMeshXml(20, 10, 5);

describe('parse3mf: file shapes real slicers write', () => {
  it('reads a core-only file with geometry in the root part', () => {
    // PrusaSlicer's shape: one part, no production extension.
    const parsed = parse3mf(coreOnly3mf());
    expect(sizeOf(boundsOf(parsed.positions))).toEqual([20, 10, 5]);
    expect(parsed.objectCount).toBe(1);
  });

  it('reads geometry held in a separate part via the production extension', () => {
    // Bambu Studio's and Orca's shape, and the one every file in the sample corpus
    // used: the root part holds only the build, and each object lives in
    // 3D/Objects/object_N.model behind a p:path. A parser that only reads the root
    // renders nothing at all for these.
    const buffer = zip3mf({
      '_rels/.rels': relsXml('/3D/3dmodel.model'),
      '3D/3dmodel.model': modelXml(
        '<resources/><build><item objectid="1" p:path="/3D/Objects/object_1.model"/></build>',
      ),
      '3D/Objects/object_1.model': modelXml(
        `<resources><object id="1" type="model">${BOX}</object></resources>`,
      ),
    });
    expect(sizeOf3mf(buffer)).toEqual([20, 10, 5]);
  });

  it('reads a p:path written under a prefix other than "p"', () => {
    // The prefix is the producer's choice; only the namespace is fixed. Reading the
    // literal attribute name "p:path" passes the test above and fails here.
    const buffer = zip3mf({
      '_rels/.rels': relsXml('/3D/3dmodel.model'),
      '3D/3dmodel.model': `<?xml version="1.0"?><model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:prod="http://schemas.microsoft.com/3dmanufacturing/production/2015/06"><resources/><build><item objectid="1" prod:path="/3D/Objects/object_1.model"/></build></model>`,
      '3D/Objects/object_1.model': modelXml(
        `<resources><object id="1" type="model">${BOX}</object></resources>`,
      ),
    });
    expect(sizeOf3mf(buffer)).toEqual([20, 10, 5]);
  });

  it('follows a cross-part path on a component, not just on a build item', () => {
    // Assemblies reach into other parts the same way items do, and the two are read by
    // separate code paths - so the prefix case has to be covered on both.
    const buffer = zip3mf({
      '_rels/.rels': relsXml('/3D/3dmodel.model'),
      '3D/3dmodel.model': `<?xml version="1.0"?><model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:prod="http://schemas.microsoft.com/3dmanufacturing/production/2015/06"><resources><object id="7" type="model"><components><component objectid="1" prod:path="/3D/Objects/object_1.model" transform="1 0 0 0 1 0 0 0 1 40 0 0"/></components></object></resources><build><item objectid="7"/></build></model>`,
      '3D/Objects/object_1.model': modelXml(
        `<resources><object id="1" type="model">${BOX}</object></resources>`,
      ),
    });
    const bounds = boundsOf(parse3mf(buffer).positions);
    expect(bounds.min).toEqual([40, 0, 0]);
    expect(bounds.max).toEqual([60, 10, 5]);
  });

  it('keeps object ids from different parts apart', () => {
    // Every sampled Bambu file numbers each object part's object "1". Keying objects by
    // id alone makes the second item render the first one's mesh.
    const buffer = zip3mf({
      '_rels/.rels': relsXml('/3D/3dmodel.model'),
      '3D/3dmodel.model': modelXml(
        '<resources/><build>' +
          '<item objectid="1" p:path="/3D/Objects/object_1.model"/>' +
          '<item objectid="1" p:path="/3D/Objects/object_2.model" transform="1 0 0 0 1 0 0 0 1 100 0 0"/>' +
          '</build>',
      ),
      '3D/Objects/object_1.model': modelXml(
        `<resources><object id="1" type="model">${boxMeshXml(20, 10, 5)}</object></resources>`,
      ),
      '3D/Objects/object_2.model': modelXml(
        `<resources><object id="1" type="model">${boxMeshXml(4, 4, 60)}</object></resources>`,
      ),
    });
    const parsed = parse3mf(buffer);
    // 100 mm of offset plus the second box's 4 mm width; Z is the taller box.
    expect(sizeOf(boundsOf(parsed.positions))).toEqual([104, 10, 60]);
    expect(parsed.objectCount).toBe(2);
  });
});

describe('parse3mf: transforms', () => {
  it('places two build items of the same object', () => {
    const buffer = coreOnly3mf({
      items:
        '<item objectid="1"/><item objectid="1" transform="1 0 0 0 1 0 0 0 1 50 0 0"/>',
    });
    const parsed = parse3mf(buffer);
    expect(sizeOf(boundsOf(parsed.positions))).toEqual([70, 10, 5]);
    expect(parsed.objectCount).toBe(2);
  });

  it('composes a nested component transform in the right order', () => {
    // A 90 degree rotation about Z, then a translation along X. Composed the other way
    // round the translation is rotated too and the box lands on Y instead - which a
    // translation-only fixture cannot tell apart.
    const buffer = coreOnly3mf({
      objects:
        `<object id="1" type="model">${boxMeshXml(20, 10, 5)}</object>` +
        '<object id="2" type="model"><components>' +
        '<component objectid="1" transform="0 1 0 -1 0 0 0 0 1 0 0 0"/>' +
        '</components></object>',
      items: '<item objectid="2" transform="1 0 0 0 1 0 0 0 1 100 0 0"/>',
    });
    const bounds = boundsOf(parse3mf(buffer).positions);
    // The box spans x 0..20, y 0..10; rotating maps (x,y) -> (-y,x), giving x -10..0,
    // y 0..20, and the outer translation shifts x to 90..100.
    expect(bounds.min.map(Math.round)).toEqual([90, 0, 0]);
    expect(bounds.max.map(Math.round)).toEqual([100, 20, 5]);
  });

  it('renders a mirrored transform', () => {
    // A negative determinant reverses triangle winding. It must still contribute
    // geometry with the right bounds; back-face culling in the renderer is what would
    // make it disappear, which is why the materials are DoubleSide.
    const buffer = coreOnly3mf({
      items: '<item objectid="1" transform="-1 0 0 0 1 0 0 0 1 0 0 0"/>',
    });
    const bounds = boundsOf(parse3mf(buffer).positions);
    expect(bounds.min).toEqual([-20, 0, 0]);
    expect(bounds.max).toEqual([0, 10, 5]);
  });

  it('refuses a transform that is not twelve finite numbers', () => {
    expect(() =>
      parse3mf(coreOnly3mf({ items: '<item objectid="1" transform="1 0 0 nope"/>' })),
    ).toThrow(/corrupt/);
  });
});

describe('parse3mf: units', () => {
  it('converts inches to millimetres', () => {
    // The reason this parser exists instead of three.js's 3MFLoader, which reads the
    // unit attribute and never applies it.
    const size = sizeOf3mf(coreOnly3mf({ unit: 'inch' }));
    expect(size[0]).toBeCloseTo(508, 3);
    expect(size[1]).toBeCloseTo(254, 3);
  });

  it('converts every unit the core spec names', () => {
    const expected: Record<string, number> = {
      micron: 0.02,
      millimeter: 20,
      centimeter: 200,
      inch: 508,
      foot: 6096,
      meter: 20000,
    };
    for (const [unit, x] of Object.entries(expected)) {
      expect(sizeOf3mf(coreOnly3mf({ unit }))[0]).toBeCloseTo(x, 2);
    }
  });

  it('treats a missing unit as millimetres', () => {
    const buffer = zip3mf({
      '_rels/.rels': relsXml('/3D/3dmodel.model'),
      '3D/3dmodel.model': modelXml(
        `<resources><object id="1" type="model">${BOX}</object></resources><build><item objectid="1"/></build>`,
        { unit: null },
      ),
    });
    expect(sizeOf3mf(buffer)).toEqual([20, 10, 5]);
  });

  it('accepts a child part that omits the unit under a millimetre root', () => {
    // Legal, and a naive string comparison of the two attributes rejects it.
    const buffer = zip3mf({
      '_rels/.rels': relsXml('/3D/3dmodel.model'),
      '3D/3dmodel.model': modelXml(
        '<resources/><build><item objectid="1" p:path="/3D/Objects/object_1.model"/></build>',
      ),
      '3D/Objects/object_1.model': modelXml(
        `<resources><object id="1" type="model">${BOX}</object></resources>`,
        { unit: null },
      ),
    });
    expect(sizeOf3mf(buffer)).toEqual([20, 10, 5]);
  });

  it('refuses a file whose parts genuinely disagree about units', () => {
    // Rendering it would show one object 25.4x the size of the other and report a
    // dimension that is true of neither.
    const buffer = zip3mf({
      '_rels/.rels': relsXml('/3D/3dmodel.model'),
      '3D/3dmodel.model': modelXml(
        '<resources/><build><item objectid="1" p:path="/3D/Objects/object_1.model"/></build>',
      ),
      '3D/Objects/object_1.model': modelXml(
        `<resources><object id="1" type="model">${BOX}</object></resources>`,
        { unit: 'inch' },
      ),
    });
    expect(() => parse3mf(buffer)).toThrow(/mixes units/);
  });

  it('refuses an unknown unit rather than guessing millimetres', () => {
    expect(() => parse3mf(coreOnly3mf({ unit: 'furlong' }))).toThrow(/unsupported unit/);
  });
});

describe('parse3mf: part resolution', () => {
  it('follows a start part that is not at the conventional path', () => {
    const buffer = zip3mf({
      '_rels/.rels': relsXml('/Models/root.model'),
      'Models/root.model': modelXml(
        `<resources><object id="1" type="model">${BOX}</object></resources><build><item objectid="1"/></build>`,
      ),
    });
    expect(sizeOf3mf(buffer)).toEqual([20, 10, 5]);
  });

  it('resolves a relative start part target', () => {
    // Relationship targets are URI references, so "./" and bare relative forms are as
    // legal as the absolute one every sampled file happens to write.
    const buffer = zip3mf({
      '_rels/.rels': relsXml('./Models/root.model'),
      'Models/root.model': modelXml(
        `<resources><object id="1" type="model">${BOX}</object></resources><build><item objectid="1"/></build>`,
      ),
    });
    expect(sizeOf3mf(buffer)).toEqual([20, 10, 5]);
  });

  it('refuses a target that climbs out of the package', () => {
    const buffer = zip3mf({
      '_rels/.rels': relsXml('/../../etc/passwd'),
      '3D/3dmodel.model': modelXml(
        `<resources><object id="1" type="model">${BOX}</object></resources><build><item objectid="1"/></build>`,
      ),
    });
    // Refused, not quietly redirected to the conventional path: the package named a root
    // and that root is not something this file is allowed to reach.
    expect(() => parse3mf(buffer)).toThrow(/corrupt/i);
  });

  it('refuses a file with no readable root part', () => {
    expect(() => parse3mf(zip3mf({ '_rels/.rels': relsXml('/3D/nope.model') }))).toThrow(
      /corrupt/,
    );
  });

  it('refuses a p:path pointing at a part that is not in the zip', () => {
    const buffer = zip3mf({
      '_rels/.rels': relsXml('/3D/3dmodel.model'),
      '3D/3dmodel.model': modelXml(
        '<resources/><build><item objectid="1" p:path="/3D/Objects/missing.model"/></build>',
      ),
    });
    expect(() => parse3mf(buffer)).toThrow(/corrupt/);
  });

  it('refuses an item naming an object that does not exist', () => {
    expect(() => parse3mf(coreOnly3mf({ items: '<item objectid="99"/>' }))).toThrow(
      /corrupt/,
    );
  });
});

describe('parse3mf: malformed geometry', () => {
  it('refuses a triangle index past the end of the vertex list', () => {
    // Left unchecked this reads undefined out of the array and writes NaN into the
    // buffer, which renders as a blank canvas and a NaN dimension rather than an error.
    const buffer = coreOnly3mf({
      objects: `<object id="1" type="model">${BOX.replace('v1="0"', 'v1="9999"')}</object>`,
    });
    expect(() => parse3mf(buffer)).toThrow(/corrupt/);
  });

  it('refuses a non-finite vertex coordinate', () => {
    const buffer = coreOnly3mf({
      objects: `<object id="1" type="model">${BOX.replace('x="0"', 'x="oops"')}</object>`,
    });
    expect(() => parse3mf(buffer)).toThrow(/corrupt/);
  });

  it('refuses a file with a build but no geometry', () => {
    const buffer = coreOnly3mf({
      objects: '<object id="1" type="model"><mesh><vertices/><triangles/></mesh></object>',
    });
    expect(() => parse3mf(buffer)).toThrow(/no printable geometry/);
  });

  it('refuses XML that does not parse', () => {
    const buffer = zip3mf({
      '_rels/.rels': relsXml('/3D/3dmodel.model'),
      '3D/3dmodel.model': '<model><resources>',
    });
    expect(() => parse3mf(buffer)).toThrow(/corrupt/);
  });

  it('refuses something that is not a zip at all', () => {
    expect(() => parse3mf(new TextEncoder().encode('not a zip').buffer as ArrayBuffer)).toThrow();
  });
});

describe('parse3mf: bounds on hostile input', () => {
  it('refuses a package whose parts exceed the byte cap', () => {
    // 40 MB of model XML that compresses to a few KB. A cap applied after inflation
    // would already have paid for the memory; fflate's filter runs before it allocates,
    // which is what makes this a real bound rather than a courtesy.
    const buffer = zip3mf({
      '_rels/.rels': relsXml('/3D/3dmodel.model'),
      '3D/3dmodel.model': modelXml(
        `<resources><object id="1" type="model">${BOX}</object></resources><build><item objectid="1"/></build>`,
      ),
      '3D/Objects/filler.model': '<!--' + 'x'.repeat(40 * 1024 * 1024) + '-->',
    });
    expect(() => parse3mf(buffer)).toThrow(/too large/);
  });

  it('refuses a package whose central directory understates its entry sizes', () => {
    // Hostile, not produced by anything: the entry claims an uncompressed size of zero
    // and is stored, so fflate still copies its full length. Trusting `originalSize`
    // alone lets 8 MB through a cap that was supposed to stop it, and the same trick at
    // 500 MB is the memory the cap exists to refuse.
    const buffer = lieAboutUncompressedSize(
      zip3mf(
        {
          '_rels/.rels': relsXml('/3D/3dmodel.model'),
          '3D/3dmodel.model': modelXml(
            `<resources><object id="1" type="model">${BOX}</object></resources><build><item objectid="1"/></build>`,
          ),
          '3D/Objects/filler.model': '<!--' + 'x'.repeat(40 * 1024 * 1024) + '-->',
        },
        { level: 0 },
      ),
    );
    expect(() => parse3mf(buffer)).toThrow(/too large/);
  });

  it('reads a package that sits just under the byte cap', () => {
    // The counterpart to the test above: the cap must not refuse a legitimately large
    // project file. The biggest in the sample corpus needed 16.8 MB of model XML.
    const buffer = zip3mf({
      '_rels/.rels': relsXml('/3D/3dmodel.model'),
      '3D/3dmodel.model': modelXml(
        `<resources><object id="1" type="model">${BOX}</object></resources><build><item objectid="1"/></build>`,
      ),
      '3D/Objects/filler.model': '<!--' + 'x'.repeat(20 * 1024 * 1024) + '-->',
    });
    expect(sizeOf3mf(buffer)).toEqual([20, 10, 5]);
  });

  it('refuses fan-out that multiplies a few KB of XML past the triangle cap', () => {
    // The byte cap does not bound this: each level multiplies, so five levels of 20
    // components each is 3.2M placements of one box from about 40 KB of XML. This is
    // the case that makes a separate triangle cap necessary.
    const objects = [`<object id="0" type="model">${boxMeshXml(1, 1, 1)}</object>`];
    for (let level = 1; level <= 5; level++) {
      const children = Array.from(
        { length: 20 },
        () => `<component objectid="${level - 1}"/>`,
      ).join('');
      objects.push(
        `<object id="${level}" type="model"><components>${children}</components></object>`,
      );
    }
    const buffer = coreOnly3mf({
      objects: objects.join(''),
      items: '<item objectid="5"/>',
    });
    expect(20 ** 5 * 12).toBeGreaterThan(MAX_TRIANGLES);
    expect(() => parse3mf(buffer)).toThrow(/too large/);
  });

  it('refuses a component cycle', () => {
    const buffer = coreOnly3mf({
      objects:
        '<object id="1" type="model"><components><component objectid="2"/></components></object>' +
        '<object id="2" type="model"><components><component objectid="1"/></components></object>',
      items: '<item objectid="1"/>',
    });
    expect(() => parse3mf(buffer)).toThrow(/corrupt/);
  });

  it('refuses nesting deeper than the depth limit', () => {
    // Acyclic, so the cycle check alone lets this recurse until the stack goes.
    const objects = [`<object id="0" type="model">${boxMeshXml(1, 1, 1)}</object>`];
    for (let level = 1; level <= 40; level++) {
      objects.push(
        `<object id="${level}" type="model"><components><component objectid="${level - 1}"/></components></object>`,
      );
    }
    const buffer = coreOnly3mf({
      objects: objects.join(''),
      items: '<item objectid="40"/>',
    });
    expect(() => parse3mf(buffer)).toThrow(/corrupt/);
  });

  it('allows the same object to appear twice on different branches', () => {
    // The cycle check must be scoped to the current path, not to everything visited: a
    // shared sub-assembly is normal and a global visited set would refuse it.
    const buffer = coreOnly3mf({
      objects:
        `<object id="1" type="model">${boxMeshXml(10, 10, 10)}</object>` +
        '<object id="2" type="model"><components>' +
        '<component objectid="1"/>' +
        '<component objectid="1" transform="1 0 0 0 1 0 0 0 1 30 0 0"/>' +
        '</components></object>',
      items: '<item objectid="2"/>',
    });
    const parsed = parse3mf(buffer);
    expect(sizeOf(boundsOf(parsed.positions))).toEqual([40, 10, 10]);
    expect(parsed.objectCount).toBe(1);
  });

  it('parses a part with more vertices than fit in an argument list', () => {
    // A real 3.8 MB Bambu 3MF has ~110,000 vertices in one part, which is past the engine's
    // limit on spread arguments. Collecting them with `push(...nodes)` throws "Maximum call
    // stack size exceeded" on that file and on nothing smaller, so the count here has to
    // stay well over 65,536 to be worth anything.
    const count = 120_000;
    const vertices: string[] = [];
    const triangles: string[] = [];
    for (let i = 0; i < count; i++) {
      vertices.push(`<vertex x="${i % 7}" y="${i % 5}" z="${i % 3}"/>`);
    }
    for (let t = 0; t + 2 < count; t += 3) {
      triangles.push(`<triangle v1="${t}" v2="${t + 1}" v3="${t + 2}"/>`);
    }
    const mesh = `<mesh><vertices>${vertices.join('')}</vertices><triangles>${triangles.join('')}</triangles></mesh>`;

    const parsed = parse3mf(
      coreOnly3mf({ objects: `<object id="1" type="model">${mesh}</object>` }),
    );

    expect(parsed.positions.length).toBe(count * 3);
  }, 60_000);

  it('reads a relationship written with a namespace prefix', () => {
    // The OPC prefix is the producer's choice. Matching on the tag name misses
    // `<r:Relationship>`, and the package then falls back to the conventional path -
    // which for a file rooted elsewhere means rendering nothing at all.
    const rels =
      `<?xml version="1.0" encoding="UTF-8"?>` +
      `<r:Relationships xmlns:r="http://schemas.openxmlformats.org/package/2006/relationships">` +
      `<r:Relationship Id="rel0" Target="/3D/elsewhere.model"` +
      ` Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/>` +
      `</r:Relationships>`;
    const buffer = zip3mf({
      '_rels/.rels': rels,
      '3D/elsewhere.model': modelXml(
        `<resources><object id="1" type="model">${boxMeshXml(4, 6, 8)}</object></resources>` +
          `<build><item objectid="1"/></build>`,
      ),
    });

    const size = sizeOf(boundsOf(parse3mf(buffer).positions));

    expect(size[0]).toBeCloseTo(4);
    expect(size[1]).toBeCloseTo(6);
    expect(size[2]).toBeCloseTo(8);
  });

  it('refuses a package whose declared root part is missing', () => {
    // Falling back to the conventional path here would render whatever happens to sit
    // there and label it with this file's name - a different mesh with different
    // dimensions, presented as if it were the one asked for.
    const buffer = zip3mf({
      '_rels/.rels': relsXml('/3D/gone.model'),
      '3D/3dmodel.model': modelXml(
        `<resources><object id="1" type="model">${boxMeshXml(1, 1, 1)}</object></resources>` +
          `<build><item objectid="1"/></build>`,
      ),
    });

    expect(() => parse3mf(buffer)).toThrow(/corrupt/i);
  });

  it('refuses a vertex that omits a coordinate', () => {
    // Number(null) is 0, so an absent z silently flattens the vertex onto the origin
    // plane and the file renders as a subtly wrong shape with wrong dimensions.
    const mesh =
      `<mesh><vertices>` +
      `<vertex x="0" y="0" z="0"/><vertex x="1" y="0" z="0"/><vertex x="0" y="1"/>` +
      `</vertices><triangles><triangle v1="0" v2="1" v3="2"/></triangles></mesh>`;

    expect(() =>
      parse3mf(coreOnly3mf({ objects: `<object id="1" type="model">${mesh}</object>` })),
    ).toThrow(/corrupt/i);
  });

  it('refuses a triangle that omits a corner', () => {
    // Number(null) is 0, which is a perfectly valid vertex index - so the triangle would
    // be silently rewritten to point at vertex zero rather than reported.
    const mesh =
      `<mesh><vertices>` +
      `<vertex x="0" y="0" z="0"/><vertex x="1" y="0" z="0"/><vertex x="0" y="1" z="0"/>` +
      `</vertices><triangles><triangle v1="0" v2="1"/></triangles></mesh>`;

    expect(() =>
      parse3mf(coreOnly3mf({ objects: `<object id="1" type="model">${mesh}</object>` })),
    ).toThrow(/corrupt/i);
  });

  it('refuses a coordinate that is finite but overflows a float32', () => {
    // 1e100 passes Number.isFinite and then becomes Infinity in the Float32Array the
    // positions live in, which reaches the camera as NaN and shows as a blank canvas.
    const mesh =
      `<mesh><vertices>` +
      `<vertex x="0" y="0" z="0"/><vertex x="1e100" y="0" z="0"/><vertex x="0" y="1" z="0"/>` +
      `</vertices><triangles><triangle v1="0" v2="1" v3="2"/></triangles></mesh>`;

    expect(() =>
      parse3mf(coreOnly3mf({ objects: `<object id="1" type="model">${mesh}</object>` })),
    ).toThrow(/corrupt/i);
  });

  it('refuses a transform that pushes a legal vertex past a float32', () => {
    // Every vertex here fits a float32 comfortably. The overflow only exists once the
    // build item's translation is applied, so a check on the raw attributes cannot see it.
    expect(() =>
      parse3mf(
        coreOnly3mf({ items: '<item objectid="1" transform="1 0 0 0 1 0 0 0 1 1e100 0 0"/>' }),
      ),
    ).toThrow(/corrupt/i);
  });

  it('accepts a vertex that only fits a float32 after the unit scale shrinks it', () => {
    // The mirror of the case above, and the reason the check cannot live on the raw
    // attribute: 1e39 overflows a float32 on its own but is 1e36 once micron becomes
    // millimetre. The magnitudes are absurd for a printable part - what this pins is the
    // stage the check happens at, not a file anyone will open.
    const mesh =
      `<mesh><vertices>` +
      `<vertex x="0" y="0" z="0"/><vertex x="1e39" y="0" z="0"/><vertex x="0" y="1e39" z="0"/>` +
      `</vertices><triangles><triangle v1="0" v2="1" v3="2"/></triangles></mesh>`;

    const parsed = parse3mf(
      coreOnly3mf({
        unit: 'micron',
        objects: `<object id="1" type="model">${mesh}</object>`,
      }),
    );

    expect(sizeOf(boundsOf(parsed.positions))[0] / 1e36).toBeCloseTo(1, 5);
  });

  it('refuses a blank transform rather than treating it as identity', () => {
    // An empty attribute is a malformed one. Reading it as identity renders the object at
    // the origin and reports dimensions the file never claimed.
    expect(() => parse3mf(coreOnly3mf({ items: '<item objectid="1" transform=""/>' }))).toThrow(
      /corrupt/i,
    );
  });
});
