import { api, type User } from '$lib/api/client';

/**
 * Who is signed in, according to the server.
 *
 * The session lives in an HttpOnly cookie, so the SPA cannot read it - the only
 * way to know is to ask `/api/auth/me`. This asks exactly once per page load and
 * caches the promise, so N route guards awaiting it produce one request.
 */
let user = $state<User | null>(null);
let booted: Promise<void> | null = null;

async function boot() {
  try {
    // openapi-fetch resolves rather than throwing on a 401, leaving data
    // undefined. The catch is for the network itself failing.
    const { data } = await api.GET('/api/auth/me');
    user = data ?? null;
  } catch {
    // Server unreachable. Treat it as logged out rather than leaving the app
    // wedged on a promise that never resolves.
    user = null;
  }
}

export const auth = {
  get user() {
    return user;
  },

  /** Resolve once the server has been asked. Never rejects, so a cached
   *  failure cannot poison every later navigation. */
  ensure: () => (booted ??= boot()),

  /** After a successful login or registration - the response body already told
   *  us who this is, so there is nothing to re-fetch. */
  signedIn(u: User) {
    user = u;
    booted = Promise.resolve();
  },

  signOut() {
    user = null;
    booted = Promise.resolve();
  }
};
