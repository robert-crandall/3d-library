import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
const config = {
  preprocess: vitePreprocess(),
  kit: {
    // index.html rather than SvelteKit's suggested 200.html: the Go server
    // serves the SPA from an embedded FS and looks for index.html at its root
    // (server.New panics without it). The prerendering conflict SvelteKit warns
    // about needs a route prerendered to /index.html, and nothing here
    // prerenders - ssr is off in the root layout and no route opts back in.
    adapter: adapter({ fallback: 'index.html' })
  }
};

export default config;
