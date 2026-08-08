import { render } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { describe, expect, it, vi } from 'vitest';

const get = vi.fn((..._args: unknown[]) => Promise.resolve({ data: [] as unknown }));
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
  it('forgets the previous session before reading the taxonomy', () => {
    library.categories = [{ id: 3, name: 'Someone elses', color: '#3b82f6', modelCount: 12 }];
    library.tags = [{ id: 8, name: 'private', modelCount: 5 }];
    library.counts = { models: 41, uncategorized: 9 };

    render(Layout, { children });

    expect(library.categories).toEqual([]);
    expect(library.tags).toEqual([]);
    expect(library.counts).toEqual({ models: 0, uncategorized: 0 });
  });
});
