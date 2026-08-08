import { BufferGeometry, Group, LineBasicMaterial, LineSegments } from 'three';
import { describe, expect, it } from 'vitest';
import { disposeGeometries } from './viewport';

describe('disposeGeometries', () => {
  it('frees every geometry in the subtree', () => {
    // A traversal that only looked at the root would leave the nested geometry on the
    // GPU, which is where the memory in a layer viewer actually is.
    const root = new Group();
    const inner = new Group();
    const deep = new LineSegments(new BufferGeometry(), new LineBasicMaterial());
    inner.add(deep);
    root.add(inner);
    const freed: string[] = [];
    deep.geometry.dispose = () => freed.push('geometry');

    disposeGeometries(root);

    expect(freed).toEqual(['geometry']);
  });

  it('leaves the material alone', () => {
    // The scene hands one material to every node it builds and disposes it itself. Freeing
    // it here breaks the next draw and then double-frees at teardown, and neither shows up
    // as an error - three just recompiles the program and carries on.
    const shared = new LineBasicMaterial();
    let disposed = 0;
    shared.dispose = () => void disposed++;
    const root = new Group();
    root.add(new LineSegments(new BufferGeometry(), shared));
    root.add(new LineSegments(new BufferGeometry(), shared));

    disposeGeometries(root);

    expect(disposed).toBe(0);
  });
});
