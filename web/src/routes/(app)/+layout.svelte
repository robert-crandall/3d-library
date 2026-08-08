<script lang="ts">
  import LibrarySidebar from '$lib/components/LibrarySidebar.svelte';
  import { library } from '$lib/library.svelte';

  let { children } = $props();

  // Read once for the whole signed-in app, here rather than on each page,
  // because the sidebar is in this layout and outlives every navigation. Pages
  // call library.refresh() again when they change something a count depends on.
  //
  // The reset is for the second sign-in in one tab: this layout unmounts on the
  // way out to /login and mounts again on the way back, but the store is module
  // state that survives both, so entering the app has to start from nothing
  // rather than from whoever was here before.
  library.reset();
  library.refresh();
</script>

<!--
  The signed-in chrome: a fixed sidebar and whatever page is showing.

  This is the layout the template deliberately did not ship - it left `(app)`
  as a guard with no component so a fork could pick its own. Screen 1a of the
  design is that pick.
-->
<div class="flex min-h-screen bg-page text-ink">
  <LibrarySidebar />

  <main class="min-w-0 flex-1">
    {@render children()}
  </main>
</div>
