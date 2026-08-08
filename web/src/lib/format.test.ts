import { describe, expect, it } from 'vitest';
import { formatBytes, formatFileCount } from './format';

describe('formatBytes', () => {
  it('leaves bytes alone below a kilobyte', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(1023)).toBe('1023 B');
  });

  // The boundary is where an off-by-one in the loop shows up.
  it('switches unit exactly at 1024', () => {
    expect(formatBytes(1024)).toBe('1.0 KB');
    expect(formatBytes(1024 * 1024)).toBe('1.0 MB');
    expect(formatBytes(1024 * 1024 * 1024)).toBe('1.0 GB');
  });

  it('keeps one decimal below ten and drops it above', () => {
    expect(formatBytes(1536)).toBe('1.5 KB');
    expect(formatBytes(1024 * 12)).toBe('12 KB');
    expect(formatBytes(500 * 1024 * 1024)).toBe('500 MB');
  });

  // Sizes come off the wire. A malformed one should not render "NaN undefined"
  // in the middle of the grid.
  it('refuses to render nonsense', () => {
    expect(formatBytes(NaN)).toBe('—');
    expect(formatBytes(-1)).toBe('—');
  });

  it('stops at the largest unit it knows', () => {
    expect(formatBytes(1024 ** 5)).toBe('1024 TB');
  });
});

describe('formatFileCount', () => {
  it('gets the plural right', () => {
    expect(formatFileCount(0)).toBe('0 files');
    expect(formatFileCount(1)).toBe('1 file');
    expect(formatFileCount(2)).toBe('2 files');
  });
});
