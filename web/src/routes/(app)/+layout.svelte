<script lang="ts">
  import SignOutButton from '$lib/components/SignOutButton.svelte';
  import ThemeToggle from '$lib/components/ThemeToggle.svelte';
  import { page } from '$app/state';

  let { children } = $props();

  // Derived, not hardcoded. There are two routes now, and a nav that always
  // claims to be on the current page tells a screen reader the wrong thing
  // everywhere except the library.
  const onLibrary = $derived(page.url.pathname === '/');
</script>

<!--
  The signed-in chrome: a fixed sidebar and whatever page is showing.

  This is the layout the template deliberately did not ship - it left `(app)`
  as a guard with no component so a fork could pick its own. Screen 1a of the
  design is that pick.

  Only the nav item the milestone actually has. The design's sidebar also lists
  categories, tags and collections; those are later milestones and a dead link
  is worse than an absent one.
-->
<div class="flex min-h-screen bg-page text-ink">
  <aside
    class="flex w-62 shrink-0 flex-col border-r border-line bg-sidebar"
  >
    <div class="flex items-center gap-2 px-5 py-5">
      <span
        class="grid h-7 w-7 place-items-center rounded bg-accent text-sm font-bold text-accent-ink"
        aria-hidden="true">3D</span
      >
      <span class="font-semibold">Library</span>
    </div>

    <nav class="px-3" aria-label="Library">
      <a
        href="/"
        aria-current={onLibrary ? 'page' : undefined}
        class="flex items-center rounded px-3 py-2 text-sm font-medium"
        class:bg-line-strong={onLibrary}
      >
        All models
      </a>
    </nav>

    <div class="mt-auto flex flex-col gap-3 border-t border-line px-5 py-4">
      <ThemeToggle />
      <SignOutButton />
    </div>
  </aside>

  <main class="min-w-0 flex-1">
    {@render children()}
  </main>
</div>
