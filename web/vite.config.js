import { svelte } from '@sveltejs/vite-plugin-svelte'
import { svelteTesting } from '@testing-library/svelte/vite'
import { defineConfig } from 'vite'

// The build goes straight into the Go package that embeds it.
export default defineConfig({
  plugins: [svelte(), svelteTesting()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
  server: {
    // `npm run dev` proxies the API to a running `caddy-view serve`.
    proxy: { '/api': 'http://127.0.0.1:8484' },
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.js'],
    setupFiles: ['src/test-setup.js'],
    restoreMocks: true,
  },
})
