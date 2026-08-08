import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [tailwindcss(), sveltekit()],
  resolve: {
    conditions: ['browser']
  },
  server: {
    port: 5173,
    strictPort: true,
    // In production one binary serves both, so the SPA talks to same-origin
    // paths. In dev, Vite forwards those to the binary on 8080 - which also
    // keeps the session cookie working, since cookies key on host and not port.
    proxy: {
      '/api': 'http://localhost:8080',
      '/healthz': 'http://localhost:8080'
    }
  },
  test: {
    environment: 'jsdom',
    setupFiles: './src/test-setup.ts',
    include: ['src/**/*.test.ts']
  }
});
