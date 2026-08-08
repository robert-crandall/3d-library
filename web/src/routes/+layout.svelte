<script lang="ts">
  import '../app.css';
  import { afterNavigate, replaceState } from '$app/navigation';
  import { page } from '$app/state';

  let { children } = $props();

  /**
   * Put the address bar back where the rendered page actually is.
   *
   * SvelteKit only writes the history entry for navigations it pushed itself:
   * in 2.70.1, `navigate()` recurses with the same `popped` object when a `load`
   * throws `redirect()`, and the history write below it is behind `if (!popped)`.
   * So a Back onto a route whose guard bounces renders the redirect target under
   * the URL you popped to - the greeting at `/login`, or the login form at `/`.
   * Cosmetic, since the content always matches the real auth state, but an
   * address bar that lies is still a lie.
   *
   * The mismatch itself is the condition, rather than
   * `navigation.type === 'popstate'`. What we want to hold is "the URL names the
   * page you're looking at"; checking for a popstate would only restate the
   * framework internals that happen to break it today.
   *
   * `replaceState` from `$app/navigation`, not `history.replaceState`: SvelteKit
   * keeps its history and navigation indices in each entry's state and reads
   * them back in its own popstate listener, which falls through to a URL-only
   * update when they're absent. A raw call would have to reconstruct that state
   * object by hand; this one does it for us, keeping the current indices and
   * recording the corrected URL for the next time we land on this entry.
   */
  afterNavigate(() => {
    if (page.url.href === location.href) return;
    replaceState(page.url, page.state);
  });
</script>

<!-- Nothing but the page. This layout is the `afterNavigate` fix above and the
     `app.css` import, and deliberately not a wrapper element: a template that
     picked the app's background, text colour or minimum height would be a look
     to undo before you could pick your own. -->
{@render children()}
