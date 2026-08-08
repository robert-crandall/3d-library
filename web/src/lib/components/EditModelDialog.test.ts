import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import EditModelDialog from './EditModelDialog.svelte';

const model = {
  id: 7,
  name: 'Filament Dry Box',
  fileCount: 1,
  totalSize: 1024,
  createdAt: '2026-03-12T09:00:00Z',
  description: 'Holds four spools.',
  printTips: 'PETG at 245 C.',
  sourceUrl: 'https://www.printables.com/model/48213',
  files: []
};

function open(overrides: Record<string, unknown> = {}) {
  const onsave = vi.fn();
  const oncancel = vi.fn();
  render(EditModelDialog, { model, onsave, oncancel, ...overrides });
  return { onsave, oncancel };
}

describe('EditModelDialog', () => {
  it('opens with the model already in the fields', () => {
    open();

    expect((screen.getByLabelText('Name') as HTMLInputElement).value).toBe('Filament Dry Box');
    expect((screen.getByLabelText('Description') as HTMLTextAreaElement).value).toBe(
      'Holds four spools.'
    );
    expect((screen.getByLabelText('Print tips') as HTMLTextAreaElement).value).toBe(
      'PETG at 245 C.'
    );
    expect((screen.getByLabelText('Source URL') as HTMLInputElement).value).toBe(
      'https://www.printables.com/model/48213'
    );
  });

  it('sends every field, not just the changed one', async () => {
    const { onsave } = open();

    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Dry Box v2' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(onsave).toHaveBeenCalledWith({
      name: 'Dry Box v2',
      description: 'Holds four spools.',
      printTips: 'PETG at 245 C.',
      sourceUrl: 'https://www.printables.com/model/48213'
    });
  });

  // The one rule the server also enforces, checked here so the round trip is
  // not spent learning something the form already knew.
  it('refuses an empty name without asking the server', async () => {
    const { onsave } = open();

    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: '   ' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(onsave).not.toHaveBeenCalled();
    expect(screen.getByRole('alert').textContent).toContain('A model needs a name');
    expect(screen.getByLabelText('Name').getAttribute('aria-invalid')).toBe('true');
  });

  // Shouting at someone who has not finished typing is worse than not shouting
  // at all, so the message waits for a submit.
  it('says nothing about the name until the form is submitted', async () => {
    open();

    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: '' } });
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('shows the server refusal it was handed', () => {
    open({ error: 'source URL must be an http:// or https:// address' });

    expect(screen.getByRole('alert').textContent).toContain('must be an http://');
  });

  it('locks both buttons while the save is in flight', () => {
    open({ busy: true });

    expect((screen.getByRole('button', { name: 'Saving…' }) as HTMLButtonElement).disabled).toBe(
      true
    );
    expect((screen.getByRole('button', { name: 'Cancel' }) as HTMLButtonElement).disabled).toBe(
      true
    );
  });

  // Escape closing a dialog mid-request would leave the page with no idea
  // whether the write landed.
  it('ignores Escape while saving', async () => {
    const { oncancel } = open({ busy: true });

    await fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(oncancel).not.toHaveBeenCalled();
  });

  it('closes on Escape when idle', async () => {
    const { oncancel } = open();

    await fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(oncancel).toHaveBeenCalled();
  });
});
