import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import ModelTile from './ModelTile.svelte';
import type { Model } from '$lib/upload';

const base: Model = {
  id: 42,
  name: 'Filament Dry Box',
  fileCount: 3,
  totalSize: 1024,
  createdAt: '2026-03-12T09:00:00Z'
};

/*
  What a weaker version of this file would miss: asserting only that an <img>
  appears would pass against a tile that built the URL from the first file, or
  from the model id, rather than from the id the server resolved. The URL
  assertion is the whole point - the precedence rule lives in Go, and the tile's
  only job is not to second-guess it.
*/
describe('ModelTile', () => {
  it('points the image at the file the server chose', () => {
    render(ModelTile, { model: { ...base, thumbnailFileId: 907 } });
    const img = document.querySelector('img') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('/api/models/42/files/907/thumbnail');
  });

  it('keeps the placeholder for a model with no thumbnail', () => {
    render(ModelTile, { model: base });
    expect(screen.getByTestId('thumbnail-placeholder')).toBeTruthy();
    expect(document.querySelector('img')).toBeNull();
  });
});
