// This is a client-rendered SPA served from a Go binary: there is no Node
// runtime next to it to render on. Turning SSR off here is what makes
// adapter-static's fallback page the entry point for every route.
export const ssr = false;
