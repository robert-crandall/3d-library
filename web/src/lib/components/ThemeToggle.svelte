<script lang="ts">
  import { theme } from '$lib/theme.svelte';
</script>

<!--
  A switch rather than a button: it has an on/off state, and `aria-checked` is
  what tells a screen reader which. The label stays "Dark mode" in both states -
  a label that changes to "Light mode" when checked reads as though the control
  does the opposite of what it says.
-->
<button
  type="button"
  role="switch"
  aria-checked={theme.dark}
  class="flex items-center justify-between rounded border border-line-strong px-3 py-1.5 text-sm"
  onclick={() => theme.toggle()}
>
  <span>Dark mode</span>
  <span
    class="ml-3 h-4 w-8 shrink-0 rounded-full border border-line-strong p-0.5"
    aria-hidden="true"
  >
    <span
      class="block h-3 w-3 rounded-full bg-ink transition-transform"
      class:translate-x-4={theme.dark}
    ></span>
  </span>
</button>
