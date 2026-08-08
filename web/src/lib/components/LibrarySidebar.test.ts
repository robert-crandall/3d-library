import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { nav } from '$lib/testing/nav.svelte';

// `nav` rather than a plain getter: the search box has to notice a navigation
// that happens after mount - that is what cancels a pending search when you
// open a model - and a getter over an ordinary variable gives $effect nothing
// to track.
vi.mock('$app/state', async () => ({ page: (await import('$lib/testing/nav.svelte')).nav }));
const goto = vi.fn((..._args: unknown[]) => Promise.resolve());
vi.mock('$app/navigation', () => ({ goto: (...args: unknown[]) => goto(...args) }));
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
  beforeEach(() => {
    nav.url = new URL('http://localhost/');
    goto.mockClear();
    vi.useRealTimers();
  });

  it('links each category and tag at the filter that selects it', () => {
    nav.url = new URL('http://localhost/');
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
    nav.url = new URL('http://localhost/?categoryId=3');
    fill();
    render(LibrarySidebar);

    expect(href(/petg/)).toBe('/?categoryId=3&tagId=8');
    expect(href(/Toys/)).toBe('/?categoryId=4');
  });

  // The selected row is also the way out of the filter. A selected category
  // that still linked to itself would leave clearing it to the "Clear filter"
  // link alone, which is not where anyone looks.
  it('links the selected row back to no filter on that axis', () => {
    nav.url = new URL('http://localhost/?categoryId=3&tagId=8');
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
    nav.url = new URL('http://localhost/?categoryId=3');
    fill();
    render(LibrarySidebar);
    expect(href(/Uncategorized/)).toBe('/?uncategorized=true');

    cleanup();
    nav.url = new URL('http://localhost/?uncategorized=true');
    render(LibrarySidebar);
    expect(href(/Toys/)).toBe('/?categoryId=4');
  });

  // A sidebar on another page must not look like a filtered library: without
  // this, opening a model from a filtered grid leaves the category highlighted
  // while the page showing is not that list at all.
  it('selects nothing while away from the library', () => {
    nav.url = new URL('http://localhost/models/7?categoryId=3');
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
    nav.url = new URL('http://localhost/');
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
    nav.url = new URL('http://localhost/');
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

  // Settings renders the same failure over the lists it makes wrong, and the
  // sidebar is on screen beside it. A weaker test would not notice the two
  // announcements and the two identical Try again buttons.
  it('leaves the failure to Settings when Settings is showing', () => {
    nav.url = new URL('http://localhost/settings');
    fill();
    library.error = 'Could not reach the server.';
    render(LibrarySidebar);

    expect(screen.queryByRole('alert')).toBeNull();
    library.error = '';
  });

  // The sequence that makes keeping a stale list safe. Deleting a category
  // succeeds, the refresh behind it fails, and the sidebar is now showing a
  // category the server no longer has. Nothing else on the screen re-reads the
  // taxonomy, so without the retry that wrong list is what the user has until
  // they reload the page. A weaker test that only asserted the alert would
  // pass with a button that does nothing.
  it('recovers a stale list through the retry beside the failure', async () => {
    nav.url = new URL('http://localhost/');
    library.reset();
    get.mockImplementation((path: unknown) =>
      Promise.resolve(
        String(path) === '/api/categories'
          ? { data: [{ id: 3, name: 'Functional', color: '#3b82f6', modelCount: 12 }] }
          : { data: [] }
      )
    );
    await library.refresh();

    // The delete landed; only the read behind it did not.
    get.mockImplementation((..._args: unknown[]) => Promise.reject(new TypeError('Failed to fetch')));
    await library.refresh();
    render(LibrarySidebar);
    expect(screen.getByRole('link', { name: /Functional/ })).toBeTruthy();

    get.mockImplementation((..._args: unknown[]) => Promise.resolve({ data: [] as unknown }));
    await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));

    await waitFor(() => expect(screen.queryByRole('link', { name: /Functional/ })).toBeNull());
    expect(screen.queryByRole('alert')).toBeNull();
    library.reset();
  });

  // Every filter link is also a way to lose a search. The links are built from
  // the whole view rather than from the two parameters they own, so this is the
  // matrix that says so: narrowing by category keeps what you typed and what
  // you sorted by, and only the page - which rarely survives narrowing - goes.
  it('keeps the search and the ordering in every filter link, and drops the page', () => {
    nav.url = new URL('http://localhost/?q=bin&sort=name&page=2&categoryId=3');
    fill();
    render(LibrarySidebar);

    expect(href(/Toys/)).toBe('/?categoryId=4&q=bin&sort=name');
    expect(href(/petg/)).toBe('/?categoryId=3&tagId=8&q=bin&sort=name');
    expect(href(/Uncategorized/)).toBe('/?uncategorized=true&q=bin&sort=name');
    // Clearing the filters is not clearing the search.
    expect(href(/All models/)).toBe('/?q=bin&sort=name');
  });

  // One navigation per pause, not one per keystroke: the alternative is a GET
  // and a history entry for every letter, and Back then walks backwards through
  // a half-typed word instead of leaving the search.
  it('commits a search once, after the typing stops', async () => {
    vi.useFakeTimers();
    nav.url = new URL('http://localhost/?categoryId=3&page=3');
    fill();
    render(LibrarySidebar);

    const box = screen.getByRole('searchbox', { name: 'Search models' });
    for (const value of ['b', 'bi', 'bin']) {
      await fireEvent.input(box, { target: { value } });
    }
    expect(goto).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(300);
    expect(goto).toHaveBeenCalledTimes(1);
    // The filter survives, the page does not, and it replaces rather than pushes.
    expect(goto.mock.calls[0][0]).toBe('/?categoryId=3&q=bin');
    expect(goto.mock.calls[0][1]).toMatchObject({ replaceState: true, keepFocus: true });
  });

  // The sidebar lives in the layout and does not unmount when you open a model,
  // so a search typed a moment before clicking a tile would otherwise land
  // while the model page is on screen and navigate the user straight back to
  // the library. The term never changes in this sequence, which is why the
  // cancellation cannot depend on the term.
  it('abandons a pending search when the page changes under it', async () => {
    vi.useFakeTimers();
    nav.url = new URL('http://localhost/');
    fill();
    render(LibrarySidebar);

    await fireEvent.input(screen.getByRole('searchbox', { name: 'Search models' }), {
      target: { value: 'bin' }
    });
    nav.url = new URL('http://localhost/models/7');
    await vi.advanceTimersByTimeAsync(300);

    expect(goto).not.toHaveBeenCalled();
  });

  // The URL is the truth. Back, "Clear search" and a filter link all rewrite it,
  // and a box still holding the old term would disagree with the grid beside it.
  it('follows the URL when the search changes elsewhere', async () => {
    nav.url = new URL('http://localhost/?q=bin');
    fill();
    render(LibrarySidebar);

    const box = screen.getByRole('searchbox', { name: 'Search models' }) as HTMLInputElement;
    expect(box.value).toBe('bin');

    nav.url = new URL('http://localhost/');
    await waitFor(() => expect(box.value).toBe(''));
  });

  // Typing while a model is open is still a search: it belongs on the library
  // page, not on whatever screen the box happens to be beside.
  it('sends a search typed away from the library to the library', async () => {
    vi.useFakeTimers();
    nav.url = new URL('http://localhost/models/7');
    fill();
    render(LibrarySidebar);

    await fireEvent.input(screen.getByRole('searchbox', { name: 'Search models' }), {
      target: { value: 'bin' }
    });
    await vi.advanceTimersByTimeAsync(300);

    expect(goto).toHaveBeenCalledTimes(1);
    expect(goto.mock.calls[0][0]).toBe('/?q=bin');
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
