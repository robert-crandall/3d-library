import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import LoginPage from './+page.svelte';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$lib/api/client', () => ({ api: { POST: vi.fn(), GET: vi.fn() } }));
vi.mock('$lib/auth.svelte', () => ({
  auth: {
    user: null,
    ensure: vi.fn(),
    signedIn: vi.fn(),
    signOut: vi.fn()
  }
}));

const data = {
  registrationOpen: true,
  googleLoginEnabled: false,
  oauthError: ''
};

describe('login page', () => {
  it('starts in registration mode and switches to login', async () => {
    render(LoginPage, { props: { data } });
    const modeGroup = screen.getByRole('group', { name: 'Log in or register' });

    expect(within(modeGroup).getByRole('button', { name: 'Register' }).getAttribute('aria-pressed')).toBe(
      'true'
    );
    expect(screen.getByRole('button', { name: 'Create account' })).not.toBeNull();

    await fireEvent.click(within(modeGroup).getByRole('button', { name: 'Log in' }));

    expect(within(modeGroup).getByRole('button', { name: 'Log in' }).getAttribute('aria-pressed')).toBe(
      'true'
    );
    expect(screen.queryByRole('button', { name: 'Create account' })).toBeNull();
    expect(screen.getAllByRole('button', { name: 'Log in' })).toHaveLength(2);
  });

  it('does not render registration controls when registration is closed', () => {
    render(LoginPage, { props: { data: { ...data, registrationOpen: false } } });

    expect(screen.queryByRole('group', { name: 'Log in or register' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Log in' })).not.toBeNull();
    expect(screen.queryByRole('button', { name: 'Register' })).toBeNull();
  });
});
