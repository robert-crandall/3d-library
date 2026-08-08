<script lang="ts">
  import { untrack } from 'svelte';
  import Modal from './Modal.svelte';
  import type { ModelDetail } from '$lib/upload';

  let {
    model,
    busy = false,
    error = '',
    onsave,
    oncancel
  }: {
    model: ModelDetail;
    busy?: boolean;
    /** The server's refusal, or a transport failure. Shown next to the inline
     *  check rather than instead of it, because they answer different
     *  questions. */
    error?: string;
    onsave: (edits: {
      name: string;
      description: string;
      printTips: string;
      sourceUrl: string;
    }) => void;
    oncancel: () => void;
  } = $props();

  // Seeded once from the model, then owned by this dialog. Explicitly untracked
  // rather than $derived: the whole point is that the fields diverge from the
  // model until Save, and a derived value would throw the user's typing away
  // the moment anything re-read the model underneath them.
  let edits = $state(
    untrack(() => ({
      name: model.name,
      description: model.description,
      printTips: model.printTips,
      sourceUrl: model.sourceUrl
    }))
  );

  // The one rule the server also enforces, checked here so an empty name is
  // caught before a round trip. Everything else - URL shape, lengths - is left
  // to the server, because duplicating a validator is how the two drift apart.
  let touched = $state(false);
  const nameEmpty = $derived(edits.name.trim() === '');

  function submit(event: SubmitEvent) {
    event.preventDefault();
    touched = true;
    if (busy || nameEmpty) return;
    onsave({ ...edits });
  }
</script>

<Modal title="Edit model" ondismiss={() => !busy && oncancel()}>
  <form onsubmit={submit}>
    <label class="mt-4 block text-sm font-medium" for="edit-name">Name</label>
    <input
      id="edit-name"
      class="mt-1 w-full rounded border border-line-strong px-3 py-2 text-sm"
      bind:value={edits.name}
      maxlength="200"
      aria-invalid={touched && nameEmpty}
    />
    {#if touched && nameEmpty}
      <p role="alert" class="mt-1 text-sm text-danger">A model needs a name.</p>
    {/if}

    <label class="mt-4 block text-sm font-medium" for="edit-description">Description</label>
    <textarea
      id="edit-description"
      class="mt-1 h-24 w-full rounded border border-line-strong px-3 py-2 text-sm"
      bind:value={edits.description}
      maxlength="5000"
    ></textarea>

    <label class="mt-4 block text-sm font-medium" for="edit-print-tips">Print tips</label>
    <textarea
      id="edit-print-tips"
      class="mt-1 h-24 w-full rounded border border-line-strong px-3 py-2 text-sm"
      bind:value={edits.printTips}
      maxlength="5000"
    ></textarea>

    <label class="mt-4 block text-sm font-medium" for="edit-source-url">Source URL</label>
    <input
      id="edit-source-url"
      class="mt-1 w-full rounded border border-line-strong px-3 py-2 text-sm"
      bind:value={edits.sourceUrl}
      maxlength="2000"
      placeholder="https://www.printables.com/model/…"
    />

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
        class="rounded bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink"
        disabled={busy}
      >
        {busy ? 'Saving…' : 'Save'}
      </button>
    </div>
  </form>
</Modal>
