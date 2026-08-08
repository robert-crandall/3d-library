<script lang="ts">
  import { MAX_FILES, MAX_FILE_BYTES, uploadModel, type Model, type UploadState } from '$lib/upload';
  import { formatBytes } from '$lib/format';

  let {
    onclose,
    onuploaded
  }: {
    onclose: () => void;
    onuploaded: (model: Model, opts?: { keepOpen?: boolean }) => void;
  } = $props();

  type Queued = { file: File; state: UploadState; error?: string };

  let name = $state('');
  let queue = $state<Queued[]>([]);
  let error = $state('');
  let busy = $state(false);
  // Set when a model was created but not every file made it. The model is
  // already in the grid at that point, so the only honest thing left to do is
  // say what is missing and close - uploading again would make a second copy,
  // and nothing in this milestone can delete either one.
  let partial = $state('');

  function pick(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const picked = Array.from(input.files ?? []);
    error = '';

    const oversized = picked.find((f) => f.size > MAX_FILE_BYTES);
    if (oversized) {
      // Refuse before uploading rather than after. The server refuses too, but
      // only once the bytes have arrived, and finding out at the end of a
      // 600 MB upload is a bad way to learn about a limit.
      error = `${oversized.name} is ${formatBytes(oversized.size)}, over the ${formatBytes(MAX_FILE_BYTES)} limit.`;
      input.value = '';
      return;
    }
    if (picked.length > MAX_FILES) {
      error = `Pick at most ${MAX_FILES} files.`;
      input.value = '';
      return;
    }

    queue = picked.map((file) => ({ file, state: 'queued' }));
    // Default the name to the first file, minus its extension. Naming a model
    // after its main file is what most uploads want, and it is still editable.
    if (!name && picked.length > 0) name = picked[0].name.replace(/\.[^.]+$/, '');
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy || queue.length === 0) return;

    error = '';
    busy = true;
    try {
      const { model, failed } = await uploadModel(
        name.trim() || 'Untitled',
        queue.map((q) => q.file),
        (index, state, failure) => {
          queue[index] = { ...queue[index], state, error: failure };
        }
      );
      if (failed.length === 0) {
        onuploaded(model);
        return;
      }
      // Put it in the grid first, so what the user sees matches what exists.
      onuploaded(model, { keepOpen: true });
      partial = `${model.name} was created without ${failed.join(', ')}. You can add the rest once editing lands.`;
    } catch (failure) {
      error = failure instanceof Error ? failure.message : 'Upload failed.';
    } finally {
      busy = false;
    }
  }
</script>

<!--
  A modal over the grid rather than its own route: uploading is a thing you do
  *to* the library, and coming back to a re-fetched list would lose the scroll
  position the user was at.

  Native <dialog> is not used because it needs an effect to call showModal(),
  and the one thing it buys - focus trapping - is not worth that when the
  content is three controls.
-->
<div class="fixed inset-0 grid place-items-center bg-black/40 p-4">
  <form
    class="w-full max-w-md rounded-tile border border-line bg-surface p-5"
    onsubmit={submit}
    aria-labelledby="upload-title"
  >
    <h2 id="upload-title" class="text-lg font-semibold">Upload a model</h2>

    <label class="mt-4 block text-sm font-medium" for="model-name">Name</label>
    <input
      id="model-name"
      class="mt-1 w-full rounded border border-line-strong px-3 py-2 text-sm"
      bind:value={name}
      maxlength="200"
      placeholder="Benchy"
    />

    <label class="mt-4 block text-sm font-medium" for="model-files">Files</label>
    <input
      id="model-files"
      class="mt-1 w-full rounded border border-line-strong px-3 py-2 text-sm"
      type="file"
      multiple
      onchange={pick}
    />
    <p class="mt-1 text-xs text-muted">
      Up to {MAX_FILES} files, {formatBytes(MAX_FILE_BYTES)} each. They become one model.
    </p>

    {#if queue.length > 0}
      <ul class="mt-4 space-y-1 text-sm">
        {#each queue as item (item.file.name)}
          <li class="flex items-baseline justify-between gap-3">
            <span class="min-w-0 truncate">{item.file.name}</span>
            <span
              class="shrink-0 text-xs"
              class:text-danger={item.state === 'failed'}
              class:text-muted={item.state !== 'failed'}
            >
              {#if item.state === 'queued'}Queued
              {:else if item.state === 'uploading'}Uploading…
              {:else if item.state === 'done'}Uploaded
              {:else}Failed{/if}
            </span>
          </li>
        {/each}
      </ul>
    {/if}

    {#if error}
      <p role="alert" class="mt-4 text-sm text-danger">{error}</p>
    {/if}

    {#if partial}
      <p role="alert" class="mt-4 text-sm text-danger">{partial}</p>
    {/if}

    <div class="mt-5 flex justify-end gap-2">
      {#if partial}
        <button
          type="button"
          class="rounded bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink"
          onclick={onclose}
        >
          Done
        </button>
      {:else}
        <button
          type="button"
          class="rounded border border-line-strong px-3 py-1.5 text-sm"
          onclick={onclose}
          disabled={busy}
        >
          Cancel
        </button>
        <button
          type="submit"
          class="rounded bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink"
          disabled={busy || queue.length === 0}
        >
          {busy ? 'Uploading…' : 'Upload'}
        </button>
      {/if}
    </div>
  </form>
</div>
