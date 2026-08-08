import { describe, expect, it } from 'vitest';
import {
  createToolpathParser,
  layerRange,
  skipSegments,
  spreadRange,
  type Toolpath,
  type ToolpathOptions,
} from './toolpath';

function parse(source: string, options?: ToolpathOptions): Toolpath {
  const parser = createToolpathParser(options);
  parser.push(new TextEncoder().encode(source));
  return parser.finish();
}

/** Every segment as `[x0, y0, z0, x1, y1, z1]`, flattened out of its chunk. */
function segments(chunks: readonly Float32Array[]): number[][] {
  const out: number[][] = [];
  for (const chunk of chunks) {
    for (let i = 0; i + 5 < chunk.length; i += 6) {
      out.push([
        chunk[i],
        chunk[i + 1],
        chunk[i + 2],
        chunk[i + 3],
        chunk[i + 4],
        chunk[i + 5],
      ]);
    }
  }
  return out;
}

function rounded(chunks: readonly Float32Array[], places = 4): number[][] {
  const factor = 10 ** places;
  return segments(chunks).map((s) => s.map((v) => Math.round(v * factor) / factor));
}

/** A file that has extruded once already, so a test can start from a known position. */
const PRIMED = ['G21', 'G90', 'M82', 'G1 X0 Y0 Z0.2', 'G1 X1 Y0 E1'].join('\n');

/**
 * `PRIMED` plus a marker, which anchors layer one. Absorption only ever applies to the
 * first marker of a file, so a table testing whether some comment counts as a marker
 * has to get past that case first.
 */
const ANCHORED = [PRIMED, ';LAYER_CHANGE', 'G1 X2 Y0 E2'].join('\n');

describe('layer detection', () => {
  it('absorbs a purge line printed at the first layer Z into layer one', () => {
    // PrusaSlicer's shape: the start G-code extrudes a purge line, and only then does
    // the first `;LAYER_CHANGE` arrive. Opening a layer of its own for the purge would
    // report one more layer than the slicer does, on every Prusa file.
    const parsed = parse(
      [
        'G90',
        'M82',
        'G1 X0 Y0 Z0.2',
        'G1 X100 Y0 E5', // the purge line
        ';LAYER_CHANGE',
        ';Z:0.2',
        'G1 X10 Y10 E6',
      ].join('\n'),
    );
    expect(parsed.layers).toHaveLength(1);
    expect(parsed.layers[0].z).toBe(0.2);
    // The purge segment is still drawn and still belongs to the layer; it is only left
    // out of the camera fit.
    expect(parsed.extrusionSegments).toBe(2);
    expect(parsed.purgeSegments).toBe(1);
    // Bounds start at the first post-purge extrusion, so the purge line running out to
    // X100 at Y0 does not stretch them. Asserting the segment counts alone let a version
    // that drew the right layers and framed the camera around the purge pass.
    expect(parsed.bounds).toEqual({ min: [10, 0, 0.2], max: [100, 10, 0.2] });
  });

  it('does not absorb a real preceding layer, which is at a different Z', () => {
    // The same shape as the purge case except that Z moved, which is the whole
    // difference between "this was a purge line" and "the first marker arrived late".
    const parsed = parse(
      [
        'G90',
        'M82',
        'G1 X0 Y0 Z0.2',
        'G1 X10 Y0 E5',
        'G1 Z0.4',
        ';LAYER_CHANGE',
        ';Z:0.4',
        'G1 X10 Y10 E6',
      ].join('\n'),
    );
    expect(parsed.layers.map((l) => l.z)).toEqual([0.2, 0.4]);
    expect(parsed.purgeSegments).toBe(1);
  });

  it('decides absorption at the extrusion, not at the marker', () => {
    // A marker fires while Z is still the old layer's. Judging "is this the same Z"
    // when the marker arrives rather than when the next extrusion does puts the new
    // layer's first extrusion into the previous layer, and loses a layer from the
    // count of every file.
    const parsed = parse(
      [
        'G90',
        'M82',
        'G1 X0 Y0 Z0.2',
        'G1 X10 Y0 E5',
        ';LAYER_CHANGE', // still at Z 0.2 here
        ';Z:0.4',
        'G1 Z0.4', // the climb happens after the marker
        'G1 X10 Y10 E6',
      ].join('\n'),
    );
    expect(parsed.layers.map((l) => l.z)).toEqual([0.2, 0.4]);
  });

  it('coalesces several markers with nothing extruded between them', () => {
    // No fixture writes two markers in a row today, but a profile with custom
    // layer-change G-code could, and a counter rather than a flag would open an empty
    // layer for each.
    const parsed = parse(
      [
        PRIMED,
        ';LAYER_CHANGE',
        ';CHANGE_LAYER',
        ';AFTER_LAYER_CHANGE',
        'G1 Z0.4',
        'G1 X2 Y0 E2',
      ].join('\n'),
    );
    expect(parsed.layers).toHaveLength(2);
  });

  it('splits on a Z change while no marker has been seen', () => {
    const parsed = parse(
      ['G90', 'M82', 'G1 X0 Y0 Z0.2', 'G1 X1 E1', 'G1 Z0.4', 'G1 X2 E2'].join('\n'),
    );
    expect(parsed.layers.map((l) => l.z)).toEqual([0.2, 0.4]);
  });

  it('stops splitting on Z once any marker has been seen', () => {
    // Markers are the slicer's own opinion about where a layer begins. A Z change
    // after that is a Z hop, an ironing pass or a vase-mode ramp, and splitting on it
    // would invent layers the slicer never declared.
    const parsed = parse(
      [
        'G90',
        'M82',
        'G1 X0 Y0 Z0.2',
        ';LAYER_CHANGE',
        'G1 X1 E1',
        'G1 Z0.6 X2 E2', // a ramp inside the layer
        'G1 Z0.9 X3 E3',
      ].join('\n'),
    );
    expect(parsed.layers).toHaveLength(1);
  });

  it('labels a layer with its announced Z rather than the machine Z', () => {
    // The belt printer in this repo's fixtures prints at a machine Z near -988 while
    // announcing `;Z:0.2`. The announced value is the one a reader can act on.
    const parsed = parse(
      [';LAYER_CHANGE', ';Z:0.2', 'G90', 'M82', 'G1 X0 Y0 Z-988.5', 'G1 X1 E1'].join('\n'),
    );
    expect(parsed.layers[0].z).toBe(0.2);
  });

  it.each([
    { name: 'Cura layer marker', comment: ';LAYER:0', layers: 2 },
    { name: 'lower case', comment: ';layer_change', layers: 2 },
    { name: 'a leading space', comment: '; CHANGE_LAYER', layers: 2 },
    // Cura writes this once, near its real markers.
    { name: 'Cura layer count', comment: ';LAYER_COUNT:8', layers: 1 },
    // OrcaSlicer 2.3 writes this twenty times in this repo's fixture.
    {
      name: 'a fan comment containing LAYER',
      comment: ';_SET_FAN_SPEED_CHANGING_LAYER',
      layers: 1,
    },
    // Every file that writes BEFORE also writes AFTER, and treating both as markers
    // splits a multi-material boundary twice, because a tool change extrudes into the
    // wipe tower between them.
    { name: 'BEFORE_LAYER_CHANGE alone', comment: ';BEFORE_LAYER_CHANGE', layers: 1 },
    { name: 'a bare LAYER: with no number', comment: ';LAYER:', layers: 1 },
  ])('$name gives $layers layer(s)', ({ comment, layers }) => {
    const parsed = parse([ANCHORED, comment, 'G1 X3 Y0 E3'].join('\n'));
    expect(parsed.layers).toHaveLength(layers);
  });

  it('closes the last layer at the final segment', () => {
    // The scrubber draws `[0, layers[n].extrusionEnd)`, so a last layer left open at 0
    // would draw the whole print as empty at the top of the slider.
    const parsed = parse([PRIMED, ';LAYER_CHANGE', 'G1 Z0.4', 'G1 X2 E2'].join('\n'));
    expect(parsed.layers.at(-1)?.extrusionEnd).toBe(parsed.extrusionSegments);
    expect(parsed.layers.at(-1)?.travelEnd).toBe(parsed.travelSegments);
  });

  it('gives the layer-change travel to the layer it moves to', () => {
    // A layer does not close until the next extrusion, because that is the first point
    // the new Z is known - so the lift and reposition after the marker are emitted while
    // the layer below is still open. Taking the travel count at close time therefore put
    // them on the wrong layer, and with travel shown, layer one drew a line hanging at
    // layer two's Z. Only an intermediate layer can catch this: the last layer holds
    // every travel either way, which is all the test above asserts.
    const parsed = parse(
      [
        PRIMED,
        ';LAYER_CHANGE',
        'G1 Z0.4', // travel 1 - belongs to layer two
        'G1 X9 Y9', // travel 2 - belongs to layer two
        'G1 X10 Y9 E2',
        ';LAYER_CHANGE',
        'G1 Z0.6',
        'G1 X2 E3',
      ].join('\n'),
    );
    expect(parsed.layers).toHaveLength(3);
    expect(parsed.layers[0].travelEnd).toBe(0);
    expect(parsed.layers[1].travelEnd).toBe(2);
    expect(parsed.layers.at(-1)?.travelEnd).toBe(parsed.travelSegments);
  });

  it('leaves the end block with the last layer that printed', () => {
    // `;LAYER_CHANGE` then a park with no extrusion after it opens no new layer, so there
    // is nothing to hand the trailing travel to. Dropping it instead would leave segments
    // no layer can reach, and the top of the slider would stop showing the wipe.
    const parsed = parse([PRIMED, ';LAYER_CHANGE', 'G1 X0 Y200'].join('\n'));
    expect(parsed.layers).toHaveLength(1);
    expect(parsed.layers[0].travelEnd).toBe(parsed.travelSegments);
    expect(parsed.travelSegments).toBe(1);
  });
});

describe('positioning', () => {
  it('follows G91 relative moves and returns to absolute on G90', () => {
    const parsed = parse(
      [
        'G90',
        'M83',
        'G1 X10 Y10 Z0.2',
        'G91',
        'G1 X5 E1',
        'G1 X5 E1',
        'G90',
        'M83',
        'G1 X30 E1',
      ].join('\n'),
    );
    expect(rounded(parsed.extrusion)).toEqual([
      [10, 10, 0.2, 15, 10, 0.2],
      [15, 10, 0.2, 20, 10, 0.2],
      [20, 10, 0.2, 30, 10, 0.2],
    ]);
  });

  it('makes E relative under G91 even with no M83', () => {
    // Marlin's G91 sets every axis bit including E, so Cura's end block retracts and
    // unretracts with bare E deltas and no M83. Reading those as absolute against an
    // E that has climbed all print turns the pair into one huge deposit.
    const parsed = parse(
      ['G90', 'M82', 'G1 X10 Y10 Z0.2', 'G92 E0', 'G1 X20 E5', 'G91', 'G1 X5 E1'].join('\n'),
    );
    // E1 relative is a real 1 mm deposit. Read as absolute it would be 1 - 5, a
    // retraction, and the segment would vanish into travel.
    expect(rounded(parsed.extrusion)).toEqual([
      [10, 10, 0.2, 20, 10, 0.2],
      [20, 10, 0.2, 25, 10, 0.2],
    ]);
  });

  it('lets G90 clear an M83 set earlier', () => {
    // The same bit-setting cuts the other way: G90 resets E to absolute, which is why
    // slicers re-emit M83 after one. Keeping M83 across the G90 would read the bare E1
    // as another relative millimetre instead of a retraction back to 1.
    const parsed = parse(
      ['G90', 'M83', 'G1 X10 Y10 Z0.2', 'G1 X20 E5', 'G90', 'G1 X30 E1'].join('\n'),
    );
    expect(rounded(parsed.extrusion)).toEqual([[10, 10, 0.2, 20, 10, 0.2]]);
  });

  it('does not take a bare G92 E0 as a statement about XY', () => {
    // Cura and OrcaSlicer both reset the extruder before the first move. Counting that
    // as "the nozzle is somewhere known" made the first real move a travel segment from
    // the origin - a diagonal line across the plate that is not in the file.
    const parsed = parse(['G90', 'M83', 'G92 E0', 'G1 X50 Y50 Z0.2', ';LAYER:0', 'G1 X60 E1'].join('\n'));
    expect(rounded(parsed.travel)).toEqual([]);
  });

  it('measures a file that never emits a layer marker', () => {
    // The parser falls back to splitting on Z, and that half worked: layers came out
    // right while bounds stayed empty, so the panel reported 0 x 0 x 0 mm for a print
    // it was drawing correctly on screen.
    const parsed = parse(
      ['G21', 'G90', 'M82', 'G1 X10 Y10 Z0.2 F1200', 'G1 X40 Y10 E5', 'G1 Z0.4', 'G1 X40 Y40 E10'].join('\n'),
    );
    expect(parsed.layers.length).toBe(2);
    expect(parsed.bounds).toEqual({ min: [10, 10, 0.2], max: [40, 40, 0.4] });
  });

  it('treats G92 as renaming the current position, not as a move', () => {
    // `G92 X10` says "call where you are X10". Assigning the value to the position
    // instead - the obvious-looking version - teleports the nozzle and translates
    // every following coordinate.
    const parsed = parse(['G90', 'M83', 'G1 X50 Y0 Z0.2', 'G92 X10', 'G1 X20 E1'].join('\n'));
    // X20 declared is X60 physical, because the offset is 50 - 10.
    expect(rounded(parsed.extrusion)).toEqual([[50, 0, 0.2, 60, 0, 0.2]]);
  });

  it('does not apply the G92 offset to a relative move', () => {
    // The delta is already a displacement. Adding the offset to it translates the
    // model once per move, so a print with a bed-levelling `G92 Z0` in its start
    // G-code would shear upward.
    const parsed = parse(
      ['G90', 'M83', 'G1 X0 Y0 Z5', 'G92 Z0', 'G91', 'G1 Z1 X1 E1', 'G1 Z1 X1 E1'].join('\n'),
    );
    expect(rounded(parsed.extrusion)).toEqual([
      [0, 0, 5, 1, 0, 6],
      [1, 0, 6, 2, 0, 7],
    ]);
  });

  it('rebases the extruder on G92 E without emitting a move', () => {
    // Absolute-E files reset E at every layer. Without this the next move looks like a
    // huge retraction, is classified as travel, and the whole print renders as nothing.
    const parsed = parse(
      ['G90', 'M82', 'G1 X0 Y0 Z0.2', 'G1 X1 E5', 'G92 E0', 'G1 X2 E1'].join('\n'),
    );
    expect(parsed.extrusionSegments).toBe(2);
    expect(parsed.travelSegments).toBe(0);
  });

  it('keeps an extruder position per tool', () => {
    // Firmware has an E register per tool, so returning to a tool resumes its E where
    // it left off. Zeroing the baseline on every `T` reads the first move back as a
    // retraction and draws that road as travel.
    const parsed = parse(
      [
        'G90',
        'M82',
        'G1 X0 Y0 Z0.2',
        'T0',
        'G1 X1 E5',
        'T1',
        'G1 X2 E1', // tool 1 starts at E0, so this extrudes
        'T0',
        'G1 X3 E6', // tool 0 resumes from E5, so this extrudes too
      ].join('\n'),
    );
    expect(parsed.extrusionSegments).toBe(3);
    expect(parsed.travelSegments).toBe(0);
  });

  it('follows M83 relative extrusion and returns to absolute on M82', () => {
    const parsed = parse(
      ['G90', 'M83', 'G1 X0 Y0 Z0.2', 'G1 X1 E1', 'G1 X2 E1', 'M82', 'G1 X3 E1'].join('\n'),
    );
    // Under M82 the extruder is at 2 from the two relative moves, so `E1` is a
    // retraction and that move is travel.
    expect(parsed.extrusionSegments).toBe(2);
    expect(parsed.travelSegments).toBe(1);
  });
});

describe('extrusion and travel', () => {
  // Every case runs after one real extrusion, because a file containing only travel is
  // refused outright - so the counts below are that first extrusion plus the line under
  // test.
  const base = ['G90', 'M83', 'G1 X0 Y0 Z0.2', 'G1 X1 E1'].join('\n');

  it.each([
    { name: 'extruding move', line: 'G1 X5 E1', extrusion: 2, travel: 0 },
    { name: 'move with no E', line: 'G1 X5', extrusion: 1, travel: 1 },
    { name: 'move while retracting', line: 'G1 X5 E-1', extrusion: 1, travel: 1 },
    { name: 'move with zero E', line: 'G1 X5 E0', extrusion: 1, travel: 1 },
    // A bare retract or prime does not move the nozzle, so there is no line to draw.
    { name: 'retract in place', line: 'G1 E-1', extrusion: 1, travel: 0 },
    { name: 'prime in place', line: 'G1 E1', extrusion: 1, travel: 0 },
    { name: 'G0 rapid with E', line: 'G0 X5 E1', extrusion: 2, travel: 0 },
    { name: 'G0 rapid with no E', line: 'G0 X5', extrusion: 1, travel: 1 },
  ])('$name', ({ line, extrusion, travel }) => {
    const parsed = parse([base, line].join('\n'));
    expect(parsed.extrusionSegments).toBe(extrusion);
    expect(parsed.travelSegments).toBe(travel);
  });

  it('does not draw the first travel move, whose start point is unknown', () => {
    // A file's position is undefined until it homes, and `G28` says nothing this
    // parser can use. Drawing from the assumed origin puts a line across the scene
    // that the file never asked for - a metre long on the belt printer fixture.
    const parsed = parse(['G90', 'M83', 'G1 X50 Y50 Z0.2', 'G1 X51 E1'].join('\n'));
    expect(parsed.travelSegments).toBe(0);
    expect(rounded(parsed.extrusion)).toEqual([[50, 50, 0.2, 51, 50, 0.2]]);
  });

  it('draws travel once the file has declared a position with G92', () => {
    const parsed = parse(
      ['G90', 'M83', 'G92 X0 Y0 Z0.2', 'G1 X50 Y50', 'G1 X51 E1'].join('\n'),
    );
    expect(parsed.travelSegments).toBe(1);
  });
});

describe('arcs', () => {
  // The maths has closed-form ground truth, which is why arcs are implemented rather
  // than approximated: a chord in place of a curve is wrong by an amount nobody can
  // check, while a quarter circle's midpoint is a number.
  it('draws a G2 quarter circle clockwise', () => {
    const parsed = parse(['G90', 'M83', 'G1 X10 Y0 Z0.2', 'G2 X0 Y-10 I-10 J0 E1'].join('\n'));
    const points = rounded(parsed.extrusion, 3);
    expect(points[0].slice(0, 3)).toEqual([10, 0, 0.2]);
    expect(points.at(-1)?.slice(3)).toEqual([0, -10, 0.2]);
    // Clockwise from (10, 0) about the origin passes through the fourth quadrant.
    const midpoint = points[Math.floor(points.length / 2)];
    expect(midpoint[0]).toBeGreaterThan(0);
    expect(midpoint[1]).toBeLessThan(0);
    // Every vertex sits on the circle.
    for (const [x, y] of points) {
      expect(Math.hypot(x, y)).toBeCloseTo(10, 3);
    }
  });

  it('draws a G3 arc the other way round', () => {
    const parsed = parse(['G90', 'M83', 'G1 X10 Y0 Z0.2', 'G3 X0 Y10 I-10 J0 E1'].join('\n'));
    const points = rounded(parsed.extrusion, 3);
    const midpoint = points[Math.floor(points.length / 2)];
    expect(midpoint[0]).toBeGreaterThan(0);
    expect(midpoint[1]).toBeGreaterThan(0);
  });

  it('treats an arc that ends where it starts as a full circle', () => {
    // An endpoint test for "did this move anywhere" gets exactly this case backwards
    // and drops the segment, which is the one arc case worth being sure about.
    const parsed = parse(['G90', 'M83', 'G1 X10 Y0 Z0.2', 'G2 X10 Y0 I-10 J0 E1'].join('\n'));
    const points = rounded(parsed.extrusion, 3);
    expect(points.length).toBeGreaterThan(16);
    for (const [x, y] of points) {
      expect(Math.hypot(x, y)).toBeCloseTo(10, 3);
    }
    expect(points.at(-1)?.slice(3, 5)).toEqual([10, 0]);
  });

  it('interpolates Z along a helical arc', () => {
    const parsed = parse(
      ['G90', 'M83', 'G1 X10 Y0 Z0.2', 'G3 X10 Y0 Z1.2 I-10 J0 E1'].join('\n'),
    );
    const points = segments(parsed.extrusion);
    expect(points[0][2]).toBeCloseTo(0.2, 5);
    expect(points.at(-1)?.[5]).toBeCloseTo(1.2, 5);
    // Monotonic, which a Z assigned to every point at once would not be.
    for (let i = 1; i < points.length; i++) {
      expect(points[i][5]).toBeGreaterThan(points[i - 1][5]);
    }
  });

  it('subdivides more finely for a tighter chord tolerance', () => {
    const source = ['G90', 'M83', 'G1 X10 Y0 Z0.2', 'G2 X10 Y0 I-10 J0 E1'].join('\n');
    const coarse = parse(source, { chordToleranceMm: 1 }).extrusionSegments;
    const fine = parse(source, { chordToleranceMm: 0.01 }).extrusionSegments;
    expect(fine).toBeGreaterThan(coarse * 5);
  });

  it('refuses an arc that would exhaust the segment budget', () => {
    // A tight chord tolerance on a large radius turns one line into millions of
    // segments, so the cap has to bite inside a single line and not only between them.
    expect(() =>
      parse(['G90', 'M83', 'G1 X1000 Y0 Z0.2', 'G2 X1000 Y0 I-1000 J0 E1'].join('\n'), {
        chordToleranceMm: 0.000001,
        segmentCap: 5000,
      }),
    ).toThrow(/more than 5,000 moves/);
  });
});

describe('refusals', () => {
  it.each([
    {
      name: 'inches',
      // Silently rendering a model 25.4 times too small is worse than a sentence.
      source: ['G20', 'G90', 'G1 X1 Y1 Z0.2 E1'].join('\n'),
      message: /inches/,
    },
    {
      name: 'an arc plane other than XY',
      source: ['G90', 'M83', 'G18', 'G1 X10 Y0 Z0.2', 'G2 X0 Y-10 I-10 J0 E1'].join('\n'),
      message: /arc plane/,
    },
    {
      name: 'absolute arc centres',
      source: ['G90', 'M83', 'G90.1', 'G1 X10 Y0 Z0.2', 'G2 X0 Y-10 I0 J0 E1'].join('\n'),
      message: /absolute arc centres/,
    },
    {
      name: 'radius-form arcs',
      source: ['G90', 'M83', 'G1 X10 Y0 Z0.2', 'G2 X0 Y-10 R10 E1'].join('\n'),
      message: /radius-form/,
    },
    {
      name: 'an arc with no centre',
      source: ['G90', 'M83', 'G1 X10 Y0 Z0.2', 'G2 X0 Y-10 E1'].join('\n'),
      message: /no I or J/,
    },
    {
      name: 'an arc whose I and J are both zero',
      source: ['G90', 'M83', 'G1 X10 Y0 Z0.2', 'G2 X0 Y-10 I0 J0 E1'].join('\n'),
      message: /no I or J/,
    },
    {
      name: 'modal motion',
      source: ['G90', 'M83', 'G1 X0 Y0 Z0.2', 'X10 Y10 E1'].join('\n'),
      message: /modal motion/,
    },
    {
      name: 'a coordinate that is not a number',
      // NaN coordinates poison the bounding box and three.js then draws nothing at
      // all, which is the blank canvas this refusal exists to replace.
      source: ['G90', 'M83', 'G1 X0 Y0 Z0.2', 'G1 X- Y10 E1'].join('\n'),
      message: /not a number/,
    },
    {
      name: 'an E value that is not a number',
      source: ['G90', 'M83', 'G1 X0 Y0 Z0.2', 'G1 X10 E.'].join('\n'),
      message: /not a number/,
    },
    {
      name: 'a file with no extrusion at all',
      source: ['G90', 'G1 X0 Y0 Z0.2', 'G1 X10 Y10'].join('\n'),
      message: /no toolpaths/,
    },
  ])('refuses $name', ({ source, message }) => {
    expect(() => parse(source)).toThrow(message);
  });

  it('refuses binary G-code by its magic number', () => {
    // Otherwise a .bgcode scans as a few thousand junk words and reports "no
    // toolpaths", which sends the reader looking for a problem with their model.
    const parser = createToolpathParser();
    expect(() => parser.push(new Uint8Array([0x47, 0x43, 0x44, 0x45, 0, 1, 2, 3]))).toThrow(
      /binary G-code/,
    );
  });

  it.each([1, 2, 3])('refuses binary G-code split across chunks after %i byte(s)', (split) => {
    // A network read has no obligation to hand over four bytes at once, and a body under
    // four bytes long is legal. Checking only the first chunk therefore let a .bgcode
    // through on a short read, and it then failed as "no toolpaths" instead - the exact
    // misleading message the magic check exists to prevent.
    const parser = createToolpathParser();
    const magic = new Uint8Array([0x47, 0x43, 0x44, 0x45, 0, 1, 2, 3]);
    parser.push(magic.subarray(0, split));
    expect(() => parser.push(magic.subarray(split))).toThrow(/binary G-code/);
  });

  it('refuses a coordinate too large to hold in the geometry', () => {
    // Positions go to the GPU as Float32, where anything past ~3.4e38 becomes Infinity;
    // the camera fit then divides into NaN and the panel goes blank with no message,
    // which is the worst way to fail. Written-out digits, not `1e40` - the number scanner
    // stops at the `e`, correctly, because G-code has no exponent notation.
    const huge = `1${'0'.repeat(40)}`;
    expect(() => parse(['G90', 'M83', 'G1 X0 Y0 Z0.2', `G1 X${huge} E1`].join('\n'))).toThrow(
      /coordinate too large/,
    );
  });

  it('refuses a file with no newline in a megabyte', () => {
    // What a binary uploaded with a .gcode extension looks like. Without the cap the
    // whole file accumulates in one string before anything notices.
    const parser = createToolpathParser();
    expect(() => parser.push(new TextEncoder().encode('G1 X1 Y1 E1 '.repeat(100_000)))).toThrow(
      /single line longer than a megabyte/,
    );
  });

  it('refuses a file with more segments than it will draw', () => {
    const source = [
      'G90',
      'M83',
      'G1 X0 Y0 Z0.2',
      ...Array(50).fill('G1 X1 E1\nG1 X0 E1'),
    ].join('\n');
    expect(() => parse(source, { segmentCap: 10 })).toThrow(/more than 10 moves/);
  });

  it('refuses a file with more layers than it will draw', () => {
    // A vase mode print sliced by something that emits no layer markers changes Z on
    // every segment, and would otherwise allocate a layer record per segment.
    const lines = ['G90', 'M83', 'G1 X0 Y0 Z0.2'];
    for (let i = 1; i <= 20; i++) lines.push(`G1 X${i} Z${0.2 + i * 0.01} E1`);
    expect(() => parse(lines.join('\n'), { layerCap: 5 })).toThrow(/more than 5 layers/);
  });

  it('does not mistake a Klipper macro for modal motion', () => {
    // Both of these are in this repo's own OrcaSlicer fixtures, and both contain a
    // letter that is an axis word in G-code: the `I` of PRINT and the `E` of SET.
    // Scanning ahead for a `G` rather than judging the first word alone refuses two of
    // the five slicers this app supports.
    const parsed = parse(
      [
        'PRINT_START EXTRUDER=260 BED=105',
        'SET_VELOCITY_LIMIT ACCEL=300 ACCEL_TO_DECEL=150',
        'EXCLUDE_OBJECT_DEFINE NAME=box CENTER=10,10',
        'G90',
        'M83',
        'G1 X0 Y0 Z0.2',
        'G1 X10 E1',
      ].join('\n'),
    );
    expect(parsed.extrusionSegments).toBe(1);
  });

  it('ignores commands it does not know rather than refusing them', () => {
    // `G28 X` is a real homing line carrying an axis letter with no value, and
    // `M201 X1000` is a real acceleration limit. Refusing either fails a file whose
    // toolpaths are perfectly readable.
    const parsed = parse(
      [
        'G28 X',
        'G28',
        'M201 X1000 Y1000',
        'M104 S210',
        'G90',
        'M83',
        'G1 X0 Y0 Z0.2',
        'G1 X10 E1',
      ].join('\n'),
    );
    expect(parsed.extrusionSegments).toBe(1);
  });
});

describe('text handling', () => {
  it('strips a trailing comment before reading axis words', () => {
    // `G1 X1 ; move to X position` has an X in its comment. Scanning words before
    // cutting the comment reads it as a second X and moves somewhere else.
    const parsed = parse(
      ['G90', 'M83', 'G1 X0 Y0 Z0.2', 'G1 X1 E1 ; move to X position Y99'].join('\n'),
    );
    expect(rounded(parsed.extrusion)).toEqual([[0, 0, 0.2, 1, 0, 0.2]]);
  });

  it('reads words that are not separated by spaces', () => {
    // Post-processors that strip whitespace write `X1.5Y2.5`, and `E` must stay the
    // extruder word rather than becoming an exponent.
    const parsed = parse(['G90', 'M83', 'G1X0Y0Z0.2', 'G1X1E2'].join('\n'));
    expect(rounded(parsed.extrusion)).toEqual([[0, 0, 0.2, 1, 0, 0.2]]);
  });

  it('reads a file with CRLF line endings', () => {
    const parsed = parse(['G90', 'M83', 'G1 X0 Y0 Z0.2', 'G1 X1 E1'].join('\r\n'));
    expect(rounded(parsed.extrusion)).toEqual([[0, 0, 0.2, 1, 0, 0.2]]);
  });

  it('reads the last line of a file with no trailing newline', () => {
    const parsed = parse(['G90', 'M83', 'G1 X0 Y0 Z0.2', 'G1 X1 E1'].join('\n'));
    expect(parsed.extrusionSegments).toBe(1);
  });

  it('gives the same answer whatever the bytes are cut into', () => {
    // The streaming path is the only way this parser is ever used, and a chunk
    // boundary can fall inside a word, inside a number or inside a multi-byte
    // character - the `°` here is two bytes.
    const source = [
      '; a Ω comment with a wide character',
      'G90',
      'M83',
      'G1 X0 Y0 Z0.2',
      ';LAYER_CHANGE',
      ';Z:0.2',
      'G1 X1.25 Y2.5 E1',
      'G1 Z0.4',
      ';LAYER_CHANGE',
      ';Z:0.4',
      'G2 X1.25 Y2.5 I-1 J0 E2',
    ].join('\n');
    const whole = parse(source);
    const bytes = new TextEncoder().encode(source);
    for (const size of [1, 2, 3, 7, 64]) {
      const parser = createToolpathParser();
      for (let i = 0; i < bytes.length; i += size) parser.push(bytes.subarray(i, i + size));
      const streamed = parser.finish();
      expect(rounded(streamed.extrusion)).toEqual(rounded(whole.extrusion));
      expect(streamed.layers).toEqual(whole.layers);
    }
  });
});

describe('chunking', () => {
  // The scrubber sets a draw range per chunk, so where a layer boundary falls relative
  // to a chunk boundary is exactly the off-by-one worth testing.
  const manyLayers = (perLayer: number, layers: number): string => {
    const lines = ['G90', 'M83', 'G1 X0 Y0 Z0.2'];
    for (let layer = 0; layer < layers; layer++) {
      lines.push(';LAYER_CHANGE', `;Z:${(0.2 * (layer + 1)).toFixed(1)}`);
      for (let i = 0; i < perLayer; i++) lines.push(`G1 X${i + 1} E1`);
      lines.push('G1 X0');
    }
    return lines.join('\n');
  };

  it('fills chunks to exactly their capacity', () => {
    const parsed = parse(manyLayers(7, 3), { chunkSegments: 4 });
    expect(parsed.extrusionSegments).toBe(21);
    expect(parsed.extrusion.map((c) => c.length / 6)).toEqual([4, 4, 4, 4, 4, 1]);
  });

  it('reports a layer boundary that falls inside a chunk', () => {
    const parsed = parse(manyLayers(3, 3), { chunkSegments: 4 });
    expect(parsed.layers.map((l) => l.extrusionEnd)).toEqual([3, 6, 9]);
    expect(parsed.extrusion.map((c) => c.length / 6)).toEqual([4, 4, 1]);
  });

  it('reports a layer that spans several chunks', () => {
    const parsed = parse(manyLayers(10, 2), { chunkSegments: 3 });
    expect(parsed.layers.map((l) => l.extrusionEnd)).toEqual([10, 20]);
  });

  it('leaves no unused floats at the end of the last chunk', () => {
    // An untrimmed tail draws as a fan of lines back to the origin, because the unused
    // part of a Float32Array is zeroes.
    const parsed = parse(manyLayers(4, 2), { chunkSegments: 3 });
    const total = parsed.extrusion.reduce((n, c) => n + c.length / 6, 0);
    expect(total).toBe(parsed.extrusionSegments);
  });

  it('keeps a whole final chunk whole', () => {
    const parsed = parse(manyLayers(4, 1), { chunkSegments: 2 });
    expect(parsed.extrusion.map((c) => c.length / 6)).toEqual([2, 2]);
  });
});

describe('skipSegments', () => {
  const chunk = (n: number, base: number) =>
    new Float32Array(Array.from({ length: n * 6 }, (_, i) => base + i));

  it('returns the whole list when there is nothing to skip', () => {
    const chunks = [chunk(2, 0), chunk(2, 100)];
    expect(skipSegments(chunks, 0)).toEqual(chunks);
  });

  it('drops whole chunks and then part of one', () => {
    const chunks = [chunk(2, 0), chunk(3, 100)];
    const kept = skipSegments(chunks, 3);
    expect(kept).toHaveLength(1);
    expect(kept[0].length / 6).toBe(2);
    expect(kept[0][0]).toBe(106);
  });

  it('returns nothing when everything is skipped', () => {
    expect(skipSegments([chunk(2, 0)], 2)).toEqual([]);
  });
});

describe('real slicer files', () => {
  // Trimmed captures rather than hand-written examples, because the failures worth
  // catching are the ones nobody would think to invent - and one of them was found
  // this way: OrcaSlicer's Klipper start G-code contains word-style macro names whose
  // letters are axis words, which a plausible-looking parser refuses outright.
  //
  // Layer counts here are what the slicer itself declared for the trimmed range.
  const files = import.meta.glob('./testdata/*.gcode', {
    query: '?raw',
    import: 'default',
    eager: true,
  }) as Record<string, string>;
  const read = (name: string): Uint8Array =>
    new TextEncoder().encode(files[`./testdata/${name}`]);

  it.each([
    {
      file: 'superslicer.gcode',
      layers: 1,
      z: [0.2],
      extrusion: 611,
      travel: 21,
    },
    {
      // Klipper macros in the start G-code, and BEFORE/AFTER pairs around every
      // LAYER_CHANGE, which have to coalesce into one transition each.
      file: 'orcaslicer_1.5.gcode',
      layers: 4,
      z: [0.2, 0.4, 0.6, 0.8],
      extrusion: 907,
      travel: 298,
    },
    {
      // A belt printer: the toolpaths are at a machine Z near -988 while the file
      // announces 0.2, 0.4 and so on. It also writes `;_SET_FAN_SPEED_CHANGING_LAYER`
      // at every boundary, which a substring match would treat as a marker.
      file: 'orcaslicer_2.3.gcode',
      layers: 5,
      z: [0.2, 0.4, 0.6, 0.8, 1],
      extrusion: 483,
      travel: 30,
    },
    {
      // Cura shares no spelling with the others: `;LAYER:n` markers, no `;Z:`, and a
      // `;LAYER_COUNT:` line sitting right beside them.
      file: 'cura.gcode',
      layers: 4,
      z: [0.27, 0.37, 0.47, 0.57],
      extrusion: 786,
      // 49, not 50: Cura and OrcaSlicer both open with a bare `G92 E0`, and taking that
      // as a statement about XY drew a travel line to the first move from the origin.
      travel: 49,
    },
  ])('$file', ({ file, layers, z, extrusion, travel }) => {
    const parser = createToolpathParser();
    parser.push(read(file));
    const parsed = parser.finish();
    expect(parsed.layers).toHaveLength(layers);
    expect(parsed.layers.map((l) => Math.round(l.z * 100) / 100)).toEqual(z);
    expect(parsed.extrusionSegments).toBe(extrusion);
    expect(parsed.travelSegments).toBe(travel);
    expect(parsed.layers.at(-1)?.extrusionEnd).toBe(extrusion);
  });

  it('reports the belt printer toolpaths at their machine Z, not the announced one', () => {
    // The labels say 0.2; the geometry is where the file really puts it. Rewriting the
    // vertices to match the label would move the print away from its own travel moves.
    const parser = createToolpathParser();
    parser.push(read('orcaslicer_2.3.gcode'));
    const parsed = parser.finish();
    const firstZ = segments(parsed.extrusion)[0][2];
    expect(firstZ).toBeLessThan(-900);
    expect(parsed.layers[0].z).toBe(0.2);
  });
});

describe('layerRange', () => {
  // The scrub slider is this number and nothing else, so this is where the off-by-one
  // lives. A canvas looks plausible whether the range is right or one out.
  const toolpath = parse(
    [
      'G90',
      'M83',
      'G1 X0 Y0 Z0.2',
      ';LAYER_CHANGE',
      'G1 X1 E1',
      'G1 X2 E1',
      ';LAYER_CHANGE',
      'G1 Z0.4',
      'G1 X3 E1',
      ';LAYER_CHANGE',
      'G1 Z0.6',
      'G1 X4 E1',
      'G1 X5 E1',
    ].join('\n'),
  );

  it('has the layers the fixture declares', () => {
    expect(toolpath.layers).toHaveLength(3);
    expect(toolpath.extrusionSegments).toBe(5);
  });

  it.each([
    { index: 0, segments: 2 },
    { index: 1, segments: 3 },
    { index: 2, segments: 5 },
  ])('layer $index draws $segments segments', ({ index, segments }) => {
    expect(layerRange(toolpath, index, 'extrusionEnd')).toBe(segments);
  });

  it('draws the whole print at the top of the slider', () => {
    expect(layerRange(toolpath, toolpath.layers.length - 1, 'extrusionEnd')).toBe(
      toolpath.extrusionSegments,
    );
  });

  it('draws one layer at the bottom of the slider, not none', () => {
    expect(layerRange(toolpath, 0, 'extrusionEnd')).toBeGreaterThan(0);
  });

  it.each([-5, 99, 1.7])('clamps an index of %s rather than throwing', (index) => {
    // The slider is a number input the user can type into.
    expect(() => layerRange(toolpath, index, 'extrusionEnd')).not.toThrow();
  });

  it('clamps to the ends', () => {
    expect(layerRange(toolpath, -5, 'extrusionEnd')).toBe(2);
    expect(layerRange(toolpath, 99, 'extrusionEnd')).toBe(5);
  });

  it('is zero for a toolpath with no layers', () => {
    expect(layerRange({ ...toolpath, layers: [] }, 0, 'extrusionEnd')).toBe(0);
  });
});

describe('spreadRange', () => {
  it.each([
    { name: 'nothing drawn', total: 0, want: [0, 0, 0] },
    { name: 'part of the first chunk', total: 2, want: [2, 0, 0] },
    { name: 'exactly the first chunk', total: 4, want: [4, 0, 0] },
    { name: 'a boundary inside the second', total: 6, want: [4, 2, 0] },
    { name: 'everything', total: 12, want: [4, 4, 4] },
    { name: 'more than there is', total: 99, want: [4, 4, 4] },
  ])('$name', ({ total, want }) => {
    expect(spreadRange([4, 4, 4], total)).toEqual(want);
  });

  it('handles a short final chunk', () => {
    expect(spreadRange([4, 1], 5)).toEqual([4, 1]);
  });
});

describe('unretract', () => {
  // Cura's end block verbatim. Without the XY guard this reads as a deposit at
  // Z 2.97, and a 0.7 mm-tall print reports as 2.7 mm tall - which is what the
  // panel showed against the real file before this was fixed.
  const END = ['G91', 'G0 F15000 X8.0 Z0.5 E-4.5', 'G0 F10000 Z1.5 E4.5', 'G90'].join('\n');

  it('does not print when the nozzle only refills', () => {
    const before = parse(ANCHORED);
    const after = parse([ANCHORED, END].join('\n'));
    expect(after.bounds).toEqual(before.bounds);
  });

  it('counts the refill as travel, because that is where the nozzle went', () => {
    const after = parse([ANCHORED, END].join('\n'));
    // Two moves in the end block: the wipe (X+8, Z+0.5) and the Z-only refill.
    expect(after.travelSegments).toBe(parse(ANCHORED).travelSegments + 2);
  });

  it('still prints a move that crosses the plate while extruding', () => {
    // The guard is XY movement, not "has a Z change": a vase-mode move rises in
    // Z and extrudes on every segment, and dropping those empties the model.
    const spiral = parse([PRIMED, ';LAYER_CHANGE', 'G1 X5 Y5 Z0.4 E9'].join('\n'));
    expect(spiral.extrusionSegments).toBeGreaterThan(0);
    expect(spiral.bounds.max[2]).toBeCloseTo(0.4);
  });

  it('ignores a stationary prime with no movement at all', () => {
    const primed = parse([ANCHORED, 'G1 E99'].join('\n'));
    expect(primed.extrusionSegments).toBe(parse(ANCHORED).extrusionSegments);
    expect(primed.travelSegments).toBe(parse(ANCHORED).travelSegments);
  });
});
