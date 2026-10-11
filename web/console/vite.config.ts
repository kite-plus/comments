/// <reference types="vitest/config" />
import { fileURLToPath, URL } from 'node:url'
import type { Connect, Plugin } from 'vite'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

const BASE = '/static/console/'
const BACKEND = process.env.COMMENTS_BACKEND ?? 'http://localhost:8081'

// The Go server answers GET /me, /me/*, /console and /console/* with
// dist/index.html. The dev and preview servers do the same here, because
// with a non-root base Vite would otherwise only serve the app under BASE.
function spaRoutes(): Plugin {
  const rewrite: Connect.NextHandleFunction = (req, res, next) => {
    const url = req.url ?? '/'
    const path = url.split('?')[0]
    if (req.method !== 'GET' && req.method !== 'HEAD') return next()
    if (path === '/') {
      res.statusCode = 302
      res.setHeader('Location', '/me')
      return res.end()
    }
    if (/^\/(me|console)(\/|$)/.test(path)) req.url = `${BASE}index.html`
    next()
  }
  return {
    name: 'comments-spa-routes',
    configureServer(server) {
      server.middlewares.use(rewrite)
    },
    configurePreviewServer(server) {
      server.middlewares.use(rewrite)
    },
  }
}

export default defineConfig(({ mode }) => {
  const backend = mode === 'backend'
  const proxy = backend
    ? Object.fromEntries(
        ['/api', '/auth', '/avatars'].map((prefix) => [
          prefix,
          { target: BACKEND, changeOrigin: false },
        ])
      )
    : undefined

  return {
    base: BASE,
    plugins: [react(), tailwindcss(), spaRoutes()],
    define: {
      // Mocks are on in plain `pnpm dev`; builds and `--mode backend` drop
      // them entirely. Tests install the fake transport themselves.
      __MOCK__: JSON.stringify(mode === 'development'),
    },
    resolve: {
      alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
    },
    server: { port: 5180, strictPort: true, proxy },
    preview: { port: 5181, strictPort: true, proxy },
    build: {
      outDir: 'dist',
      emptyOutDir: true,
      sourcemap: false,
      chunkSizeWarningLimit: 400,
    },
    test: {
      environment: 'jsdom',
      globals: false,
      setupFiles: ['./src/test/setup.ts'],
      css: false,
      restoreMocks: true,
      testTimeout: 20_000,
    },
  }
})
