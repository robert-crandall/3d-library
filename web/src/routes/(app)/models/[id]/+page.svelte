<script lang="ts">
  import { goto } from '$app/navigation';
  import { api } from '$lib/api/client';
  import { apiErrorMessage } from '$lib/api/errors';
  import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
  import EditModelDialog from '$lib/components/EditModelDialog.svelte';
  import UploadDialog from '$lib/components/UploadDialog.svelte';
  import { formatBytes, formatDate, formatFileCount } from '$lib/format';
  import type { ModelDetail, ModelFile } from '$lib/upload';

  let { data }: { data: { id: number } } = $props();

  // Four states rather than three. A model that does not exist is not a model
  // that failed to load: the library page's `failed` offers Try again, and
  // retrying a 404 is a button that can only ever fail again. `missing` says so
  // and offers the way back instead.
  let status = $state<'loading' | 'ready' | 'failed' | 'missing'>('loading');
  let model = $state<ModelDetail>();
  let error = $state('');

  let editing = $state(false);
  let adding = $state(false);
  let deletingModel = $state(false);
  let deletingFile = $state<ModelFile>();
  // Shown inside whichever dialog is open. Separate from `error`, which belongs
  // to the page: a refused edit does not mean the model on screen is wrong.
  let busy = $state(false);
  let dialogError = $state('');

  /**
   * Read the model. Also the "it worked, now show me what is there" path after
   * a mutation, which is why every caller of that kind closes its dialog
   * *before* awaiting this: a re-read that fails must leave the page saying so,
   * never a dialog still offering to do the write a second time.
   */
  async function load() {
    status = 'loading';
    error = '';
    try {
      const { data: body, error: failure, response } = await api.GET('/api/models/{id}', {
        params: { path: { id: data.id } }
      });
      if (failure) {
        if (response.status === 404) {
          status = 'missing';
          return;
        }
        error = apiErrorMessage(failure, 'Could not load this model.');
        status = 'failed';
        return;
      }
      model = body;
      status = 'ready';
    } catch {
      // openapi-fetch lets a fetch-level rejection through, so without this the
      // page would sit on "Loading…" forever.
      error = 'Could not reach the server.';
      status = 'failed';
    }
  }

  load();

  async function save(edits: {
    name: string;
    description: string;
    printTips: string;
    sourceUrl: string;
  }) {
    busy = true;
    dialogError = '';
    try {
      const { data: body, error: failure } = await api.PUT('/api/models/{id}', {
        params: { path: { id: data.id } },
        body: edits
      });
      if (failure) {
        dialogError = apiErrorMessage(failure, 'Could not save.');
        return;
      }
      // The response is the saved model, so there is nothing a second GET would
      // add except a chance for the two to disagree.
      model = body;
      editing = false;
    } catch {
      dialogError = 'Could not reach the server.';
    } finally {
      busy = false;
    }
  }

  async function deleteFile(file: ModelFile) {
    busy = true;
    dialogError = '';
    try {
      const { error: failure } = await api.DELETE('/api/models/{id}/files/{fileId}', {
        params: { path: { id: data.id, fileId: file.id } }
      });
      if (failure) {
        dialogError = apiErrorMessage(failure, 'Could not delete that file.');
        return;
      }
      // The file is gone, so the dialog's work is done and it closes before the
      // re-read. What the re-read then reports is the page's problem, not the
      // dialog's: `load` puts a failure in the page's own state, where the way
      // out is Try again, rather than in a dialog still offering to delete
      // something that no longer exists.
      deletingFile = undefined;
      await load();
    } catch {
      dialogError = 'Could not reach the server.';
    } finally {
      busy = false;
    }
  }

  async function deleteModel() {
    busy = true;
    dialogError = '';
    try {
      const { error: failure } = await api.DELETE('/api/models/{id}', {
        params: { path: { id: data.id } }
      });
      if (failure) {
        dialogError = apiErrorMessage(failure, 'Could not delete this model.');
        return;
      }
      // Nothing left to show. The library re-reads on mount, so it will not be
      // in the grid either.
      await goto('/');
    } catch {
      dialogError = 'Could not reach the server.';
    } finally {
      busy = false;
    }
  }
</script>

<!--
  Screen 1c of the design, minus the parts no milestone has built yet: the 3D
  viewer, slice settings, versions, category and tags. Those are omitted, not
  rendered empty - a panel headed "Slice settings" with nothing in it reads as
  broken, where an absent panel reads as a feature that is not here yet.

  "Open in slicer" is on the design and is deliberately not built; the epic cut
  it from v1.
-->
<div class="px-8 py-7">
  {#if status === 'loading'}
    <p class="text-sm text-muted">Loading…</p>
  {:else if status === 'missing'}
    <h1 class="text-2xl font-semibold">Model not found</h1>
    <p class="mt-2 text-sm text-muted">It may have been deleted.</p>
    <a class="mt-6 inline-block rounded border border-line-strong px-3 py-1.5 text-sm" href="/">
      Back to the library
    </a>
  {:else if status === 'failed'}
    <p role="alert" class="text-sm text-danger">{error}</p>
    <button
      type="button"
      class="mt-6 rounded border border-line-strong px-3 py-1.5 text-sm"
      onclick={load}
    >
      Try again
    </button>
  {:else if model}
    <nav class="flex items-center gap-2 text-sm text-muted" aria-label="Breadcrumb">
      <a href="/">Library</a>
      <span aria-hidden="true">/</span>
      <span class="text-ink">{model.name}</span>
    </nav>

    <header class="mt-3.5 flex items-start gap-4">
      <div class="min-w-0">
        <h1 class="text-2xl font-semibold">{model.name}</h1>
        <p class="mt-1.5 flex flex-wrap items-center gap-2 text-sm text-muted">
          <span>{formatFileCount(model.fileCount)} · {formatBytes(model.totalSize)}</span>
          <span aria-hidden="true">·</span>
          <span>Added {formatDate(model.createdAt)}</span>
          {#if model.sourceUrl}
            <span aria-hidden="true">·</span>
            <!-- Off-site and user-supplied, so it opens away from the app and
                 carries no referrer or opener. -->
            <a href={model.sourceUrl} target="_blank" rel="noreferrer" class="truncate">
              {model.sourceUrl}
            </a>
          {/if}
        </p>
      </div>
      <div class="ml-auto flex shrink-0 gap-2">
        <button
          type="button"
          class="rounded border border-line-strong px-3 py-1.5 text-sm"
          onclick={() => ((dialogError = ''), (editing = true))}
        >
          Edit
        </button>
        <button
          type="button"
          class="rounded border border-line-strong px-3 py-1.5 text-sm"
          onclick={() => ((dialogError = ''), (adding = true))}
        >
          Add files
        </button>
        <button
          type="button"
          class="rounded border border-line-strong px-3 py-1.5 text-sm text-danger"
          onclick={() => ((dialogError = ''), (deletingModel = true))}
        >
          Delete
        </button>
      </div>
    </header>

    <div class="mt-6 grid grid-cols-1 items-start gap-5 lg:grid-cols-[1fr_360px]">
      <div class="flex flex-col gap-5">
        <!--
          Screen 1c's viewer, drawn as the placeholder the design itself draws:
          a hatched panel with nothing in it. It is here rather than omitted -
          unlike Slice settings and Versions - because it is the largest element
          on the screen, and a detail page without it is a different layout, not
          the same layout missing a panel. It is inert and aria-hidden: there is
          nothing to read out and nothing to operate until M5 puts a mesh in it.
        -->
        <div
          aria-hidden="true"
          class="viewer-hatch flex h-64 items-center justify-center rounded-tile border border-line"
        >
          <span class="text-xs text-faint">3D preview</span>
        </div>

        <section class="rounded-tile border border-line bg-surface">
        <div class="flex items-baseline justify-between border-b border-line px-4 py-3">
          <h2 class="text-sm font-semibold">Files</h2>
          <span class="text-xs text-muted">
            {formatFileCount(model.fileCount)} · {formatBytes(model.totalSize)}
          </span>
        </div>

        {#if model.files.length === 0}
          <!--
            Reachable: deleting the last file leaves the model, by design - the
            epic keeps them separate so a mis-uploaded file can be replaced
            without losing the description that goes with it.
          -->
          <div class="px-4 py-10 text-center">
            <p class="text-sm text-muted">This model has no files.</p>
            <button
              type="button"
              class="mt-4 rounded bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink"
              onclick={() => ((dialogError = ''), (adding = true))}
            >
              Add files
            </button>
          </div>
        {:else}
          <table class="w-full text-sm">
            <thead>
              <tr class="text-xs text-faint">
                <th scope="col" class="px-4 py-2 text-left font-medium">Name</th>
                <th scope="col" class="px-4 py-2 text-left font-medium">Type</th>
                <th scope="col" class="px-4 py-2 text-right font-medium">Size</th>
                <th scope="col" class="px-4 py-2 text-left font-medium">Added</th>
                <th scope="col" class="px-4 py-2 text-right font-medium">
                  <span class="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {#each model.files as file (file.id)}
                <tr class="border-t border-line">
                  <td class="max-w-0 px-4 py-2.5">
                    <!--
                      A plain link to the download endpoint, not a fetch. The
                      browser already knows how to stream a 500 MB response to
                      disk with a progress bar and a resume; reading it into a
                      Blob to hand back to the same browser would only be a way
                      to run out of memory. The session cookie rides along.
                    -->
                    <a
                      class="block truncate"
                      href="/api/models/{model.id}/files/{file.id}"
                      download={file.filename}
                      title={file.filename}
                    >
                      {file.filename}
                    </a>
                  </td>
                  <td class="px-4 py-2.5 text-muted">{file.type}</td>
                  <td class="px-4 py-2.5 text-right text-muted">{formatBytes(file.size)}</td>
                  <td class="px-4 py-2.5 text-muted">{formatDate(file.createdAt)}</td>
                  <td class="px-4 py-2.5 text-right">
                    <button
                      type="button"
                      class="text-xs text-danger"
                      aria-label="Delete {file.filename}"
                      onclick={() => ((dialogError = ''), (deletingFile = file))}
                    >
                      Delete
                    </button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      </section>
      </div>

      <div class="flex flex-col gap-5">
        {#if model.description}
          <section class="rounded-tile border border-line bg-surface px-4 py-3">
            <h2 class="text-sm font-semibold">Description</h2>
            <!-- whitespace-pre-line, not a markdown renderer: the field is a
                 plain textarea, and the only formatting anyone can put in it is
                 the line breaks they typed. -->
            <p class="mt-2 text-sm whitespace-pre-line text-muted">{model.description}</p>
          </section>
        {/if}

        {#if model.printTips}
          <section class="rounded-tile border border-line bg-surface px-4 py-3">
            <h2 class="text-sm font-semibold">Print tips</h2>
            <!-- One tip per line, as design 1c draws them. The column is plain
                 text and the editor is a textarea, so the newline the user
                 typed is the only separator there is. -->
            <ul class="mt-2 list-disc space-y-1 pl-4 text-sm text-muted">
              {#each model.printTips.split('\n').filter((tip) => tip.trim() !== '') as tip}
                <li>{tip}</li>
              {/each}
            </ul>
          </section>
        {/if}
      </div>
    </div>
  {/if}
</div>

{#if model && editing}
  <EditModelDialog
    {model}
    {busy}
    error={dialogError}
    onsave={save}
    oncancel={() => (editing = false)}
  />
{/if}

{#if model && adding}
  <UploadDialog
    model={{ id: model.id, fileCount: model.fileCount }}
    onclose={(opts) => {
      adding = false;
      // Always true from the add-files flow: files may have landed before the
      // user cancelled, and the count on screen is the one thing this page must
      // not get wrong.
      if (opts?.reload) load();
    }}
  />
{/if}

{#if deletingFile}
  <ConfirmDialog
    title="Delete file"
    body="{deletingFile.filename} will be deleted. This cannot be undone."
    confirm="Delete file"
    {busy}
    error={dialogError}
    onconfirm={() => deletingFile && deleteFile(deletingFile)}
    oncancel={() => (deletingFile = undefined)}
  />
{/if}

{#if model && deletingModel}
  <ConfirmDialog
    title="Delete model"
    body="{model.name} and its {formatFileCount(model.fileCount)} will be deleted. This cannot be undone."
    confirm="Delete model"
    {busy}
    error={dialogError}
    onconfirm={deleteModel}
    oncancel={() => (deletingModel = false)}
  />
{/if}
