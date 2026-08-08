<script lang="ts">
  import { goto } from '$app/navigation';
  import { api } from '$lib/api/client';
  import { apiErrorMessage } from '$lib/api/errors';
  import { auth } from '$lib/auth.svelte';

  let { data } = $props();

  // Only the explicit choice is state; the data decides the rest. Closed wins
  // outright, so a `load` that re-runs and comes back closed can't leave a
  // register form behind a toggle that is no longer on screen. Open opens on
  // Register, because that's the thing to do at that moment.
  let chosen = $state<'login' | 'register' | null>(null);
  const mode = $derived(data.registrationOpen ? (chosen ?? 'register') : 'login');
  let email = $state('');
  let password = $state('');
  let busy = $state(false);

  // One alert slot, two sources. The form's own error is local state; the OAuth
  // failure the browser arrived with lives in `data`, so it stays derived rather
  // than being copied into state - a copy would only ever hold the first value
  // `load` produced.
  //
  // `oauthDismissed` is what lets doing something else clear it: once you've
  // submitted the form or switched tabs, a complaint about a Google sign-in you
  // already abandoned is noise, and two stacked alerts are worse than one.
  let formError = $state('');
  let oauthDismissed = $state(false);
  const error = $derived(formError || (oauthDismissed ? '' : data.oauthError));

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    formError = '';
    oauthDismissed = true;
    busy = true;
    try {
      const path = mode === 'login' ? '/api/auth/login' : '/api/auth/register';
      const { data, error: failure } = await api.POST(path, { body: { email, password } });
      if (failure || !data) {
        // Whatever the server said, verbatim. See $lib/api/errors.
        formError = apiErrorMessage(failure);
        return;
      }
      auth.signedIn(data);
      await goto('/');
    } catch {
      formError = 'Could not reach the server.';
    } finally {
      busy = false;
    }
  }

  function switchTo(next: 'login' | 'register') {
    chosen = next;
    formError = '';
    oauthDismissed = true;
  }
</script>

<!--
  Semantic markup and the structural classes Tailwind's preflight takes away -
  it resets every border to zero width and every form control to a transparent
  background, so with no classes at all this page is a set of invisible boxes.
  Nothing here picks a colour, a font or a layout beyond centring the form:
  restyle it, or throw it away and write your own.
-->
<main class="mx-auto max-w-sm p-6">
  <!--
    Only when registration is open. Closed is the steady state for a
    single-account app, and a Register control that can only ever produce
    "registration is closed" is a dead end - see the load function.

    It stays a pair rather than becoming "the register form" because
    ALLOW_OPEN_REGISTRATION=true holds this open forever, and an app in that
    mode still needs a way to log in. One bool can't tell "open because there's
    no account yet" from "open because it's configured that way", and it doesn't
    need to: defaulting to Register while leaving Log in reachable is right for
    both.

    Two buttons with aria-pressed, not ARIA tabs: there is one form below, not a
    panel per tab, and tab roles would promise arrow-key navigation and
    aria-controls that do not exist here - worse for a screen reader than no
    ARIA at all.
  -->
  {#if data.registrationOpen}
    <div class="mb-4 flex gap-2" role="group" aria-label="Log in or register">
      <button
        type="button"
        class="rounded border px-3 py-1.5 {mode === 'login' ? 'font-bold' : ''}"
        aria-pressed={mode === 'login'}
        onclick={() => switchTo('login')}>Log in</button
      >
      <button
        type="button"
        class="rounded border px-3 py-1.5 {mode === 'register' ? 'font-bold' : ''}"
        aria-pressed={mode === 'register'}
        onclick={() => switchTo('register')}>Register</button
      >
    </div>
  {/if}

  <form onsubmit={submit} novalidate={false}>
    <label class="block" for="email">Email</label>
    <input
      id="email"
      name="email"
      type="email"
      required
      autocomplete="email"
      class="mt-1 w-full rounded border px-2 py-1.5"
      bind:value={email}
    />

    <label class="mt-4 block" for="password">Password</label>
    <input
      id="password"
      name="password"
      type="password"
      required
      minlength="8"
      autocomplete={mode === 'login' ? 'current-password' : 'new-password'}
      class="mt-1 w-full rounded border px-2 py-1.5"
      bind:value={password}
    />

    {#if error}
      <p role="alert" class="mt-4">{error}</p>
    {/if}

    <button type="submit" class="mt-5 w-full rounded border px-3 py-1.5" disabled={busy}>
      {mode === 'login' ? 'Log in' : 'Create account'}
    </button>
  </form>

  <!--
    A plain link, because the foundation's flow is entirely server-side:
    /api/auth/google/start 302s to Google's consent screen and the callback sets
    the same session cookie a password login sets. No Google script tag, no
    client SDK, nothing to initialise.

    data-sveltekit-reload because /api/... is not a SvelteKit route and this has
    to be a real navigation, not a client-side one.

    Shown in both modes, and specifically NOT hidden while registration is open.
    Google can only create an account under ALLOW_OPEN_REGISTRATION=true - it
    never bootstraps the first one - but `registrationOpen` is true for both that
    case and "no account exists yet", and cannot tell them apart (same limitation
    as the button pair above). Gating on it would hide the button in the one
    deployment where Google signup works, to spare the bootstrap case a message.
    So it stays, and `registration_closed` says what to do instead - see
    +page.ts.
  -->
  {#if data.googleLoginEnabled}
    <a
      href="/api/auth/google/start"
      data-sveltekit-reload
      class="mt-4 block rounded border px-3 py-1.5 text-center"
    >
      Sign in with Google
    </a>
  {/if}
</main>
