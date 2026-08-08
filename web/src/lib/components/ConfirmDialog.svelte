<script lang="ts">
  import Modal from './Modal.svelte';

  let {
    title,
    body,
    confirm,
    busy = false,
    error = '',
    onconfirm,
    oncancel
  }: {
    title: string;
    body: string;
    /** The verb on the destructive button. Never "OK" - the button should say
     *  what it does, so a misclick is a misclick and not a misunderstanding. */
    confirm: string;
    busy?: boolean;
    error?: string;
    onconfirm: () => void;
    oncancel: () => void;
  } = $props();
</script>

<Modal {title} ondismiss={() => !busy && oncancel()}>
  <p class="mt-3 text-sm text-muted">{body}</p>

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
      type="button"
      class="rounded bg-danger px-3 py-1.5 text-sm font-medium text-accent-ink"
      onclick={onconfirm}
      disabled={busy}
    >
      {busy ? 'Deleting…' : confirm}
    </button>
  </div>
</Modal>
