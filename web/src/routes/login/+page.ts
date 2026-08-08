import { redirect } from '@sveltejs/kit';
import { api } from '$lib/api/client';
import { auth } from '$lib/auth.svelte';

/**
 * What a Google sign-in that didn't happen looks like when the browser lands
 * back here.
 *
 * This is the one place the frontend owns error copy, and it's the exception
 * that proves $lib/api/errors' rule rather than a break with it. There is no
 * server response to render: the foundation redirects a failed OAuth flow to
 * `/login?error=<code>` with a fixed, internal vocabulary, deliberately so that
 * Google's own error strings are never reflected back. A code is not a sentence,
 * so somebody has to write one, and only the SPA can.
 *
 * `registration_closed` says what to do next because it's the one a real person
 * hits by accident: either the app has no accounts yet (Google never creates the
 * first one - that door stays password-only so a broken OAuth client can't lock
 * the owner out), or they're a stranger at a closed door.
 */
const OAUTH_ERRORS: Record<string, string> = {
  oauth_denied: 'Google sign-in was cancelled.',
  invalid_state: 'That sign-in link expired. Please try again.',
  token_exchange_failed: 'Could not complete sign-in with Google. Please try again.',
  invalid_id_token: 'Could not complete sign-in with Google. Please try again.',
  registration_closed:
    "That Google account isn't registered. Sign in with a password, or create an account first."
};

/** The inverse of the guard on `/`: someone already signed in has no business
 *  on the login page. */
export async function load({ url }) {
  await auth.ensure();
  if (auth.user) redirect(307, '/');

  // Only asked for someone who is actually going to see the form - hence after
  // the redirect. registrationOpen is advisory state: under the default gate the
  // register handler re-checks under a lock, so this page can lose the race and
  // has to render the resulting error rather than assume it can't happen.
  //
  // Falling back to closed / no Google is the useful direction when the call
  // fails: login is the case for the whole life of a single-account app, and
  // offering a registration the server would refuse - or a button whose route
  // isn't mounted - helps nobody. Both failure shapes land there. openapi-fetch
  // resolves rather than throwing on a non-2xx and leaves `data` undefined, so
  // the `??` covers a refusal; the `catch` is for the network itself failing,
  // which it does throw for.
  let registrationOpen = false;
  let googleLoginEnabled = false;
  try {
    const { data } = await api.GET('/api/app');
    registrationOpen = data?.registrationOpen ?? false;
    googleLoginEnabled = data?.googleLoginEnabled ?? false;
  } catch {
    // Server unreachable. Same fallback.
  }

  // An unrecognised code still gets a message: a future foundation code should
  // degrade to something honest rather than to a silent blank page.
  const code = url.searchParams.get('error');
  const oauthError = code ? (OAUTH_ERRORS[code] ?? 'Could not sign in with Google.') : '';

  return { registrationOpen, googleLoginEnabled, oauthError };
}
