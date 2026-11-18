import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [
    tailwindcss(),
    sveltekit({
      preprocess: vitePreprocess(),
      // index.html rather than SvelteKit's suggested 200.html: the Go server
      // serves the SPA from an embedded FS and looks for index.html at its root
      // (server.New panics without it). The prerendering conflict SvelteKit warns
      // about needs a route prerendered to /index.html, and nothing here
      // prerenders - ssr is off in the root layout and no route opts back in.
      adapter: adapter({ fallback: 'index.html' }),
      version: { pollInterval: 0 },
      alias: { '$lib': 'src/lib' }
    })
  ],
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
