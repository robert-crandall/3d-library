import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import CollectionsPanel from './CollectionsPanel.svelte';

const all = [
  { id: 12, name: 'Dry box build', description: '', modelCount: 4 },
  { id: 13, name: 'Gifts 2026', description: '', modelCount: 0 }
];

function show(overrides: Record<string, unknown> = {}) {
  const onadd = vi.fn();
  const onremove = vi.fn();
  render(CollectionsPanel, {
    memberships: [],
    all,
    loaded: true,
    onadd,
    onremove,
    ...overrides
  });
  return { onadd, onremove };
}

describe('CollectionsPanel', () => {
  it('offers only the collections this model is not already in', () => {
    show({ memberships: [{ id: 12, name: 'Dry box build' }] });

    // Offering one it already belongs to is a control whose only possible
    // outcome is nothing happening.
    const options = screen
      .getAllByRole('option')
      .map((option) => option.textContent?.trim());
    expect(options).toEqual(['Choose a collection', 'Gifts 2026']);
  });

  it('adds by id and puts the picker back', async () => {
    const { onadd } = show();
    const select = screen.getByRole('combobox');
    await fireEvent.change(select, { target: { value: '13' } });

    expect(onadd).toHaveBeenCalledWith(13);
    // Back to the prompt: the picker is a verb, and a select left showing
    // "Gifts 2026" reads as if the model is in it.
    expect((select as HTMLSelectElement).value).toBe('');
  });

  it('does not fire on the empty choice', async () => {
    const { onadd } = show();
    await fireEvent.change(screen.getByRole('combobox'), { target: { value: '' } });
    expect(onadd).not.toHaveBeenCalled();
  });

  it('removes the membership it was asked about, not the first one', async () => {
    const { onremove } = show({
      memberships: [
        { id: 12, name: 'Dry box build' },
        { id: 13, name: 'Gifts 2026' }
      ]
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Remove from Gifts 2026' }));

    expect(onremove).toHaveBeenCalledWith({ id: 13, name: 'Gifts 2026' });
  });

  it('links each membership at the view that shows it', () => {
    show({ memberships: [{ id: 12, name: 'Dry box build' }] });
    expect(
      screen.getByRole('link', { name: 'Dry box build' }).getAttribute('href')
    ).toBe('/?collectionId=12');
  });

  it('points at Settings when there are no collections, and offers no picker', () => {
    show({ all: [] });

    // The panel is always rendered, unlike Versions - a model in no collections
    // still needs somewhere to be put into one - so with none made yet it has to
    // say where they are made.
    expect(screen.getByRole('link', { name: 'Settings' })).toBeTruthy();
    expect(screen.queryByRole('combobox')).toBeNull();
  });

  it('offers no picker once the model is in every collection', () => {
    show({
      memberships: [
        { id: 12, name: 'Dry box build' },
        { id: 13, name: 'Gifts 2026' }
      ]
    });
    // An empty picker is not a picker, and it must not fall through to the
    // "no collections yet" copy either: there are two.
    expect(screen.queryByRole('combobox')).toBeNull();
    expect(screen.queryByText(/No collections yet/)).toBeNull();
  });

  it('surfaces a refused write, which no dialog would have shown', () => {
    show({ error: 'collection not found' });
    // Neither add nor remove opens a dialog, so without a place here the
    // refusal is invisible - and the page's dialog error would then turn up in
    // whichever dialog the user opened next.
    expect(screen.getByRole('alert').textContent).toContain('collection not found');
  });

  it('locks both controls while a write is in flight', () => {
    show({ memberships: [{ id: 12, name: 'Dry box build' }], mutating: true });

    // Both, not just the picker: a second click on × during the first write
    // sends a delete for a membership the server is already removing.
    expect((screen.getByRole('combobox') as HTMLSelectElement).disabled).toBe(true);
    expect(
      (screen.getByRole('button', { name: 'Remove from Dry box build' }) as HTMLButtonElement)
        .disabled
    ).toBe(true);
    cleanup();
  });
});

describe('CollectionsPanel before the store answers', () => {
  it('says nothing about having no collections until the store has loaded', () => {
    // The store starts empty and fills in asynchronously, so an empty list is
    // not evidence of anything yet. Claiming "No collections yet" here sends
    // the user to Settings to make one they may already have.
    show({ all: [], loaded: false });

    expect(screen.queryByText(/No collections yet/)).toBeNull();
    expect(screen.queryByRole('combobox')).toBeNull();
  });

  it('says it once the store has answered with nothing', () => {
    show({ all: [], loaded: true });

    expect(screen.getByText(/No collections yet/)).toBeTruthy();
  });
});
