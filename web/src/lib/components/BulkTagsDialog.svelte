<script lang="ts">
  import Modal from './Modal.svelte';

  let {
    tags,
    count,
    busy = false,
    error = '',
    onapply,
    oncancel
  }: {
    tags: { id: number; name: string }[];
    /** How many models this will be applied to, so the prompt can say so. */
    count: number;
    busy?: boolean;
    error?: string;
    onapply: (tagIds: number[]) => void;
    oncancel: () => void;
  } = $props();

  let picked = $state<number[]>([]);

  function flip(id: number) {
    picked = picked.includes(id) ? picked.filter((x) => x !== id) : [...picked, id];
  }

  function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy || picked.length === 0) return;
    onapply(picked);
  }
</script>

<!--
  Check several tags and add them all. Checkboxes rather than the pick-one
  dialog's radios, because tags are additive: adding two in one pass is the
  normal case, and the endpoint takes a list.

  Nothing here shows which tags the models already have. They are a selection,
  not a model, so "already tagged" is per-model and there is no honest single
  answer to render; adding one that is already there is a no-op on the server.
-->
<Modal title="Add tags" ondismiss={() => !busy && oncancel()}>
  <form onsubmit={submit}>
    <p class="mt-3 text-sm text-muted">
      Add these tags to {count}
      {count === 1 ? 'model' : 'models'}. Tags they already have are left alone.
    </p>

    {#if tags.length === 0}
      <p class="mt-4 text-sm text-muted">No tags yet. Add one from a model's page first.</p>
    {:else}
      <div class="mt-3 max-h-64 overflow-y-auto rounded border border-line">
        {#each tags as tag (tag.id)}
          <label class="flex items-center gap-2 px-3 py-2 text-sm">
            <input
              type="checkbox"
              checked={picked.includes(tag.id)}
              onchange={() => flip(tag.id)}
              disabled={busy}
            />
            <span class="truncate">{tag.name}</span>
          </label>
        {/each}
      </div>
    {/if}

    {#if error}
      <p role="alert" class="mt-4 text-sm text-danger">{error}</p>
    {/if}

    <div class="mt-5 flex justify-end gap-2">
      <button
        type="button"
        class="rounded border border-line-strong px-3 py-1.5 text-sm"
        onclick={oncancel}
        disabled={busy}
      >
        Cancel
      </button>
      <button
        type="submit"
        class="rounded bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink disabled:opacity-50"
        disabled={busy || picked.length === 0}
      >
        {busy ? 'Saving…' : 'Add tags'}
      </button>
    </div>
  </form>
</Modal>
