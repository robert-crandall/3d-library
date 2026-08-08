import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';
import ThemeToggle from './ThemeToggle.svelte';

afterEach(() => {
  document.documentElement.classList.remove('dark');
  localStorage.clear();
});

describe('theme toggle', () => {
  it('flips the class on <html> and remembers the choice', async () => {
    render(ThemeToggle);
    const toggle = screen.getByRole('switch', { name: 'Dark mode' });

    await fireEvent.click(toggle);

    expect(document.documentElement.classList.contains('dark')).toBe(true);
    expect(toggle.getAttribute('aria-checked')).toBe('true');
    // The written value is what `app.html` reads on the next load, which is
    // what makes the choice survive a reload.
    expect(localStorage.getItem('theme')).toBe('dark');

    await fireEvent.click(toggle);

    expect(document.documentElement.classList.contains('dark')).toBe(false);
    expect(toggle.getAttribute('aria-checked')).toBe('false');
    expect(localStorage.getItem('theme')).toBe('light');
  });
});
