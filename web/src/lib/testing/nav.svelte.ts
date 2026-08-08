/**
 * A stand-in for `$app/state`'s `page`, for tests where a navigation has to
 * actually happen rather than a URL just being read once at mount.
 *
 * Mocking `$app/state` with a plain object gives a component its URL, but
 * assigning a new one later changes nothing on screen: a getter over an
 * ordinary variable gives `$derived` nothing to track. This is `$state`, so
 * setting `nav.url` re-runs the effects that read it - which is what clicking a
 * category in the sidebar does in the real app.
 *
 * Test-only. Nothing under `routes/` or `lib/components/` imports it, so it
 * never reaches the bundle.
 */
export const nav = $state({ url: new URL('http://localhost/') });
