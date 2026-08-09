<script lang="ts">
  import LibraryError from '$lib/components/LibraryError.svelte';
  import TaxonomySection from '$lib/components/TaxonomySection.svelte';
  import CollectionsSection from '$lib/components/CollectionsSection.svelte';
  import { library } from '$lib/library.svelte';

  // A fixed palette, not a colour picker. The sidebar's dots are 8px, so the
  // only thing a free hex field buys is the chance to pick one that is
  // invisible against the sidebar in one of the two themes. These eight are the
  // design's own swatches, and the server still validates the hex it is sent.
  const palette = [
    '#3b82f6',
    '#8b5cf6',
    '#ec4899',
    '#ef4444',
    '#f59e0b',
    '#10b981',
    '#14b8a6',
    '#64748b'
  ];
</script>

<div class="mx-auto max-w-3xl px-8 py-7">
  <header>
    <p class="text-xs font-medium tracking-wide text-faint uppercase">Settings</p>
    <h1 class="mt-1 text-2xl font-semibold">Collections, categories, tags and materials</h1>
    <p class="mt-2 text-sm text-muted">
      The vocabulary this library is filed under. Every name is yours alone, and every one of these
      deletes is permanent.
    </p>
  </header>

  <LibraryError class="mt-6" />

  <div class="mt-6 flex flex-col gap-5">
    <TaxonomySection
      title="Categories"
      singular="category"
      hint="One per model, like a shelf. Deleting a category leaves its models uncategorized."
      path="/api/categories"
      rows={library.categories}
      colors={palette}
      deleteBody={(row) =>
        row.modelCount === 0
          ? 'This category has no models in it.'
          : `${row.modelCount} ${row.modelCount === 1 ? 'model becomes' : 'models become'} uncategorized. The ${row.modelCount === 1 ? 'model itself is' : 'models themselves are'} not deleted.`}
    />

    <CollectionsSection />

    <TaxonomySection
      title="Tags"
      singular="tag"
      hint="As many per model as you like. Tags show in the sidebar and filter the library."
      path="/api/tags"
      rows={library.tags}
      deleteBody={(row) =>
        row.modelCount === 0
          ? 'This tag is not on any models.'
          : `This tag comes off ${row.modelCount} ${row.modelCount === 1 ? 'model' : 'models'}. The ${row.modelCount === 1 ? 'model is' : 'models are'} not deleted.`}
    />

    <TaxonomySection
      title="Materials"
      singular="material"
      hint="What you printed a model in. Everyone starts with PLA, PETG, ABS, ASA and TPU; add your own or delete the ones you do not use."
      path="/api/materials"
      rows={library.materials}
      deleteBody={(row) =>
        row.modelCount === 0
          ? 'This material is not on any models.'
          : `This material comes off ${row.modelCount} ${row.modelCount === 1 ? 'model' : 'models'}. The ${row.modelCount === 1 ? 'model is' : 'models are'} not deleted.`}
    />
  </div>
</div>
