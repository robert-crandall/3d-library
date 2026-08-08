import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const post = vi.fn();
const put = vi.fn();
const del = vi.fn();
const get = vi.fn((..._args: unknown[]) => Promise.resolve({ data: [] }));
vi.mock('$lib/api/client', () => ({
  api: {
    GET: (...args: unknown[]) => get(...args),
    POST: (...args: unknown[]) => post(...args),
    PUT: (...args: unknown[]) => put(...args),
    DELETE: (...args: unknown[]) => del(...args)
  }
}));

import TaxonomySection from './TaxonomySection.svelte';

const rows = [
  { id: 3, name: 'Functional', color: '#3b82f6', modelCount: 12 },
  { id: 4, name: 'Toys', color: '#ec4899', modelCount: 0 }
];

beforeEach(() => {
  post.mockReset();
  put.mockReset();
  del.mockReset();
});

function open(overrides: Record<string, unknown> = {}) {
  return render(TaxonomySection, {
    title: 'Categories',
    singular: 'category',
    hint: 'One per model.',
    path: '/api/categories',
    rows,
    colors: ['#3b82f6', '#ec4899'],
    deleteBody: (row: { modelCount: number }) => `${row.modelCount} models become uncategorized.`,
    ...overrides
  });
}

describe('TaxonomySection', () => {
  it('sends the name and the chosen colour when adding', async () => {
    post.mockResolvedValue({ data: { id: 9 } });
    open();

    await fireEvent.input(screen.getByLabelText('New category name'), {
      target: { value: '  Enclosure  ' }
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Colour #ec4899' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Add' }));

    await waitFor(() => expect(post).toHaveBeenCalled());
    // Trimmed, because a name with a trailing space looks identical to the one
    // already there and would defeat the uniqueness the server is enforcing.
    expect(post.mock.calls[0][1].body).toEqual({ name: 'Enclosure', color: '#ec4899' });
  });

  // The server owns "you already have one of those" - it is the only thing that
  // can see the whole list under a concurrent insert. Restating it here would
  // be a second source of truth that drifts.
  it('shows the server refusal verbatim', async () => {
    post.mockResolvedValue({ error: { errors: [{ message: 'you already have a category called Toys' }] } });
    open();

    await fireEvent.input(screen.getByLabelText('New category name'), {
      target: { value: 'Toys' }
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Add' }));

    expect((await screen.findByRole('alert')).textContent).toContain(
      'you already have a category called Toys'
    );
    // Still in the field: a refused name the user has to retype is a refusal
    // they will get wrong twice.
    expect((screen.getByLabelText('New category name') as HTMLInputElement).value).toBe('Toys');
  });

  // openapi-fetch resolves with `error` for a refusal but rejects when the
  // request never happens at all. Without the finally, that rejection leaves
  // every control in the section disabled until the page is reloaded, and the
  // user is told nothing about why.
  it('recovers when the request never reaches the server', async () => {
    post.mockRejectedValue(new TypeError('Failed to fetch'));
    open();

    const field = screen.getByLabelText('New category name');
    await fireEvent.input(field, { target: { value: 'Spares' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Add' }));

    expect((await screen.findByRole('alert')).textContent).toContain('Could not reach the server');
    await waitFor(() =>
      expect((screen.getByRole('button', { name: 'Add' }) as HTMLButtonElement).disabled).toBe(false)
    );

    // And the retry actually goes out, which is the whole point of not being
    // stuck: one call for the failure, one for the second attempt.
    post.mockResolvedValue({ data: { id: 9 } });
    await fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
  });

  // Rename and delete are separate functions with their own try/finally, so the
  // Add case above says nothing about them. Both leave a dialog or an inline
  // form open, which is where being stuck is most visible.
  it('recovers when a rename never reaches the server', async () => {
    put.mockRejectedValue(new TypeError('Failed to fetch'));
    open();

    await fireEvent.click(screen.getAllByRole('button', { name: 'Rename' })[0]);
    await fireEvent.input(screen.getByLabelText('New name for Functional'), {
      target: { value: 'Practical' }
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect((await screen.findByRole('alert')).textContent).toContain('Could not reach the server');
    put.mockResolvedValue({ data: { id: 3 } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(2));
  });

  it('recovers when a delete never reaches the server', async () => {
    del.mockRejectedValue(new TypeError('Failed to fetch'));
    open();

    await fireEvent.click(screen.getByRole('button', { name: 'Delete Functional' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    // Scoped to the dialog: the section shows the same message behind it, and
    // the dialog is where the user is looking.
    const dialog = screen.getByRole('dialog');
    expect((await within(dialog).findByRole('alert')).textContent).toContain(
      'Could not reach the server'
    );
    del.mockResolvedValue({ data: undefined });
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(del).toHaveBeenCalledTimes(2));
  });

  it('renames in place and sends the id it was given', async () => {
    put.mockResolvedValue({ data: { id: 3 } });
    open();

    await fireEvent.click(screen.getAllByRole('button', { name: 'Rename' })[0]);
    await fireEvent.input(screen.getByLabelText('New name for Functional'), {
      target: { value: 'Practical' }
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(put).toHaveBeenCalled());
    expect(put.mock.calls[0][1].params.path.id).toBe(3);
    expect(put.mock.calls[0][1].body).toEqual({ name: 'Practical', color: '#3b82f6' });
  });

  // The count is what makes the confirmation honest. "Delete Functional?" alone
  // does not say that twelve models are about to lose their shelf, and this is
  // a hard delete with no undo.
  it('says what a delete does to the models before doing it', async () => {
    del.mockResolvedValue({ data: undefined });
    open();

    await fireEvent.click(screen.getByRole('button', { name: 'Delete Functional' }));

    const dialog = screen.getByRole('dialog');
    expect(dialog.textContent).toContain('12 models become uncategorized.');
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(del).toHaveBeenCalled());
    expect(del.mock.calls[0][1].params.path.id).toBe(3);
  });

  it('does not send an empty name', async () => {
    open();

    await fireEvent.input(screen.getByLabelText('New category name'), {
      target: { value: '   ' }
    });
    expect((screen.getByRole('button', { name: 'Add' }) as HTMLButtonElement).disabled).toBe(true);
    expect(post).not.toHaveBeenCalled();
  });

  // Tags and materials use the same component with no palette, so the colour
  // controls have to be absent rather than disabled - a swatch row on a tag
  // form would imply tags have colours.
  it('omits the colour controls when there is no palette', () => {
    open({
      title: 'Tags',
      singular: 'tag',
      path: '/api/tags',
      colors: undefined,
      rows: [{ id: 8, name: 'petg', modelCount: 5 }]
    });

    expect(screen.queryByRole('button', { name: /^Colour/ })).toBeNull();
    expect(screen.getByLabelText('New tag name')).toBeTruthy();
  });
});
