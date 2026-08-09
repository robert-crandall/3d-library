import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import ModelTile from './ModelTile.svelte';
import type { Model } from '$lib/upload';

const base: Model = {
  id: 42,
  name: 'Filament Dry Box',
  fileCount: 3,
  totalSize: 1024,
  createdAt: '2026-03-12T09:00:00Z'
};

/*
  What a weaker version of this file would miss: asserting only that an <img>
  appears would pass against a tile that built the URL from the first file, or
  from the model id, rather than from the id the server resolved. The URL
  assertion is the whole point - the precedence rule lives in Go, and the tile's
  only job is not to second-guess it.
*/
describe('ModelTile', () => {
  it('points the image at the file the server chose', () => {
    render(ModelTile, { model: { ...base, thumbnailFileId: 907 } });
    const img = document.querySelector('img') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('/api/models/42/files/907/thumbnail');
  });

  it('keeps the placeholder for a model with no thumbnail', () => {
    render(ModelTile, { model: base });
    expect(screen.getByTestId('thumbnail-placeholder')).toBeTruthy();
    expect(document.querySelector('img')).toBeNull();
  });
});

// The design puts the category on the tile, which is the only place the grid
// says what a model is filed under. Without it a filtered grid and an
// unfiltered one look identical.
describe('ModelTile category', () => {
  it('shows the category name and its colour', () => {
    render(ModelTile, {
      model: {
        id: 1,
        name: 'Benchy',
        fileCount: 1,
        totalSize: 1024,
        createdAt: '2026-01-01T00:00:00Z',
        category: { id: 3, name: 'Functional', color: '#3b82f6' }
      }
    });

    expect(screen.getByText('Functional')).toBeTruthy();
    // The colour is data, not a class, so the only way to check it is the
    // attribute it is written into. jsdom normalises the hex to rgb().
    const dot = document.querySelector<HTMLElement>('[style*="background-color"]');
    expect(dot?.style.backgroundColor).toBe('rgb(59, 130, 246)');
  });

  // Most models are uncategorized, and an "Uncategorized" word on every tile is
  // noise that pushes the size off the row.
  it('shows nothing at all when the model has no category', () => {
    render(ModelTile, {
      model: {
        id: 1,
        name: 'Benchy',
        fileCount: 1,
        totalSize: 1024,
        createdAt: '2026-01-01T00:00:00Z'
      }
    });

    expect(screen.queryByText('Uncategorized')).toBeNull();
    expect(document.querySelector('[style*="background-color"]')).toBeNull();
  });
});

/*
  What a weaker version of this section would miss: firing a plain `click` with
  `ctrlKey` and asserting the callback would pass on Linux and silently fail on
  macOS, where Ctrl-click is delivered as `contextmenu` and never reaches a
  click handler at all. Both paths are exercised, and so is the pair arriving
  together, which is what a browser that fires both would do.
*/
describe('ModelTile selection', () => {
  it('selects on ctrl-click', async () => {
    const onselect = vi.fn();
    render(ModelTile, { model: base, onselect });
    const tile = screen.getByRole('link', { name: /Filament Dry Box/ });

    await fireEvent.mouseDown(tile);
    await fireEvent.click(tile, { ctrlKey: true });

    expect(onselect).toHaveBeenCalledWith('toggle');
  });

  // macOS. Ctrl-click is a context menu gesture there, so this is the only
  // event the handler ever sees.
  it('selects on a ctrl-contextmenu', async () => {
    const onselect = vi.fn();
    render(ModelTile, { model: base, onselect });
    const tile = screen.getByRole('link', { name: /Filament Dry Box/ });

    await fireEvent.mouseDown(tile);
    await fireEvent.contextMenu(tile, { ctrlKey: true });

    expect(onselect).toHaveBeenCalledWith('toggle');
  });

  // Once, not twice. A browser that delivers both events to the same gesture
  // would otherwise toggle the tile on and straight back off.
  it('counts one gesture once when both events arrive', async () => {
    const onselect = vi.fn();
    render(ModelTile, { model: base, onselect });
    const tile = screen.getByRole('link', { name: /Filament Dry Box/ });

    await fireEvent.mouseDown(tile);
    await fireEvent.contextMenu(tile, { ctrlKey: true });
    await fireEvent.click(tile, { ctrlKey: true });

    expect(onselect).toHaveBeenCalledTimes(1);
  });

  // Two gestures are two toggles. Without the mousedown reset the flag set by
  // the first would swallow the second.
  it('counts a second gesture', async () => {
    const onselect = vi.fn();
    render(ModelTile, { model: base, onselect });
    const tile = screen.getByRole('link', { name: /Filament Dry Box/ });

    await fireEvent.mouseDown(tile);
    await fireEvent.contextMenu(tile, { ctrlKey: true });
    await fireEvent.mouseDown(tile);
    await fireEvent.contextMenu(tile, { ctrlKey: true });

    expect(onselect).toHaveBeenCalledTimes(2);
  });

  it('extends on shift-click', async () => {
    const onselect = vi.fn();
    render(ModelTile, { model: base, onselect });

    await fireEvent.click(screen.getByRole('link', { name: /Filament Dry Box/ }), {
      shiftKey: true
    });

    expect(onselect).toHaveBeenCalledWith('range');
  });

  // The tile is still a link. Selection is an extra gesture on top, and a plain
  // click has to keep opening the model.
  it('leaves an unmodified click alone', async () => {
    const onselect = vi.fn();
    render(ModelTile, { model: base, onselect });
    const tile = screen.getByRole('link', { name: /Filament Dry Box/ });

    await fireEvent.mouseDown(tile);
    await fireEvent.click(tile);

    expect(onselect).not.toHaveBeenCalled();
    expect(tile.getAttribute('href')).toBe('/models/42');
  });

  // A modified click on an <a> opens a new tab or window. Without the
  // preventDefault the browser navigates away mid-selection.
  it('stops the browser opening a tab for a modified click', async () => {
    render(ModelTile, { model: base, onselect: vi.fn() });
    const tile = screen.getByRole('link', { name: /Filament Dry Box/ });

    const event = new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true });
    tile.dispatchEvent(event);

    expect(event.defaultPrevented).toBe(true);
  });

  // Without a selection to join the tile is a plain link, and a modified click
  // must keep doing what the browser does with one.
  it('stays a plain link with no selection handler', async () => {
    render(ModelTile, { model: base });
    const tile = screen.getByRole('link', { name: /Filament Dry Box/ });

    const event = new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true });
    tile.dispatchEvent(event);

    expect(event.defaultPrevented).toBe(false);
  });

  // The selected state is a colour, and a colour is nothing to a screen reader.
  it('says it is selected in its accessible name', () => {
    render(ModelTile, { model: base, selected: true, onselect: vi.fn() });
    // The accessible name of the tile is its whole text, so this asserts the
    // suffix sits right after the model name, separated - a leading space is
    // trimmed by name computation and would run the two words together.
    expect(screen.getByRole('link', { name: /Filament Dry Box, selected/ })).toBeTruthy();
  });
});
