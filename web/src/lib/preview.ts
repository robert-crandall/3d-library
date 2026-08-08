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
 * `file.type` is the server's vocabulary from `internal/library.fileTypes`, derived from
 * the extension at upload time. It is not enough on its own: that table maps `.bgcode` to
 * `"gcode"` alongside `.gcode`, `.gco` and `.g`, so the type cannot tell the two apart.
 * Binary G-code is heatshrink-compressed and `internal/gcode` already declines to read it,
 * so the filename decides. Without this the panel downloads the whole file - up to
 * `MAX_GCODE_BYTES` of it - to learn from the first four bytes what its name already said.
 */
export function previewKind(file: ModelFile): PreviewKind | undefined {
  if (file.type === 'stl' || file.type === '3mf') return 'mesh';
  if (file.type === 'gcode') return isBinaryGcode(file.filename) ? undefined : 'gcode';
  return undefined;
}

function isBinaryGcode(filename: string): boolean {
  return filename.toLowerCase().endsWith('.bgcode');
}

export function hasPreview(files: readonly ModelFile[]): boolean {
  return files.some((file) => previewKind(file) !== undefined);
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
  const viewable = files.filter((file) => previewKind(file) !== undefined);
  return pick(viewable.filter(drawable)) ?? pick(viewable);
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
  const kind = previewKind(file);
  if (kind === 'mesh') return file.size <= MAX_PREVIEW_BYTES;
  if (kind === 'gcode') return file.size <= MAX_GCODE_BYTES;
  return false;
}
