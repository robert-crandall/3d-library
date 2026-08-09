import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import VersionsPanel from './VersionsPanel.svelte';

const root = {
  id: 7,
  name: 'final — desiccant tray fix',
  description: 'Holds four spools.',
  fileCount: 3,
  createdAt: '2026-03-12T09:00:00Z'
};
const v2 = {
  id: 8,
  name: 'v2 — thicker lid gasket',
  description: '',
  fileCount: 2,
  createdAt: '2026-02-28T09:00:00Z'
};
const v1 = {
  id: 9,
  name: 'v1 — original download',
  description: 'From Printables.',
  fileCount: 1,
  createdAt: '2026-02-19T09:00:00Z'
};

function open(overrides: Record<string, unknown> = {}) {
  const ondetach = vi.fn();
  render(VersionsPanel, { family: [root, v2, v1], currentId: 7, ondetach, ...overrides });
  return { ondetach };
}

function rows() {
  return within(screen.getByRole('list')).getAllByRole('listitem');
}

describe('VersionsPanel', () => {
  // The order is the server's, and it is the order design 1c draws: the root
  // first, then its versions newest first. Asserting on the rendered order
  // rather than on presence is what catches a panel that renders the family as
  // a set - three names in the wrong order still "shows all three".
  it('lists the root first, then the versions newest first', () => {
    open();
    expect(rows().map((row) => row.textContent?.trim().split(' —')[0])).toEqual([
      'final',
      'v2',
      'v1'
    ]);
  });

  // AC 1: the one you are looking at is marked as current. It is not a link,
  // because it goes nowhere, and aria-current is what says so to a screen
  // reader.
  it('marks the model you are on and links to the others', () => {
    open({ currentId: 8 });
    expect(screen.getByText('v2 — thicker lid gasket').getAttribute('aria-current')).toBe('page');
    expect(screen.getByRole('link', { name: /final/ }).getAttribute('href')).toBe('/models/7');
    expect(screen.getByRole('link', { name: /v1/ }).getAttribute('href')).toBe('/models/9');
    expect(screen.queryByRole('link', { name: /v2/ })).toBeNull();
  });

  // The note beside the label is half of what the design's rows carry, and it
  // is optional: the fixture's v2 has none.
  it('shows a version note when there is one', () => {
    open();
    expect(screen.getByText(/From Printables/)).toBeTruthy();
    expect(screen.getByText(/Holds four spools/)).toBeTruthy();
  });

  it('shows each version date', () => {
    open();
    expect(screen.getByText('12 Mar 2026')).toBeTruthy();
    expect(screen.getByText('28 Feb 2026')).toBeTruthy();
    expect(screen.getByText('19 Feb 2026')).toBeTruthy();
  });

  // A family of two is the smallest one that renders at all, and it is where an
  // "at least three rows" assumption would break.
  it('renders a family of two', () => {
    open({ family: [root, v1], currentId: 7 });
    expect(rows()).toHaveLength(2);
  });

  // The root is the thing the versions hang off, so there is nothing to detach
  // it from. A Detach button on that row would be a request the server refuses.
  it('offers Detach on every version and not on the root', () => {
    const { ondetach } = open();
    const buttons = screen.getAllByRole('button', { name: 'Detach' });
    expect(buttons).toHaveLength(2);
    fireEvent.click(buttons[0]);
    expect(ondetach).toHaveBeenCalledWith(v2);
  });

  // The page disables every mutation while one is in flight. Without this the
  // panel would be the one place a second write could start.
  it('disables Detach while another write is running', () => {
    open({ mutating: true });
    for (const button of screen.getAllByRole('button', { name: 'Detach' })) {
      expect((button as HTMLButtonElement).disabled).toBe(true);
    }
  });
});
