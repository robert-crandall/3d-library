import { cleanup, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

// A URL, not a store: the sidebar reads page.url.searchParams and nothing else,
// so a plain object with a real URL on it is the whole of what it needs.
let url = new URL('http://localhost/');
vi.mock('$app/state', () => ({
  page: {
    get url() {
      return url;
    }
  }
}));
const get = vi.fn((..._args: unknown[]) => Promise.resolve({ data: [] as unknown }));
vi.mock('$lib/api/client', () => ({ api: { GET: (...args: unknown[]) => get(...args) } }));

import LibrarySidebar from './LibrarySidebar.svelte';
import { library } from '$lib/library.svelte';

function fill() {
  library.categories = [
    { id: 3, name: 'Functional', color: '#3b82f6', modelCount: 12 },
    { id: 4, name: 'Toys', color: '#ec4899', modelCount: 2 }
  ];
  library.tags = [
    { id: 8, name: 'petg', modelCount: 5 },
    { id: 9, name: 'quick-print', modelCount: 1 }
  ];
  library.counts = { models: 41, uncategorized: 9 };
  library.error = '';
}

function href(name: string | RegExp) {
  return screen.getByRole('link', { name }).getAttribute('href');
}

describe('LibrarySidebar', () => {
  it('links each category and tag at the filter that selects it', () => {
    url = new URL('http://localhost/');
    fill();
    render(LibrarySidebar);

    expect(href(/Functional/)).toBe('/?categoryId=3');
    expect(href(/petg/)).toBe('/?tagId=8');
    expect(href(/Uncategorized/)).toBe('/?uncategorized=true');
    // The counts are the point of the row: a category list without them is a
    // list of words, and these come from the server rather than from counting
    // whatever the grid happens to be showing.
    expect(screen.getByRole('link', { name: /Functional/ }).textContent).toContain('12');
    expect(screen.getByRole('link', { name: /All models/ }).textContent).toContain('41');
    expect(screen.getByRole('link', { name: /Uncategorized/ }).textContent).toContain('9');
  });

  // Two axes, one URL. Without this, picking a tag while a category is selected
  // would silently drop the category and show a wider library than the sidebar
  // claims - the two selected rows and the grid would disagree.
  it('keeps the other axis when a second filter is added', () => {
    url = new URL('http://localhost/?categoryId=3');
    fill();
    render(LibrarySidebar);

    expect(href(/petg/)).toBe('/?categoryId=3&tagId=8');
    expect(href(/Toys/)).toBe('/?categoryId=4');
  });

  // The selected row is also the way out of the filter. A selected category
  // that still linked to itself would leave clearing it to the "Clear filter"
  // link alone, which is not where anyone looks.
  it('links the selected row back to no filter on that axis', () => {
    url = new URL('http://localhost/?categoryId=3&tagId=8');
    fill();
    render(LibrarySidebar);

    expect(href(/Functional/)).toBe('/?tagId=8');
    expect(href(/petg/)).toBe('/?categoryId=3');
    expect(screen.getByRole('link', { name: /Functional/ }).getAttribute('aria-current')).toBe(
      'page'
    );
  });

  // Uncategorized and a category together match nothing, so offering the
  // combination would be a filter that is always empty.
  it('drops the category when Uncategorized is picked, and the reverse', () => {
    url = new URL('http://localhost/?categoryId=3');
    fill();
    render(LibrarySidebar);
    expect(href(/Uncategorized/)).toBe('/?uncategorized=true');

    cleanup();
    url = new URL('http://localhost/?uncategorized=true');
    render(LibrarySidebar);
    expect(href(/Toys/)).toBe('/?categoryId=4');
  });

  // A sidebar on another page must not look like a filtered library: without
  // this, opening a model from a filtered grid leaves the category highlighted
  // while the page showing is not that list at all.
  it('selects nothing while away from the library', () => {
    url = new URL('http://localhost/models/7?categoryId=3');
    fill();
    render(LibrarySidebar);

    expect(screen.getByRole('link', { name: /Functional/ }).getAttribute('aria-current')).toBeNull();
    expect(screen.getByRole('link', { name: /All models/ }).getAttribute('aria-current')).toBeNull();
    expect(href(/Functional/)).toBe('/?categoryId=3');
  });

  // Driving the failure through refresh() rather than assigning `error` is the
  // point: the question is what a failed read leaves on screen, not what the
  // flag renders. Nothing has been read yet here, so there is nothing to keep,
  // and a sidebar listing categories it never loaded would be an invention.
  it('shows nothing but the failure when the first read fails', async () => {
    url = new URL('http://localhost/');
    library.reset();
    get.mockRejectedValueOnce(new TypeError('Failed to fetch'));
    await library.refresh();
    render(LibrarySidebar);

    expect(screen.getByRole('alert').textContent).toContain('Could not reach the server');
    expect(screen.queryByRole('link', { name: /Functional/ })).toBeNull();
    library.reset();
  });

  // The other half. Once a read has succeeded the lists were right an action
  // ago, and there is no retry on the sidebar: emptying them because one later
  // refresh blipped would take a working category tree away for the rest of the
  // session. The alert is on screen either way, so nothing is being hidden.
  it('keeps the lists it did read when a later refresh fails', async () => {
    url = new URL('http://localhost/');
    library.reset();
    get.mockImplementation((path: unknown) =>
      Promise.resolve(
        String(path) === '/api/categories'
          ? { data: [{ id: 3, name: 'Functional', color: '#3b82f6', modelCount: 12 }] }
          : { data: [] }
      )
    );
    await library.refresh();

    get.mockRejectedValueOnce(new TypeError('Failed to fetch'));
    await library.refresh();
    render(LibrarySidebar);

    expect(screen.getByRole('alert').textContent).toContain('Could not reach the server');
    expect(screen.getByRole('link', { name: /Functional/ })).toBeTruthy();
    get.mockImplementation((..._args: unknown[]) => Promise.resolve({ data: [] as unknown }));
    library.reset();
  });

  // Two deletes in a row are two refreshes, and the first can answer last.
  // Without a guard the sidebar would settle on the list from before the second
  // delete, showing a row the user just removed.
  it('keeps the newest refresh when an older one answers late', async () => {
    type Answer = (value: { data: unknown }) => void;
    const answers: Answer[] = [];
    get.mockImplementation(
      () => new Promise<{ data: unknown }>((resolve) => answers.push(resolve))
    );

    const stale = library.refresh();
    const fresh = library.refresh();
    // Four endpoints per refresh, so the first four resolvers are the stale one.
    answers.slice(4).forEach((resolve) => resolve({ data: [{ id: 4, name: 'Toys', color: '#ec4899', modelCount: 2 }] }));
    await fresh;
    answers.slice(0, 4).forEach((resolve) => resolve({ data: [{ id: 3, name: 'Functional', color: '#3b82f6', modelCount: 12 }] }));
    await stale;

    expect(library.categories.map((c) => c.name)).toEqual(['Toys']);
    get.mockImplementation((..._args: unknown[]) => Promise.resolve({ data: [] as unknown }));
    library.reset();
  });

  // The same, for the refresh that fails rather than answers. A stale rejection
  // must not put an error over a list that loaded fine afterwards.
  it('ignores an older refresh that fails after a newer one succeeded', async () => {
    library.reset();
    const failures: ((reason: unknown) => void)[] = [];
    get.mockImplementation(
      () => new Promise<{ data: unknown }>((_resolve, reject) => failures.push(reject))
    );

    const stale = library.refresh();
    get.mockImplementation((path: unknown) =>
      Promise.resolve(
        String(path) === '/api/categories'
          ? { data: [{ id: 4, name: 'Toys', color: '#ec4899', modelCount: 2 }] }
          : { data: [] }
      )
    );
    await library.refresh();
    failures.forEach((reject) => reject(new TypeError('Failed to fetch')));
    await stale;

    expect(library.error).toBe('');
    expect(library.categories.map((c) => c.name)).toEqual(['Toys']);
    get.mockImplementation((..._args: unknown[]) => Promise.resolve({ data: [] as unknown }));
    library.reset();
  });
});
