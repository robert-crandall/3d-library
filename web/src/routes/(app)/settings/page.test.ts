import { render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

const get = vi.fn((..._args: unknown[]) => Promise.resolve({ data: [] as unknown }));
vi.mock('$lib/api/client', () => ({
  api: {
    GET: (...args: unknown[]) => get(...args),
    POST: vi.fn(),
    PUT: vi.fn(),
    DELETE: vi.fn()
  }
}));

import Settings from './+page.svelte';
import { library } from '$lib/library.svelte';

describe('settings', () => {
  // Settings is the one screen whose whole job is the taxonomy, so it is the
  // worst place for a failed refresh to be silent: every list on it comes from
  // the store, and a refresh that fails after a save leaves it showing a name
  // the server no longer has. A weaker test that only rendered the three
  // sections would pass with the failure invisible.
  it('shows a failed refresh, with the way to retry it', () => {
    library.reset();
    library.error = 'Could not reach the server.';

    render(Settings);

    expect(screen.getByRole('alert').textContent).toContain('Could not reach the server');
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
    library.reset();
  });

  it('says nothing when the taxonomy loaded', () => {
    library.reset();

    render(Settings);

    expect(screen.queryByRole('alert')).toBeNull();
  });
});
