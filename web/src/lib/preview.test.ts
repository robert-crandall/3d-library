import { describe, expect, it } from 'vitest';
import { MAX_GCODE_BYTES } from '$lib/gcode/load';
import { MAX_PREVIEW_BYTES } from '$lib/mesh/parse';
import { defaultFile, hasPreview, previewKind } from './preview';
import type { ModelFile } from '$lib/upload';

function make(filename: string, type: string, size = 1_000): ModelFile {
  return {
    id: filename.length,
    filename,
    type,
    contentType: 'application/octet-stream',
    size,
    createdAt: '2026-03-12T09:00:00Z',
    hasThumbnail: false,
  };
}

describe('previewKind', () => {
  it.each([
    ['stl', 'mesh'],
    ['3mf', 'mesh'],
    ['gcode', 'gcode'],
  ])('draws %s with the %s viewer', (type, kind) => {
    expect(previewKind(type)).toBe(kind);
  });

  it.each(['bgcode', 'image', 'zip', 'other'])('has no viewer for %s', (type) => {
    // `.bgcode` especially: it is G-code by name and heatshrink-compressed binary in
    // fact, so routing it to the G-code viewer would show an empty plate rather than
    // saying the file cannot be previewed.
    expect(previewKind(type)).toBeUndefined();
  });
});

describe('hasPreview', () => {
  it('is false for a model of nothing but images', () => {
    // This is what keeps three.js - the largest thing in the bundle - unfetched for a
    // model that has nothing to draw.
    expect(hasPreview([make('front.jpg', 'image'), make('notes.txt', 'other')])).toBe(false);
  });

  it('is true as soon as one file can be drawn', () => {
    expect(hasPreview([make('front.jpg', 'image'), make('part.stl', 'stl')])).toBe(true);
  });
});

describe('defaultFile', () => {
  it('opens on the 3MF ahead of the STL and the G-code', () => {
    // Upload order is the tempting default and it is wrong: it opens on whatever the
    // owner happened to drag in first. Listing G-code first here is the case that
    // separates the two.
    const files = [make('plate.gcode', 'gcode'), make('part.stl', 'stl'), make('proj.3mf', '3mf')];
    expect(defaultFile(files)?.filename).toBe('proj.3mf');
  });

  it('opens on the STL when there is no 3MF', () => {
    const files = [make('plate.gcode', 'gcode'), make('part.stl', 'stl')];
    expect(defaultFile(files)?.filename).toBe('part.stl');
  });

  it('falls back to G-code when no mesh is present', () => {
    expect(defaultFile([make('note.txt', 'other'), make('plate.gcode', 'gcode')])?.filename).toBe(
      'plate.gcode',
    );
  });

  it('skips a mesh too large to draw in favour of one that is not', () => {
    // A 3MF holding every plate can pass the cap where a single-part STL export does
    // not, and the other way round. Opening on the refused one shows "too large" to
    // someone whose model previews fine two buttons along.
    const files = [
      make('everything.3mf', '3mf', MAX_PREVIEW_BYTES + 1),
      make('part.stl', 'stl', 10_000),
    ];
    expect(defaultFile(files)?.filename).toBe('part.stl');
  });

  it('skips G-code too large to draw', () => {
    // The G-code cap is far larger than the mesh one, so an ordering that only ever
    // consulted `MAX_PREVIEW_BYTES` would pass every test above and still open on a
    // 300 MB file it refuses.
    const files = [make('huge.gcode', 'gcode', MAX_GCODE_BYTES + 1), make('small.gcode', 'gcode')];
    expect(defaultFile(files)?.filename).toBe('small.gcode');
  });

  it('still opens on a file too large to draw when nothing else can be drawn', () => {
    // Better to say why this file will not preview than to show an empty panel with a
    // strip the user has to guess at.
    const only = make('everything.3mf', '3mf', MAX_PREVIEW_BYTES + 1);
    expect(defaultFile([only, make('front.jpg', 'image')])?.filename).toBe('everything.3mf');
  });

  it('is undefined when nothing in the model can be drawn', () => {
    expect(defaultFile([make('front.jpg', 'image')])).toBeUndefined();
  });
});
