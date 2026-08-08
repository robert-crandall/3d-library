import { redirect } from '@sveltejs/kit';
import { auth } from '$lib/auth.svelte';

/**
 * Guard the whole group, from `load` rather than from a component. SvelteKit
 * does not render below a layout until its `load` resolves, so a signed-out
 * visitor gets a redirect instead of a flash of the page - `auth.spec.ts`
 * measures that with a MutationObserver rather than trusting it.
 *
 * There is no `+layout.svelte` beside this file, and that is the point: the
 * group is a guard and nothing else, so signed-in pages inherit a session and
 * no chrome. SvelteKit renders the children through its built-in fallback
 * layout when a layout module has no component.
 *
 * On the layout, not on each page: every route in this group sits behind the
 * session, so a copy of these five lines per page would only be a chance to
 * forget one.
 */
export async function load() {
  await auth.ensure();
  if (!auth.user) redirect(307, '/login');
  return { user: auth.user };
}
