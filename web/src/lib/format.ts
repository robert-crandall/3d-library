/**
 * Sizes for humans. The API reports bytes; a tile showing "12841472" is
 * useless.
 *
 * Binary units with decimal-ish labels (KB = 1024) because that is what every
 * file manager the user has ever seen does, and matching them matters more here
 * than matching the SI definition.
 *
 * One decimal place below 10 in a unit, none above: "1.4 MB" and "512 MB" both
 * read cleanly, where "1 MB" loses real information and "512.0 MB" is noise.
 */
const UNITS = ['B', 'KB', 'MB', 'GB', 'TB'];

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—';
  if (bytes < 1024) return `${Math.round(bytes)} B`;

  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value < 10 ? value.toFixed(1) : Math.round(value)} ${UNITS[unit]}`;
}

/** "1 file" / "3 files". Its own function so the tile and the dialog cannot
 *  disagree about the plural. */
export function formatFileCount(count: number): string {
  return `${count} ${count === 1 ? 'file' : 'files'}`;
}
