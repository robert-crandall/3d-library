import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import Thumbnail from './Thumbnail.svelte';

/*
  What a weaker version of this file would miss: rendering an <img> when there
  is a src, and a placeholder when there is not, is the obvious half. The half
  that actually breaks in use is the error state - a component that hides the
  broken image but never re-arms leaves a permanently blank tile after the user
  pins a different file, and only the last test here would catch that.
*/
describe('Thumbnail', () => {
  it('renders the image when there is a source', () => {
    render(Thumbnail, { src: '/api/models/1/files/2/thumbnail', alt: 'Preview of a.png' });
    const img = screen.getByAltText('Preview of a.png') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('/api/models/1/files/2/thumbnail');
    // Lazy because a grid page draws a screen's worth of tiles and then some;
    // eager loading would fetch every PNG in the library on first paint.
    expect(img.getAttribute('loading')).toBe('lazy');
    expect(screen.queryByTestId('thumbnail-placeholder')).toBeNull();
  });

  it('renders the placeholder when there is no source', () => {
    render(Thumbnail, { src: null });
    expect(screen.getByTestId('thumbnail-placeholder')).toBeTruthy();
    expect(document.querySelector('img')).toBeNull();
  });

  it('falls back to the placeholder when the image fails to load', async () => {
    render(Thumbnail, { src: '/api/models/1/files/2/thumbnail', alt: 'Preview' });
    await fireEvent.error(screen.getByAltText('Preview'));
    expect(screen.getByTestId('thumbnail-placeholder')).toBeTruthy();
    expect(document.querySelector('img')).toBeNull();
  });

  it('tries again when the source changes after a failure', async () => {
    // The regression this pins: pin a different file after one failed to load
    // and the tile stays blank forever, because `failed` was never reset.
    const { rerender } = render(Thumbnail, {
      src: '/api/models/1/files/2/thumbnail',
      alt: 'Preview'
    });
    await fireEvent.error(screen.getByAltText('Preview'));
    expect(screen.getByTestId('thumbnail-placeholder')).toBeTruthy();

    await rerender({ src: '/api/models/1/files/9/thumbnail', alt: 'Preview' });
    const img = screen.getByAltText('Preview') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('/api/models/1/files/9/thumbnail');
  });
});
