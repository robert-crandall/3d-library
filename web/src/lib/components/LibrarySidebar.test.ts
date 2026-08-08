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
vi.mock('$lib/api/client', () => ({ api: { GET: vi.fn(async () => ({ data: [] })) } }));

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

  // A stale list is worse than a missing one: it would be a category tree the
  // user could click, filtering by things that may no longer exist.
  it('says so when the taxonomy could not be read', () => {
    url = new URL('http://localhost/');
    fill();
    library.error = 'Could not reach the server.';
    render(LibrarySidebar);

    expect(screen.getByRole('alert').textContent).toContain('Could not reach the server');
    library.error = '';
  });
});
