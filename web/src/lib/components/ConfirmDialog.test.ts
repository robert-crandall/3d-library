import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import ConfirmDialog from './ConfirmDialog.svelte';

function open(overrides: Record<string, unknown> = {}) {
  const onconfirm = vi.fn();
  const oncancel = vi.fn();
  render(ConfirmDialog, {
    title: 'Delete model',
    body: 'Filament Dry Box and its 1 file will be deleted. This cannot be undone.',
    confirm: 'Delete model',
    onconfirm,
    oncancel,
    ...overrides
  });
  return { onconfirm, oncancel };
}

describe('ConfirmDialog', () => {
  // The button says what it does. "OK" turns a misclick into a misunderstanding.
  it('names the action on its own button', () => {
    open();

    expect(screen.getByRole('button', { name: 'Delete model' })).toBeTruthy();
    expect(screen.getByText(/cannot be undone/)).toBeTruthy();
    expect(screen.getByRole('dialog').getAttribute('aria-modal')).toBe('true');
  });

  it('reports each answer once', async () => {
    const { onconfirm, oncancel } = open();

    await fireEvent.click(screen.getByRole('button', { name: 'Delete model' }));
    expect(onconfirm).toHaveBeenCalledTimes(1);
    expect(oncancel).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(oncancel).toHaveBeenCalledTimes(1);
  });

  // Two clicks on a delete button is one delete and one 404, which would then
  // be reported as a failure of something that worked.
  it('locks both buttons while the delete is in flight', () => {
    open({ busy: true });

    expect((screen.getByRole('button', { name: 'Deleting…' }) as HTMLButtonElement).disabled).toBe(
      true
    );
    expect((screen.getByRole('button', { name: 'Cancel' }) as HTMLButtonElement).disabled).toBe(
      true
    );
  });

  it('shows a failure without closing', () => {
    open({ error: 'still in use' });

    expect(screen.getByRole('alert').textContent).toContain('still in use');
    expect(screen.getByRole('button', { name: 'Delete model' })).toBeTruthy();
  });
});
