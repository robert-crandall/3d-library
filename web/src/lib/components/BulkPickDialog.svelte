<script lang="ts">
  import Modal from './Modal.svelte';

  type Choice = { id: number; name: string; color?: string };

  let {
    title,
    prompt,
    confirm,
    choices,
    empty,
    busy = false,
    error = '',
    onpick,
    oncancel
  }: {
    title: string;
    prompt: string;
    /** The verb on the button. Says what it does, like every other dialog here. */
    confirm: string;
    choices: Choice[];
    /** What to say when there is nothing to pick, e.g. "No categories yet." */
    empty: string;
    busy?: boolean;
    error?: string;
    onpick: (id: number) => void;
    oncancel: () => void;
  } = $props();

  let chosen = $state<number>();

  function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy || chosen === undefined) return;
    onpick(chosen);
  }

  // Unique per instance, so the two of these on the library page cannot end up
  // with radios in the same group.
  const group = $props.id();
</script>

<!--
  Pick exactly one of a list. Shared by bulk recategorize and bulk add-to-
  collection, which are the same dialog with different words - two real uses,
  which is why this is a component and not two copies.

  Radios and a button rather than a clickable list: the list is the choice and
  the button is the commitment, so a mis-click picks the wrong category instead
  of applying it to everything selected.
-->
<Modal {title} ondismiss={() => !busy && oncancel()}>
  <form onsubmit={submit}>
    <p class="mt-3 text-sm text-muted">{prompt}</p>

    {#if choices.length === 0}
      <p class="mt-4 text-sm text-muted">{empty}</p>
    {:else}
      <div class="mt-3 max-h-64 overflow-y-auto rounded border border-line">
        {#each choices as choice (choice.id)}
          <label class="flex items-center gap-2 px-3 py-2 text-sm">
            <input
              type="radio"
              name={group}
              value={choice.id}
              checked={chosen === choice.id}
              onchange={() => (chosen = choice.id)}
              disabled={busy}
            />
            {#if choice.color}
              <span
                class="h-2.5 w-2.5 shrink-0 rounded-xs"
                style="background-color: {choice.color}"
                aria-hidden="true"
              ></span>
            {/if}
            <span class="truncate">{choice.name}</span>
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
        disabled={busy || chosen === undefined}
      >
        {busy ? 'Saving…' : confirm}
      </button>
    </div>
  </form>
</Modal>
