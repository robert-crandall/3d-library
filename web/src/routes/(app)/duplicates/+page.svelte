<script lang="ts">
  import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
  import { api } from '$lib/api/client';
  import { apiErrorMessage } from '$lib/api/errors';
  import { formatBytes, formatDate } from '$lib/format';
  import { onDestroy } from 'svelte';
  import type { components } from '$lib/api/schema';

  type Duplicates = components['schemas']['Duplicates'];
  type Group = components['schemas']['DuplicateGroup'];
  type DuplicateFile = components['schemas']['DuplicateFile'];

  let found = $state<Duplicates>();
  // Three states, matching the library page: "we asked and there is nothing"
  // and "we could not ask" must not render the same, or a failed load claims a
  // clean library.
  let status = $state<'loading' | 'ready' | 'failed'>('loading');
  let error = $state('');
  let doomed = $state<{ group: Group & { files: DuplicateFile[] }; file: DuplicateFile }>();
  let deleting = $state(false);
  let deleteError = $state('');

  // While a scan runs the page polls. A timer id rather than a loop, so leaving
  // the page stops it: a poll that outlives the component keeps fetching for as
  // long as the tab is open.
  let timer: ReturnType<typeof setTimeout> | undefined;
  // A read in flight when the component goes away still resolves, and without
  // this it would arm a fresh timer over a dead component - a poll that runs
  // for as long as the tab is open.
  let gone = false;
  // "We believe a scan is running", latched. It cannot be read off `found`,
  // because a failed read drops `found` - and a read that failed is no evidence
  // the scan stopped. Without this, two failures in a row end the poll and the
  // page sits on an error until someone reloads it.
  let polling = false;
  // Reads race each other, so each one takes a ticket and only the newest is
  // allowed to write. The library page uses the same counter for the same
  // reason.
  let generation = 0;
  const POLL_MS = 750;

  // openapi-fetch resolves with an `error` for an HTTP error but *rejects* when
  // fetch itself fails - offline, DNS, a dropped connection. Both are the same
  // event to this page, so every call goes through here and nothing below has
  // to know which kind it got. An uncaught rejection would leave the stale
  // answer onscreen and the button spinning forever.
  async function attempt<T extends { error?: unknown }>(
    call: Promise<T>
  ): Promise<T | { data?: undefined; error: unknown }> {
    try {
      return await call;
    } catch (thrown) {
      return { error: thrown };
    }
  }

  /**
   * Retire every read already in flight and cancel any pending poll. Anything
   * that makes the current answer obsolete calls this first, so a response that
   * predates the change cannot land after it.
   */
  function invalidate() {
    generation++;
    clearTimeout(timer);
  }

  async function read() {
    // Reads overlap: a poll, a retry and the read after a delete can all be in
    // flight at once. Without a generation the slowest response wins, which
    // means an older answer - possibly a clean one - can land on top of a newer
    // one and cancel its timer.
    invalidate();
    const mine = generation;
    const { data, error: failed } = await attempt(api.GET('/api/duplicates'));
    if (gone || mine !== generation) return;

    if (failed || !data) {
      // The old answer is dropped, not kept. A stale group list is only
      // misleading, but a stale "no duplicate files" is a claim about a library
      // nobody has looked at since - and it is the claim the user acts on.
      found = undefined;
      status = 'failed';
      error = apiErrorMessage(failed, 'Could not read the duplicate list.');
      // A scan we know is running keeps being polled through a failed read,
      // because giving up leaves the page frozen mid-scan until a reload.
      if (polling) timer = setTimeout(read, POLL_MS);
      return;
    }

    found = data;
    status = 'ready';
    error = '';
    polling = data.status.running;
    if (polling) timer = setTimeout(read, POLL_MS);
  }

  onDestroy(() => {
    gone = true;
    clearTimeout(timer);
  });
  read();

  async function scan() {
    // The old answer stops being true the moment a scan is requested, and the
    // dangerous one - "No duplicate files" - would otherwise sit there through a
    // POST that failed. Dropping it also removes the button, so there is nothing
    // to double-click while the request is in flight. The invalidate matters as
    // much as the drop: a read started before this click is still in flight and
    // would otherwise put its now-obsolete answer back.
    invalidate();
    found = undefined;
    status = 'loading';
    error = '';
    const { error: failed } = await attempt(api.POST('/api/duplicates/scan'));
    if (gone) return;
    if (failed) {
      status = 'failed';
      error = apiErrorMessage(failed, 'Could not start the scan.');
      // Deliberately no second POST behind the retry: a dropped connection says
      // nothing about whether the server started scanning, so the way back is a
      // read that establishes what actually happened.
      return;
    }
    // The scan is running whether or not the read below succeeds, so the latch
    // is set here rather than inferred from a GET that may never land.
    polling = true;
    await read();
  }

  async function remove() {
    if (!doomed) return;
    deleting = true;
    deleteError = '';
    const { error: failed } = await attempt(
      api.DELETE('/api/models/{id}/files/{fileId}', {
        params: { path: { id: doomed.file.modelId, fileId: doomed.file.fileId } }
      })
    );
    if (gone) return;
    deleting = false;
    if (failed) {
      deleteError = apiErrorMessage(failed, 'Could not delete that file.');
      return;
    }
    doomed = undefined;
    // Re-read rather than splice the group locally: a group that drops to one
    // file stops being a group, and the server is what decides that.
    await read();
  }

  // The schema types both lists as nullable, because a Go nil slice is null.
  // Normalising once here keeps every `??` out of the markup below.
  const groups = $derived((found?.groups ?? []).map((g) => ({ ...g, files: g.files ?? [] })));
  const scanState = $derived(found?.status);
  const reclaimable = $derived(groups.reduce((sum, g) => sum + g.reclaimable, 0));

  // "Nothing to find" is only honest when the library is fully hashed. With
  // files still pending - a run that hit an unreadable blob, or uploads since
  // the last scan - an empty list means "not finished looking", which is a
  // different sentence.
  const clean = $derived(
    Boolean(scanState?.scannedAt) &&
      !scanState?.running &&
      scanState?.pending === 0 &&
      groups.length === 0
  );
</script>

<div class="mx-auto max-w-3xl px-8 py-7">
  <header>
    <p class="text-xs font-medium tracking-wide text-faint uppercase">Duplicates</p>
    <h1 class="mt-1 text-2xl font-semibold">The same file, stored twice</h1>
    <p class="mt-2 text-sm text-muted">
      Files are compared by their contents, not their names. Deleting a copy here is permanent, and
      leaves every other copy alone.
    </p>
  </header>

  {#if error}
    <p role="alert" class="mt-6 text-sm text-danger">{error}</p>
  {/if}

  {#if status === 'loading'}
    <p class="mt-6 text-sm text-muted">Loading…</p>
  {:else if scanState}
    <div class="mt-6 flex flex-wrap items-center gap-3 border-b border-line pb-5">
      <button
        type="button"
        class="rounded bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink"
        onclick={scan}
        disabled={scanState.running}
      >
        {scanState.scannedAt ? 'Scan again' : 'Scan for duplicates'}
      </button>

      {#if scanState.running}
        <p class="text-sm text-muted" role="status">
          Reading {scanState.total}
          {scanState.total === 1 ? 'file' : 'files'} · {scanState.hashed} done
        </p>
      {:else if scanState.scannedAt}
        <p class="text-sm text-faint">Scanned through {formatDate(scanState.scannedAt)}</p>
      {/if}
    </div>

    {#if scanState.error}
      <p role="alert" class="mt-5 text-sm text-danger">{scanState.error}</p>
    {/if}

    <!--
      Groups first, before the never-scanned copy. A run that hashed some files
      and then died before recording its watermark leaves exactly that state -
      real duplicates on screen with no timestamp - and ordering it the other
      way would hide them behind "nothing has been compared yet".
    -->
    {#if groups.length > 0}
      <p class="mt-6 text-sm text-muted">
        {groups.length}
        {groups.length === 1 ? 'group' : 'groups'} · {formatBytes(reclaimable)} to reclaim
      </p>

      {#if !scanState.running && scanState.pending > 0}
        <p class="mt-2 text-sm text-faint">
          {scanState.pending}
          {scanState.pending === 1 ? 'file has' : 'files have'} not been read yet, so there may be more
          below this list.
        </p>
      {/if}

      <ul class="mt-4 flex flex-col gap-4">
        {#each groups as group (group.hash)}
          <li class="rounded border border-line bg-surface p-4">
            <p class="text-sm font-medium">
              {group.files.length} copies · {formatBytes(group.size)} each
            </p>
            <p class="mt-0.5 text-xs text-faint">
              {formatBytes(group.reclaimable)} freed by keeping one
            </p>
            <ul class="mt-3 flex flex-col gap-2">
              {#each group.files as file (file.fileId)}
                <li class="flex items-center justify-between gap-3">
                  <span class="min-w-0 text-sm">
                    <a href="/models/{file.modelId}" class="underline">{file.modelName}</a>
                    <span class="text-muted"> · {file.filename}</span>
                  </span>
                  <button
                    type="button"
                    class="shrink-0 rounded border border-line-strong px-2 py-1 text-xs"
                    onclick={() => {
                      doomed = { group, file };
                      deleteError = '';
                    }}
                  >
                    Delete
                  </button>
                </li>
              {/each}
            </ul>
          </li>
        {/each}
      </ul>
    {:else if !scanState.scannedAt && !scanState.running}
      <p class="mt-6 text-sm text-muted">
        Nothing has been compared yet. A scan reads every file whose size matches another one, which
        is the only way to know two of them hold the same bytes.
      </p>
    {:else if clean}
      <p class="mt-6 text-sm text-muted">No duplicate files. Every file in the library is unique.</p>
    {:else if !scanState.running}
      <p class="mt-6 text-sm text-muted">
        {scanState.pending}
        {scanState.pending === 1 ? 'file has' : 'files have'} not been read yet, so there may be duplicates
        this list does not show. Scan again to finish.
      </p>
    {/if}
  {:else}
    <!--
      A failed read drops the last answer, which takes the scan button with it.
      Without something here the only way back is a page reload.
    -->
    <p class="mt-6">
      <button
        type="button"
        class="rounded border border-line-strong px-3 py-1.5 text-sm font-medium"
        onclick={read}
      >
        Try again
      </button>
    </p>
  {/if}
</div>

{#if doomed}
  <ConfirmDialog
    title="Delete this file?"
    body={`${doomed.file.filename} is deleted from ${doomed.file.modelName}. The other ${doomed.group.files.length - 1} ${doomed.group.files.length === 2 ? 'copy stays' : 'copies stay'} where they are. This cannot be undone.`}
    confirm="Delete file"
    busy={deleting}
    error={deleteError}
    onconfirm={remove}
    oncancel={() => (doomed = undefined)}
  />
{/if}
