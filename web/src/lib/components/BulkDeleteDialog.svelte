<script lang="ts">
  import Modal from './Modal.svelte';
  import { api } from '$lib/api/client';
  import { apiErrorMessage } from '$lib/api/errors';

  let {
    modelIds,
    ondeleted,
    oncancel
  }: {
    modelIds: number[];
    ondeleted: () => void;
    oncancel: () => void;
  } = $props();

  type Preview = { models: number; versions: number; files: number };

  let preview = $state<Preview>();
  let status = $state<'checking' | 'ready' | 'failed'>('checking');
  let busy = $state(false);
  let error = $state('');
  // Set when the server refused because the numbers moved. The sentence below
  // is then about a set the user has not agreed to yet, so it needs a fresh
  // look before the button means anything.
  let changed = $state(false);

  async function check() {
    status = 'checking';
    error = '';
    try {
      const { data, error: failure } = await api.POST('/api/models/bulk/delete-preview', {
        body: { modelIds }
      });
      if (failure) {
        error = apiErrorMessage(failure, 'Could not work out what would be deleted.');
        status = 'failed';
        return;
      }
      preview = data;
      status = 'ready';
    } catch {
      error = 'Could not reach the server.';
      status = 'failed';
    }
  }

  // No generation counter: modelIds is fixed for the life of the dialog - the
  // page closes it rather than re-pointing it - so there is only ever one of
  // these in flight.
  $effect(() => {
    check();
  });

  async function confirm() {
    if (!preview || busy) return;
    busy = true;
    error = '';
    try {
      const { error: failure, response } = await api.POST('/api/models/bulk/delete', {
        body: {
          modelIds,
          expectVersions: preview.versions,
          expectFiles: preview.files
        }
      });
      if (failure) {
        // 409 is the one refusal the user can do something about: something
        // changed between opening this and pressing the button, so re-read and
        // make them agree to the new sentence.
        if (response.status === 409) {
          changed = true;
          await check();
          return;
        }
        error = apiErrorMessage(failure, 'Could not delete the models.');
        return;
      }
      ondeleted();
    } catch {
      error = 'Could not reach the server.';
    } finally {
      busy = false;
    }
  }

  // Versions are only mentioned when there are any. "and its 0 versions" is
  // noise on the common case, and the single-model dialog already sets this
  // precedent.
  const sentence = $derived(
    preview === undefined
      ? ''
      : `${preview.models} ${preview.models === 1 ? 'model' : 'models'}` +
        (preview.versions > 0
          ? `, ${preview.versions} ${preview.versions === 1 ? 'version' : 'versions'}`
          : '') +
        `, and all ${preview.files} ${preview.files === 1 ? 'file' : 'files'}` +
        ' will be deleted. This cannot be undone.'
  );
</script>

<!--
  The bulk delete confirmation. It counts what it is about to destroy from the
  server rather than from the grid, because a listed model's file count is its
  own files and deleting a root takes its versions and their files too - so a
  number added up on the client under-reports exactly when a model has versions.

  The count is also sent back with the delete and rechecked under the locks, so
  a version attached in another tab while this was open cannot make the sentence
  a lie. That is worth the round trip here and nowhere else: this is the only
  dialog in the app whose button is permanent.
-->
<Modal title="Delete models" ondismiss={() => !busy && oncancel()}>
  {#if status === 'checking'}
    <p class="mt-3 text-sm text-muted">Checking what will be deleted…</p>
  {:else if status === 'ready'}
    {#if changed}
      <p role="alert" class="mt-3 text-sm text-danger">
        The selection changed while this was open. Here is what will be deleted now.
      </p>
    {/if}
    <p class="mt-3 text-sm text-muted">{sentence}</p>
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
      type="button"
      class="rounded bg-danger px-3 py-1.5 text-sm font-medium text-accent-ink disabled:opacity-50"
      onclick={confirm}
      disabled={busy || status !== 'ready'}
    >
      {busy ? 'Deleting…' : 'Delete'}
    </button>
  </div>
</Modal>
