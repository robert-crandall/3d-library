<script lang="ts">
  let {
    count,
    busy = false,
    ontag,
    oncategorize,
    oncollect,
    ondelete,
    onclear
  }: {
    count: number;
    busy?: boolean;
    ontag: () => void;
    oncategorize: () => void;
    oncollect: () => void;
    ondelete: () => void;
    onclear: () => void;
  } = $props();
</script>

<!--
  The bulk action bar. Rendered only when something is selected, rather than
  always present and disabled: four dead buttons above an untouched grid are
  four things to explain, and the bar appearing is itself the feedback that a
  ctrl-click did something.

  aria-live, because the appearance and the count are the only confirmation
  that a modified click landed, and neither is announced otherwise.

  No error line here. Every action is behind a dialog, the dialog stays open
  when the server refuses, and the message belongs next to the button that was
  pressed - a copy here would say the same thing twice.
-->
<div
  class="mt-4 flex flex-wrap items-center gap-2 rounded border border-line bg-surface px-3 py-2"
  role="status"
  aria-live="polite"
>
  <span class="text-sm font-medium">{count} selected</span>
  <div class="ml-auto flex flex-wrap items-center gap-2">
    <button
      type="button"
      class="rounded border border-line-strong px-3 py-1.5 text-sm"
      onclick={ontag}
      disabled={busy}>Add tags</button
    >
    <button
      type="button"
      class="rounded border border-line-strong px-3 py-1.5 text-sm"
      onclick={oncategorize}
      disabled={busy}>Recategorize</button
    >
    <button
      type="button"
      class="rounded border border-line-strong px-3 py-1.5 text-sm"
      onclick={oncollect}
      disabled={busy}>Add to collection</button
    >
    <button
      type="button"
      class="rounded bg-danger px-3 py-1.5 text-sm font-medium text-accent-ink"
      onclick={ondelete}
      disabled={busy}>Delete</button
    >
    <button type="button" class="px-2 py-1.5 text-sm text-muted underline" onclick={onclear}
      >Clear</button
    >
  </div>
</div>
