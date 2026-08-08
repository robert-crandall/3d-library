<script lang="ts">
  import SignOutButton from '$lib/components/SignOutButton.svelte';

  let { data } = $props();
</script>

<!--
  The one example page, and the only thing behind the guard. It exists to prove
  a session works end to end - you are signed in, here is who you are, here is
  the way out - and it is meant to be replaced by whatever your app actually is.

  Sign-out lives here rather than in a layout on purpose. Chrome that offers it
  on every page would be this template deciding your app has a sidebar or a
  header, which is the decision it is trying not to make. `(app)/+layout.ts` is
  a guard with no component beside it, so pages inherit a session and nothing
  else. Move `SignOutButton` into a layout of your own once you know what your
  chrome looks like.

  The classes are structural and carry no colour: Tailwind's preflight resets
  headings to body size, so with none at all this reads as one undifferentiated
  paragraph.
-->
<main class="p-6">
  <h1 class="text-2xl font-bold">Hello</h1>
  <p class="mt-2">
    You're signed in as {data.user.email}. The API and this page are served by the same Go binary on
    the same port.
  </p>
  <div class="mt-6">
    <SignOutButton />
  </div>
</main>
