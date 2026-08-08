import { render, waitFor } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { describe, expect, it, vi } from 'vitest';

const get = vi.fn((...args: unknown[]) =>
  Promise.resolve(
    String(args[0]) === '/api/categories'
      ? { data: [{ id: 9, name: 'Mine', color: '#16a34a', modelCount: 1 }] }
      : String(args[0]) === '/api/library/counts'
        ? { data: { models: 1, uncategorized: 0 } }
        : { data: [] }
  )
);
vi.mock('$lib/api/client', () => ({ api: { GET: (...args: unknown[]) => get(...args) } }));
vi.mock('$app/state', async () => ({ page: (await import('$lib/testing/nav.svelte')).nav }));

import Layout from './+layout.svelte';
import { library } from '$lib/library.svelte';

const children = createRawSnippet(() => ({ render: () => '<p>page</p>' }));

describe('signed-in layout', () => {
  // The taxonomy store is module state, so it outlives sign-out: `goto('/login')`
  // is a client-side navigation and nothing reloads the page. Entering the app
  // therefore has to start from nothing, or the next person to sign in on this
  // tab reads the previous one's categories off the sidebar.
  it('forgets the previous session before reading the taxonomy', async () => {
    library.categories = [{ id: 3, name: 'Someone elses', color: '#3b82f6', modelCount: 12 }];
    library.tags = [{ id: 8, name: 'private', modelCount: 5 }];
    library.counts = { models: 41, uncategorized: 9 };

    render(Layout, { children });

    // Empty at once, before anything has been asked for.
    expect(library.categories).toEqual([]);
    expect(library.tags).toEqual([]);
    expect(library.counts).toEqual({ models: 0, uncategorized: 0 });

    // And then filled from the server, so this cannot pass by simply never
    // reading the taxonomy at all.
    await waitFor(() => expect(library.categories.map((c) => c.name)).toEqual(['Mine']));
    expect(library.counts).toEqual({ models: 1, uncategorized: 0 });
    library.reset();
  });
});
