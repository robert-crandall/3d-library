<script lang="ts">
  import {
    MAX_FILES,
    MAX_FILE_BYTES,
    UploadFailed,
    addFiles,
    uploadModel,
    type ModelDetail,
    type UploadState
  } from '$lib/upload';
  import { formatBytes } from '$lib/format';
  import Modal from './Modal.svelte';

  let {
    model,
    onclose,
    onuploaded
  }: {
    /**
     * Present for "add files to this model", absent for "create a model".
     *
     * A mode rather than a second component: the two flows share the picker,
     * the size and count checks, the per-file progress list and the failure
     * rendering, which is nearly all of this file. The differences are the name
     * field, which function is called, and how many slots are left.
     */
    model?: { id: number; fileCount: number };
    onclose: (opts?: { reload?: boolean }) => void;
    onuploaded?: (model: ModelDetail, opts?: { keepOpen?: boolean }) => void;
  } = $props();

  type Queued = { file: File; state: UploadState; error?: string };

  let name = $state('');
  let queue = $state<Queued[]>([]);
  let error = $state('');
  let busy = $state(false);
  // Set when uploading again could produce a second copy: either a model was
  // created without all its files, or a request failed in a way that does not
  // prove it failed. Either way the dialog goes terminal - it says what it
  // knows and offers Done - because a duplicate is a mess to clean up by hand
  // even now that it can be deleted.
  let done = $state('');
  // True when `done` was reached without knowing what the server did. Closing
  // then is not enough: the page would offer Upload again and the user could
  // make the second copy this whole dance exists to prevent. The way out is to
  // re-read, which settles the question either way.
  let unresolved = $state(false);

  // How many more files this model can take. Only meaningful in add mode; the
  // create flow always has the full allowance.
  const slots = $derived(model ? MAX_FILES - model.fileCount : MAX_FILES);

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
    if (picked.length > slots) {
      error = model
        ? `This model has room for ${slots} more ${slots === 1 ? 'file' : 'files'}.`
        : `Pick at most ${MAX_FILES} files.`;
      input.value = '';
      return;
    }

    queue = picked.map((file) => ({ file, state: 'queued' }));
    // Default the name to the first file, minus its extension. Naming a model
    // after its main file is what most uploads want, and it is still editable.
    if (!model && !name && picked.length > 0) name = picked[0].name.replace(/\.[^.]+$/, '');
  }

  function progress(index: number, state: UploadState, failure?: string) {
    queue[index] = { ...queue[index], state, error: failure };
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    // `done` is checked here and not only on the button, because the name field
    // is still focusable and Enter in a text input submits the form.
    if (busy || done || queue.length === 0) return;

    error = '';
    busy = true;
    try {
      if (model) {
        const { failed } = await addFiles(
          model.id,
          queue.map((q) => q.file),
          progress
        );
        if (failed.length > 0) {
          // No count here, unlike the create flow: the page re-reads the model
          // the moment this closes, so the server says what landed. All this
          // has to say is which files to try again.
          error = `These did not upload: ${failed.join(', ')}.`;
          return;
        }
        onclose({ reload: true });
        return;
      }

      const { model: created, failed } = await uploadModel(
        name.trim() || 'Untitled',
        queue.map((q) => q.file),
        progress
      );
      if (failed.length === 0) {
        onuploaded?.(created);
        return;
      }
      // Put it in the grid first, so what the user sees matches what exists.
      onuploaded?.(created, { keepOpen: true });
      // The count comes from re-reading the model and is authoritative; the
      // names are the best guess we have. They can disagree - a file whose
      // response was lost is present but still on this list - so the sentence
      // leads with the count and hedges the names rather than the other way
      // round.
      done = `${created.name} has ${created.fileCount} of ${queue.length} files. These may not have uploaded: ${failed.join(', ')}. Add the rest from the model page.`;
    } catch (failure) {
      const message = failure instanceof Error ? failure.message : 'Upload failed.';
      if (failure instanceof UploadFailed && !failure.certain) {
        // Might have landed - or definitely did, and we could not read it back.
        // Either way, offering Upload again here is what makes a second copy.
        // Which case it is is baked into the message where the failure is
        // thrown, because only there is it known.
        done = message;
        unresolved = true;
        return;
      }
      error = message;
    } finally {
      busy = false;
    }
  }

  function cancel() {
    // Add mode reloads on cancel: some files may already have uploaded before
    // the user gave up on the rest, and leaving the page showing the old count
    // is the one thing the model page must never do.
    onclose({ reload: model !== undefined });
  }

  function dismiss() {
    // Escape does whatever the dismissing button would have done, including
    // doing nothing while that button is disabled mid-upload.
    if (done) onclose({ reload: unresolved });
    else if (!busy) cancel();
  }
</script>

<!--
  A modal over the page rather than its own route: uploading is a thing you do
  *to* a library or a model, and coming back to a re-fetched list would lose the
  scroll position the user was at.

  The file list is deliberately unkeyed: it is built once by `pick`, replaced
  entry-by-entry at the same index as uploads progress, and never reordered or
  spliced. A key here would only be a claim about identity that nothing needs -
  and keying on the filename was worse than nothing, because two files can share
  a basename and Svelte would then reuse the wrong row's state.
-->
<Modal title={model ? 'Add files' : 'Upload a model'} ondismiss={dismiss}>
  <form onsubmit={submit}>
    {#if !model}
      <label class="mt-4 block text-sm font-medium" for="model-name">Name</label>
      <input
        id="model-name"
        class="mt-1 w-full rounded border border-line-strong px-3 py-2 text-sm"
        bind:value={name}
        maxlength="200"
        placeholder="Benchy"
      />
    {/if}

    <label class="mt-4 block text-sm font-medium" for="model-files">Files</label>
    <input
      id="model-files"
      class="mt-1 w-full rounded border border-line-strong px-3 py-2 text-sm"
      type="file"
      multiple
      onchange={pick}
    />
    <p class="mt-1 text-xs text-muted">
      {#if model}
        Room for {slots} more, {formatBytes(MAX_FILE_BYTES)} each.
      {:else}
        Up to {MAX_FILES} files, {formatBytes(MAX_FILE_BYTES)} each. They become one model.
      {/if}
    </p>

    {#if queue.length > 0}
      <ul class="mt-4 space-y-1 text-sm">
        {#each queue as item}
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
          onclick={cancel}
          disabled={busy}
        >
          Cancel
        </button>
        <button
          type="submit"
          class="rounded bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink"
          disabled={busy || queue.length === 0}
        >
          {busy ? 'Uploading…' : model ? 'Add' : 'Upload'}
        </button>
      {/if}
    </div>
  </form>
</Modal>
