import { describe, expect, it } from 'vitest';
import { EMPTY, extend, toggle, type Selection } from './selection';

const ordered = [10, 20, 30, 40, 50];

// Sorted, because the selection's order is an implementation detail - it is
// which ids are in it that the bulk endpoints and the count line care about.
const has = (s: Selection) => [...s.ids].sort((a, b) => a - b);

describe('toggle', () => {
  it('adds a tile that is not selected and removes one that is', () => {
    const one = toggle(EMPTY, 30);
    expect(has(one)).toEqual([30]);
    expect(has(toggle(one, 30))).toEqual([]);
  });

  // The anchor moving on a deselect is the part a weaker test would miss: if it
  // only moved on select, ctrl-clicking a tile off and then shift-clicking
  // would draw the range from wherever the user had been before, which is not
  // a tile they just touched.
  it('moves the anchor to the clicked tile even when the click deselected it', () => {
    expect(toggle(EMPTY, 30).anchor).toBe(30);
    expect(toggle(toggle(EMPTY, 30), 30).anchor).toBe(30);
  });
});

describe('extend', () => {
  it('selects the run between the anchor and the clicked tile', () => {
    const anchored = toggle(EMPTY, 20);
    expect(has(extend(anchored, ordered, 40))).toEqual([20, 30, 40]);
  });

  it('works backwards', () => {
    const anchored = toggle(EMPTY, 40);
    expect(has(extend(anchored, ordered, 20))).toEqual([20, 30, 40]);
  });

  // Union, not replace. A weaker extend that assigned the range would drop the
  // 10 picked before the range started, which is exactly the mixed ctrl-then-
  // shift flow the feature is for.
  it('keeps tiles picked outside the range, and does not duplicate ones inside it', () => {
    const mixed = toggle(toggle(EMPTY, 10), 40);
    const out = extend(mixed, ordered, 20);
    expect(has(out)).toEqual([10, 20, 30, 40]);
    expect(out.ids.length).toBe(4);
  });

  it('moves the anchor to the clicked tile, so a second shift-click extends from there', () => {
    const first = extend(toggle(EMPTY, 20), ordered, 30);
    expect(first.anchor).toBe(30);
    expect(has(extend(first, ordered, 50))).toEqual([20, 30, 40, 50]);
  });

  it('behaves as a toggle when nothing is selected yet', () => {
    expect(has(extend(EMPTY, ordered, 30))).toEqual([30]);
  });

  // Reachable: select a tile, then a filter or a page change replaces the grid.
  // The page clears the selection on load, but extend must not depend on that
  // having happened - an anchor off the list has no run to describe.
  it('behaves as a toggle when the anchor is no longer on the page', () => {
    const stale: Selection = { ids: [99], anchor: 99 };
    expect(has(extend(stale, ordered, 30))).toEqual([30, 99]);
    expect(extend(stale, ordered, 30).anchor).toBe(30);
  });
});
