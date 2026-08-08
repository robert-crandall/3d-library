<script lang="ts">
  import ModelTile from '$lib/components/ModelTile.svelte';
  import UploadDialog from '$lib/components/UploadDialog.svelte';
  import { api } from '$lib/api/client';
  import { apiErrorMessage } from '$lib/api/errors';
  import type { Model } from '$lib/upload';

  let models = $state<Model[]>([]);
  // Three states, not a boolean and a string. "We asked and you have nothing"
  // and "we could not ask" must not render the same, and with only `loading`
  // plus `error` a failed load falls through to the empty state - which tells
  // the user their library is empty when nobody knows whether it is.
  let status = $state<'loading' | 'ready' | 'failed'>('loading');
  let error = $state('');
  let uploading = $state(false);

  async function load() {
    error = '';
    try {
      const { data, error: failure } = await api.GET('/api/models');
      if (failure) {
        error = apiErrorMessage(failure, 'Could not load the library.');
        status = 'failed';
        return;
      }
      models = data ?? [];
      status = 'ready';
    } catch {
      // openapi-fetch lets a fetch-level rejection through, so without this the
      // page would sit on "Loading…" forever.
      error = 'Could not reach the server.';
      status = 'failed';
    }
  }

  load();

  // Prepend rather than re-fetch: the list is newest-first and the upload just
  // returned the finished model, so a second round trip would only be a chance
  // for the two to disagree.
  function uploaded(model: Model) {
    models = [model, ...models];
    uploading = false;
  }
</script>

<div class="px-8 py-7">
  <header class="flex items-start justify-between gap-4">
    <div>
      <p class="text-xs font-medium tracking-wide text-faint uppercase">Library</p>
      <h1 class="mt-1 text-2xl font-semibold">All models</h1>
    </div>
    <button
      type="button"
      class="rounded bg-accent px-4 py-2 text-sm font-medium text-accent-ink"
      onclick={() => (uploading = true)}
    >
      Upload
    </button>
  </header>

  {#if error}
    <p role="alert" class="mt-6 text-sm text-danger">{error}</p>
  {/if}

  {#if status === 'loading'}
    <p class="mt-8 text-sm text-muted">Loading…</p>
  {:else if status === 'failed'}
    <button type="button" class="mt-6 rounded border border-line-strong px-3 py-1.5 text-sm" onclick={load}>
      Try again
    </button>
  {:else if models.length === 0}
    <!--
      The empty state is a real screen, not an absence. It is the first thing a
      new user sees, and a blank area under a header reads as a failed load.
    -->
    <div class="mt-8 rounded-tile border border-dashed border-line-strong px-6 py-16 text-center">
      <h2 class="text-base font-medium">Nothing here yet</h2>
      <p class="mx-auto mt-2 max-w-sm text-sm text-muted">
        Upload an STL, a 3MF, a G-code file or a photo, and it shows up here as a model.
      </p>
      <button
        type="button"
        class="mt-6 rounded bg-accent px-4 py-2 text-sm font-medium text-accent-ink"
        onclick={() => (uploading = true)}
      >
        Upload your first model
      </button>
    </div>
  {:else}
    <ul class="mt-7 grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      {#each models as model (model.id)}
        <li><ModelTile {model} /></li>
      {/each}
    </ul>
  {/if}
</div>

{#if uploading}
  <UploadDialog onclose={() => (uploading = false)} onuploaded={uploaded} />
{/if}
