<script lang="ts">
  import {
    MAX_FILES,
    MAX_FILE_BYTES,
    UploadFailed,
    uploadModel,
    type Model,
    type UploadState
  } from '$lib/upload';
  import { formatBytes } from '$lib/format';

  let {
    onclose,
    onuploaded
  }: {
    onclose: (opts?: { reload?: boolean }) => void;
    onuploaded: (model: Model, opts?: { keepOpen?: boolean }) => void;
  } = $props();

  type Queued = { file: File; state: UploadState; error?: string };

  let name = $state('');
  let queue = $state<Queued[]>([]);
  let error = $state('');
  let busy = $state(false);
  // Set when uploading again could produce a second copy: either a model was
  // created without all its files, or a request failed in a way that does not
  // prove it failed. Either way the dialog goes terminal - it says what it
  // knows and offers Done - because nothing in this milestone can delete a
  // duplicate once it exists.
  let done = $state('');
  // True when `done` was reached without knowing what the server did. Closing
  // then is not enough: the page would offer Upload again and the user could
  // make the second copy this whole dance exists to prevent. The way out is to
  // re-read the library, which settles the question either way.
  let unresolved = $state(false);

  function pick(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const picked = Array.from(input.files ?? []);
    error = '';

    // Drop whatever was picked before. Leaving the old selection uploadable
    // under a message about a different file is how you upload the wrong thing.
    queue = [];

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
    // `done` is checked here and not only on the button, because the name field
    // is still focusable and Enter in a text input submits the form.
    if (busy || done || queue.length === 0) return;

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
      // The count comes from re-reading the model and is authoritative; the
      // names are the best guess we have. They can disagree - a file whose
      // response was lost is present but still on this list - so the sentence
      // leads with the count and hedges the names rather than the other way
      // round.
      done = `${model.name} has ${model.fileCount} of ${queue.length} files. These may not have uploaded: ${failed.join(', ')}. You can add the rest once editing lands.`;
    } catch (failure) {
      const message = failure instanceof Error ? failure.message : 'Upload failed.';
      if (failure instanceof UploadFailed && !failure.certain) {
        // Might have landed. Offering Upload again here is what makes a second
        // copy of a model nobody can delete.
        done = `${message} The model may still have been created.`;
        unresolved = true;
        return;
      }
      error = message;
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

    {#if done}
      <p role="alert" class="mt-4 text-sm text-danger">{done}</p>
    {/if}

    <div class="mt-5 flex justify-end gap-2">
      {#if done}
        <button
          type="button"
          class="rounded bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink"
          onclick={() => onclose({ reload: unresolved })}
        >
          {unresolved ? 'Reload library' : 'Done'}
        </button>
      {:else}
        <button
          type="button"
          class="rounded border border-line-strong px-3 py-1.5 text-sm"
          onclick={() => onclose()}
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
