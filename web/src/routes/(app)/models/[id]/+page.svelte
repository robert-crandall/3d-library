<script lang="ts">
  import { goto } from '$app/navigation';
  import { api } from '$lib/api/client';
  import { apiErrorMessage } from '$lib/api/errors';
  import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
  import EditModelDialog from '$lib/components/EditModelDialog.svelte';
  import UploadDialog from '$lib/components/UploadDialog.svelte';
  import SliceSettings from '$lib/components/SliceSettings.svelte';
  import FilePreviewPanel from '$lib/components/FilePreviewPanel.svelte';
  import { hasPreview } from '$lib/preview';
  import Thumbnail from '$lib/components/Thumbnail.svelte';
  import CategoryBadge from '$lib/components/CategoryBadge.svelte';
  import LabelChips from '$lib/components/LabelChips.svelte';
  import { library } from '$lib/library.svelte';
  import { formatBytes, formatDate, formatFileCount } from '$lib/format';
  import { sliceRows } from '$lib/slice';
  import type { ModelDetail, ModelFile } from '$lib/upload';

  let { data }: { data: { id: number } } = $props();

  // Four states rather than three. A model that does not exist is not a model
  // that failed to load: the library page's `failed` offers Try again, and
  // retrying a 404 is a button that can only ever fail again. `missing` says so
  // and offers the way back instead.
  let status = $state<'loading' | 'ready' | 'failed' | 'missing'>('loading');
  let model = $state<ModelDetail>();
  let error = $state('');

  // The first G-code file with settings we could read, in upload order, so
  // that adding another file never moves the panel to a different one. A
  // model with several plates is common and they are all sliced the same way;
  // picking one and saying which is more useful than a per-file selector
  // nobody asked for.
  const sliced = $derived(
    model?.files.find((file) => file.extractedMeta && sliceRows(file.extractedMeta).length > 0)
  );

  let editing = $state(false);
  let adding = $state(false);
  let deletingModel = $state(false);
  let deletingFile = $state<ModelFile>();
  // Shown inside whichever dialog is open. Separate from `error`, which belongs
  // to the page: a refused edit does not mean the model on screen is wrong.
  let busy = $state(false);
  let dialogError = $state('');
  // Separate from `busy`, which belongs to the dialogs. Pinning happens in a
  // table row with no dialog around it, and it must not disable the dialog
  // buttons while it runs.
  let pinning = $state(false);

  // Every mutation on this page is a button, so disabling them all while any
  // one is in flight is the whole of the concurrency story. Without it a pin
  // and a delete overlap, and whichever response lands second wins: pin-then-
  // delete leaves the page showing a file the server no longer has.
  const mutating = $derived(pinning || busy);

  // The viewer is a section like the description and the source link: absent when there
  // is nothing to put in it, rather than present and empty. Deciding it here is also
  // what keeps three.js off a model with nothing to draw - the viewers import it on
  // mount, so an always-present panel would fetch 130 KB to tell a model of photographs
  // there is nothing to show.
  const previewable = $derived(model ? hasPreview(model.files) : false);
  // Its own message rather than the page's `error`, which is only rendered by
  // the `failed` branch: a refused pin must not replace a model that loaded
  // fine with an error screen.
  let pinError = $state('');

  /**
   * Read the model. Also the "it worked, now show me what is there" path after
   * a mutation, which is why every caller of that kind closes its dialog
   * *before* awaiting this: a re-read that fails must leave the page saying so,
   * never a dialog still offering to do the write a second time.
   */
  async function load() {
    status = 'loading';
    error = '';
    // A pin failure is about the model as it was; a re-read replaces that, so
    // leaving the banner up would attach it to rows it never described.
    pinError = '';
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
    categoryId: number | null;
    tagIds: number[];
    materialIds: number[];
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
      // The sidebar's counts moved: this model just joined or left a category
      // and some tags. Nothing else on the page can work that out, and a
      // sidebar that still says 12 after the twelfth model left is worse than
      // one extra GET.
      library.refresh();
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

  /**
   * Pin a file as the model's thumbnail, or pass null to go back to letting the
   * server pick. One call for both because they are the same write - the field
   * is nullable and clearing it is what "automatic" means.
   *
   * The error goes in the page's `error`, not a dialog's: this is a button in a
   * table row with no dialog to put it in, and the row is still on screen.
   */
  async function pinThumbnail(fileId: number | null) {
    pinning = true;
    pinError = '';
    try {
      const { data: body, error: failure } = await api.PUT('/api/models/{id}/thumbnail', {
        params: { path: { id: data.id } },
        body: { fileId }
      });
      if (failure) {
        pinError = apiErrorMessage(failure, 'Could not change the thumbnail.');
        return;
      }
      // The response is the resolved model, which matters here: pass null and
      // the server answers with whichever file its own precedence rule chose,
      // so the page shows the real outcome rather than guessing at it.
      model = body;
    } catch {
      pinError = 'Could not reach the server.';
    } finally {
      pinning = false;
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
  Screen 1c of the design, minus the parts no milestone has built yet: versions,
  category and tags. Those are omitted, not rendered empty - a panel with nothing
  in it reads as broken, where an absent panel reads as a feature that is not here
  yet.

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
      {#if model.category}
        <!-- A link back to the filtered grid, which is the trail the design's
             breadcrumb implies: it is the place this model came from. -->
        <a href="/?categoryId={model.category.id}">{model.category.name}</a>
        <span aria-hidden="true">/</span>
      {/if}
      <span class="truncate text-ink">{model.name}</span>
    </nav>

    <header class="mt-3.5 flex items-start gap-4">
      <div class="min-w-0">
        <h1 class="text-2xl font-semibold">{model.name}</h1>
        <p class="mt-1.5 flex flex-wrap items-center gap-2 text-sm text-muted">
          <CategoryBadge category={model.category} />
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
          disabled={mutating}
          onclick={() => ((dialogError = ''), (editing = true))}
        >
          Edit
        </button>
        <button
          type="button"
          class="rounded border border-line-strong px-3 py-1.5 text-sm"
          disabled={mutating}
          onclick={() => ((dialogError = ''), (adding = true))}
        >
          Add files
        </button>
        <button
          type="button"
          class="rounded border border-line-strong px-3 py-1.5 text-sm text-danger"
          disabled={mutating}
          onclick={() => ((dialogError = ''), (deletingModel = true))}
        >
          Delete
        </button>
      </div>
    </header>

    <div class="mt-6 grid grid-cols-1 items-start gap-5 lg:grid-cols-[1fr_360px]">
      <div class="flex flex-col gap-5">
        {#if previewable}
          <FilePreviewPanel modelId={model.id} files={model.files} />
        {/if}

        <section class="rounded-tile border border-line bg-surface">
        <div class="flex items-baseline justify-between border-b border-line px-4 py-3">
          <h2 class="text-sm font-semibold">Files</h2>
          <span class="text-xs text-muted">
            {formatFileCount(model.fileCount)} · {formatBytes(model.totalSize)}
          </span>
        </div>

        {#if pinError}
          <p role="alert" class="border-b border-line px-4 py-2 text-sm text-danger">{pinError}</p>
        {/if}

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
              disabled={mutating}
              onclick={() => ((dialogError = ''), (adding = true))}
            >
              Add files
            </button>
          </div>
        {:else}
          <table class="w-full text-sm">
            <thead>
              <tr class="text-xs text-faint">
                <th scope="col" class="px-4 py-2 text-left font-medium">
                  <span class="sr-only">Thumbnail</span>
                </th>
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
                  <td class="py-2.5 pr-0 pl-4">
                    {#if file.hasThumbnail}
                      <div class="h-10 w-10 overflow-hidden rounded border border-line">
                        <Thumbnail
                          src="/api/models/{model.id}/files/{file.id}/thumbnail"
                          alt="Preview of {file.filename}"
                        />
                      </div>
                    {:else}
                      <!-- A fixed-size empty box, not an omitted cell: without
                           it the rows below a thumbnail-less file jump left. -->
                      <div class="h-10 w-10" aria-hidden="true"></div>
                    {/if}
                  </td>
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
                    {#if file.id === model.thumbnailFileId}
                      <!-- The badge says which file the picture comes from, and
                           whether the user chose it. "Thumbnail (automatic)" is
                           the difference between "this is what you picked" and
                           "this is what we picked, and it will change if you
                           add a better file". -->
                      <span class="mt-0.5 block text-xs text-muted">
                        Thumbnail{model.thumbnailAutomatic ? ' (automatic)' : ''}
                      </span>
                    {/if}
                  </td>
                  <td class="px-4 py-2.5 text-muted">{file.type}</td>
                  <td class="px-4 py-2.5 text-right text-muted">{formatBytes(file.size)}</td>
                  <td class="px-4 py-2.5 text-muted">{formatDate(file.createdAt)}</td>
                  <td class="px-4 py-2.5 text-right">
                    {#if file.hasThumbnail && file.id !== model.thumbnailFileId}
                      <button
                        type="button"
                        class="mr-3 text-xs"
                        disabled={mutating}
                        onclick={() => pinThumbnail(file.id)}
                      >
                        Use as thumbnail
                      </button>
                    {:else if file.id === model.thumbnailFileId && !model.thumbnailAutomatic}
                      <!-- Only offered on the pinned row, and only when the pin
                           is doing something. On an automatic pick there is
                           nothing to undo. -->
                      <button
                        type="button"
                        class="mr-3 text-xs"
                        disabled={mutating}
                        onclick={() => pinThumbnail(null)}
                      >
                        Use automatic
                      </button>
                    {/if}
                    <button
                      type="button"
                      class="text-xs text-danger"
                      aria-label="Delete {file.filename}"
                      disabled={mutating}
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
        {#if model.tags.length > 0 || model.materials.length > 0}
          <!-- One panel, not two: with two or three chips each, a heading per
               list costs more vertical space than the chips do, and the design
               draws them together under the model's own metadata. -->
          <section class="rounded-tile border border-line bg-surface px-4 py-3">
            {#if model.materials.length > 0}
              <h2 class="text-sm font-semibold">Materials</h2>
              <div class="mt-2"><LabelChips labels={model.materials} /></div>
            {/if}
            {#if model.tags.length > 0}
              <h2 class="text-sm font-semibold" class:mt-3={model.materials.length > 0}>Tags</h2>
              <!-- Linked, unlike the materials: the sidebar filters by tag, so
                   a tag chip has somewhere to go and a material chip does not.
                   Material filtering is a follow-up, not a gap here. -->
              <div class="mt-2"><LabelChips labels={model.tags} href={(t) => `/?tagId=${t.id}`} /></div>
            {/if}
          </section>
        {/if}

        {#if sliced?.extractedMeta}
          <SliceSettings meta={sliced.extractedMeta} filename={sliced.filename} />
        {/if}

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
              {#each model.printTips.split('\n').map((tip) => tip.trim()).filter(Boolean) as tip}
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
      // The dialog decides: false when nothing was sent, true when files landed
      // and the count on screen is now behind. Reading it rather than assuming
      // it is what keeps a cancel from costing a request.
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

