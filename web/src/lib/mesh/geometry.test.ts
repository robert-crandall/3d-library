import { describe, expect, it } from 'vitest';
import { formatObjectCount } from './geometry';

describe('formatObjectCount', () => {
  it('counts objects in words the design uses', () => {
    expect(formatObjectCount(1)).toBe('1 object');
    expect(formatObjectCount(4)).toBe('4 objects');
  });
});
