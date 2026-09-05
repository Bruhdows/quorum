import { defineConfig } from 'astro/config';

// Static build. The Go hub serves the output directly, no SSR needed.
export default defineConfig({
  output: 'static',
});
