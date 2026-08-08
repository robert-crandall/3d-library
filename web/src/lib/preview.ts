import { MAX_GCODE_BYTES } from '$lib/gcode/load';
import { MAX_PREVIEW_BYTES } from '$lib/mesh/parse';
import type { ModelFile } from '$lib/upload';

/*
  Which file the preview panel opens on, and what kind of viewer draws it.

  Pure and separate from the panel because picking the default is the part with real
  rules in it - a model can hold a 3MF project, an STL export, the G-code that came out
  of the slicer and a photo, and opening on the wrong one of those is a bug you only
  notice by reading the strip.
*/

export type PreviewKind = 'mesh' | 'gcode';

/**
 * What can draw this file, if anything.
 *
 * `type` is the server's vocabulary from `internal/library.fileTypes`, derived from the
 * extension at upload time. `.bgcode` is deliberately absent: it is heatshrink-compressed
 * binary, `internal/gcode` already declines to read it, and the honest panel for it says
 * so rather than showing an empty plate.
 */
export function previewKind(type: string): PreviewKind | undefined {
  if (type === 'stl' || type === '3mf') return 'mesh';
  if (type === 'gcode') return 'gcode';
  return undefined;
}

export function hasPreview(files: readonly ModelFile[]): boolean {
  return files.some((file) => previewKind(file.type) !== undefined);
}

/**
 * The file to open on when the user has not picked one.
 *
 * The order is 3MF, then STL, then G-code. A 3MF carries its own unit and its object
 * structure where an STL is a bag of triangles everyone agrees to read as millimetres,
 * so it is the better of the two to open on. Both come before G-code because the mesh is
 * the thing that was modelled and the G-code is one slicing of it for one printer -
 * opening on the toolpaths of somebody's draft profile misrepresents the model.
 *
 * Size comes first, though. Opening on a file we already know we will refuse, while one
 * we could draw sits next to it in the strip, shows "too large" to someone whose model
 * previews fine. A 3MF project carrying every plate can pass the mesh cap where the STL
 * export of one part does not, which is the pair that reaches this.
 *
 * Without any of this the default is whichever file the server lists first, which is
 * upload order wearing a disguise.
 */
export function defaultFile(files: readonly ModelFile[]): ModelFile | undefined {
  return pick(files.filter(drawable)) ?? pick(files);
}

function pick(files: readonly ModelFile[]): ModelFile | undefined {
  return (
    files.find((file) => file.type === '3mf') ??
    files.find((file) => file.type === 'stl') ??
    files.find((file) => file.type === 'gcode')
  );
}

/** Whether the viewer for this file would draw it rather than refuse it on size. */
function drawable(file: ModelFile): boolean {
  const kind = previewKind(file.type);
  if (kind === 'mesh') return file.size <= MAX_PREVIEW_BYTES;
  if (kind === 'gcode') return file.size <= MAX_GCODE_BYTES;
  return false;
}
