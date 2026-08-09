/**
 * The grid's multi-selection, as pure functions over plain data.
 *
 * Separate from the page because the range rules are the part worth testing on
 * their own, and because a component test that has to synthesise ctrl-clicks to
 * check "extend backwards over a partly selected run" is testing the wrong
 * thing.
 *
 * `ids` is an array rather than a Set: Svelte 5's `$state` is not deeply
 * reactive over a plain Set, and a page holds at most one page of tiles, so
 * `includes` on 24 numbers is not worth a SvelteSet import to avoid.
 */
export type Selection = { ids: number[]; anchor: number | null };

export const EMPTY: Selection = { ids: [], anchor: null };

/**
 * Ctrl-click: add the tile if it is not selected, remove it if it is.
 *
 * The anchor moves to the clicked tile either way, including when the click
 * deselected it. That is what "extends a range from the last selected tile"
 * means when read literally, and it is what makes a ctrl-click followed by a
 * shift-click select the run between the two tiles the user actually touched.
 */
export function toggle(selection: Selection, id: number): Selection {
  const has = selection.ids.includes(id);
  return {
    ids: has ? selection.ids.filter((x) => x !== id) : [...selection.ids, id],
    anchor: id
  };
}

/**
 * Shift-click: select every tile between the anchor and this one, inclusive.
 *
 * `ordered` is the ids as displayed, because a range is a run on screen and the
 * ids themselves are in whatever order the sort produced.
 *
 * The range is unioned into the selection rather than replacing it. Replacing
 * loses the tiles picked before the range started, which is the whole point of
 * mixing ctrl and shift; unioning also makes extending backwards over a partly
 * selected run add the missing ones instead of clobbering the rest.
 *
 * With no usable anchor - nothing selected yet, or an anchor from a page that
 * has since been replaced - there is no range to describe, so this is a toggle.
 */
export function extend(selection: Selection, ordered: number[], id: number): Selection {
  const to = ordered.indexOf(id);
  const from = selection.anchor === null ? -1 : ordered.indexOf(selection.anchor);
  if (from === -1 || to === -1) return toggle(selection, id);

  const ids = [...selection.ids];
  for (let i = Math.min(from, to); i <= Math.max(from, to); i++) {
    if (!ids.includes(ordered[i])) ids.push(ordered[i]);
  }
  return { ids, anchor: id };
}
