// What the mesh parsers produce, and the one readout only a mesh has. The camera and
// bounding-box maths moved to `viewer/framing.ts` when the G-code viewer needed it too.

/**
 * A parsed mesh, ready to hand to the renderer.
 *
 * Positions are non-indexed triangles - 9 floats each, three vertices of three
 * coordinates - already converted to millimetres by the parser. Non-indexed because
 * three's `computeVertexNormals()` then produces flat shading, which is what a printed
 * part should look like, without us tracking normals through the transform stack.
 */
export type ParsedMesh = {
  positions: Float32Array;
  /** Objects the file places: 1 for an STL, the build-item count for a 3MF. */
  objectCount: number;
};

export function formatObjectCount(count: number): string {
  return count === 1 ? '1 object' : `${count} objects`;
}
