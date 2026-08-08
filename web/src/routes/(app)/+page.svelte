<script lang="ts">
  import ModelTile from '$lib/components/ModelTile.svelte';
  import UploadDialog from '$lib/components/UploadDialog.svelte';
  import { api } from '$lib/api/client';
  import { apiErrorMessage } from '$lib/api/errors';
  import { library } from '$lib/library.svelte';
  import { page } from '$app/state';
  import type { Model } from '$lib/upload';

  let models = $state<Model[]>([]);
  // Three states, not a boolean and a string. "We asked and you have nothing"
  // and "we could not ask" must not render the same, and with only `loading`
  // plus `error` a failed load falls through to the empty state - which tells
  // the user their library is empty when nobody knows whether it is.
  let status = $state<'loading' | 'ready' | 'failed'>('loading');
  let error = $state('');
  let uploading = $state(false);

  // The filter is the URL's, so it is the sidebar's and this page's at once
  // without either telling the other. `query` is the string the server sees;
  // deriving the fetch from it means Back and a shared link both work.
  const params = $derived(page.url.searchParams);
  const categoryId = $derived(params.get('categoryId'));
  const tagId = $derived(params.get('tagId'));
  const uncategorized = $derived(params.get('uncategorized') === 'true');
  const query = $derived(page.url.search);

  const category = $derived(library.categories.find((c) => String(c.id) === categoryId));
  const tag = $derived(library.tags.find((t) => String(t.id) === tagId));
  const filtered = $derived(Boolean(categoryId || tagId || uncategorized));
  const heading = $derived(
    category?.name ?? (uncategorized ? 'Uncategorized' : (tag ? `#${tag.name}` : 'All models'))
  );

  // You can only add to a library you have actually seen. Uploading over a
  // library that failed to load, or one still arriving, means prepending the
  // new model to a list that is wrong or about to be replaced - and then either
  // the upload disappears when the load lands, or the load is thrown away and
  // everything already in the library disappears instead. Waiting costs one
  // fast GET and removes both.

  async function load(search: string) {
    // Back to loading for the duration, not just on the first call. Without
    // this a reload keeps the page in `ready` while the GET is in flight, which
    // leaves Upload enabled over a library that is being re-read precisely
    // because nobody knows what is in it.
    status = 'loading';
    error = '';
    try {
      // The raw search string rather than openapi-fetch's `params.query`,
      // because it is already exactly the filter the sidebar built and
      // rebuilding it from three optional values here would be a second place
      // for the two to disagree.
      const { data, error: failure } = await api.GET(`/api/models${search}` as '/api/models');
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

  // Re-runs whenever the URL changes, which is the whole of filtering: clicking
  // a category in the sidebar is a navigation, and this is what notices.
  $effect(() => {
    load(query);
  });

  // Re-read rather than prepend. Prepending is right for an unfiltered library,
  // but under a filter the new model may not belong in the list at all - it has
  // no category and no tags yet - and putting it there would show the user a
  // grid that disagrees with its own heading.
  function uploaded(_model: Model, opts?: { keepOpen?: boolean }) {
    load(query);
    library.refresh();
    // A partial upload keeps the dialog open to say what is missing.
    if (!opts?.keepOpen) uploading = false;
  }
</script>

<div class="px-8 py-7">
  <header class="flex items-start justify-between gap-4">
    <div class="min-w-0">
      <p class="text-xs font-medium tracking-wide text-faint uppercase">Library</p>
      <h1 class="mt-1 flex items-center gap-2 text-2xl font-semibold">
        {#if category}
          <span
            class="h-2.5 w-2.5 shrink-0 rounded-xs"
            style="background-color: {category.color}"
            aria-hidden="true"
          ></span>
        {/if}
        <span class="truncate">{heading}</span>
      </h1>
      {#if filtered}
        <a class="mt-1 inline-block text-sm text-muted underline" href="/">Clear filter</a>
      {/if}
    </div>
    <button
      type="button"
      class="rounded bg-accent px-4 py-2 text-sm font-medium text-accent-ink disabled:opacity-50"
      onclick={() => (uploading = true)}
      disabled={status !== 'ready'}
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
    <button
      type="button"
      class="mt-6 rounded border border-line-strong px-3 py-1.5 text-sm"
      onclick={() => load(query)}
    >
      Try again
    </button>
  {:else if models.length === 0 && filtered}
    <!--
      A filter that matches nothing is not an empty library, and offering
      "Upload your first model" here would be wrong twice: the library is not
      empty, and an upload would not show up under this filter anyway.
    -->
    <div class="mt-8 rounded-tile border border-dashed border-line-strong px-6 py-16 text-center">
      <h2 class="text-base font-medium">Nothing matches this filter</h2>
      <p class="mx-auto mt-2 max-w-sm text-sm text-muted">
        No models are filed here yet. Open a model and use Edit to give it a category or a tag.
      </p>
      <a class="mt-6 inline-block rounded border border-line-strong px-3 py-1.5 text-sm" href="/">
        Show all models
      </a>
    </div>
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
  <UploadDialog
    onclose={(opts) => {
      uploading = false;
      // The dialog could not tell whether the upload landed. Re-reading the
      // library is what settles it, and it has to happen before Upload is
      // available again or the user can make a second copy they then have to
      // find and delete by hand.
      if (opts?.reload) {
        load(query);
        library.refresh();
      }
    }}
    onuploaded={uploaded}
  />
{/if}
