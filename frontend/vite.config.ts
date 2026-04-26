import { defineConfig, loadEnv } from 'vite'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig(({ mode }) => {
  // Parsing file ../credentials/.env secara native
  const env = loadEnv(mode, '../credentials', '')

  // Ekstrak informasi dari .env (jika tidak ada akan fallback ke port 5173/8080 local)
  let port = 5173
  let host = '127.0.0.1'
  if (env.FRONTEND_ORIGIN) {
    try {
      const url = new URL(env.FRONTEND_ORIGIN)
      port = parseInt(url.port, 10) || (url.protocol === 'https:' ? 443 : 80)
      host = url.hostname
    } catch(e) {}
  }

  const apiHost = env.API_HOST || '127.0.0.1'
  const apiPort = env.API_PORT || '8080'
  const apiUrl = `http://${apiHost}:${apiPort}`

  return {
    plugins: [tailwindcss()],
    server: {
      port,
      host,
      // ── Dev Proxy: tunnel /api/* ke backend ──────────────────────────────
      // Ini menyatukan FE dan BE dalam satu origin dari perspektif browser.
      // Cookie SameSite=Lax bekerja normal karena tidak ada lagi cross-origin.
      // EventSource (SSE logs) juga ikut di-proxy sehingga withCredentials OK.
      proxy: {
        '/api': {
          target: apiUrl,
          changeOrigin: true,
          // Preserve cookie domain agar session cookie diterima browser
          cookieDomainRewrite: { '*': '' },
        },
      },
    },
    define: {
      // Saat dev: API_BASE = '' (relative path) agar request lewat proxy Vite.
      // Saat production build: tetap pakai apiUrl langsung.
      __API_URL__: JSON.stringify(mode === 'development' ? '' : apiUrl)
    },
    build: {
      outDir: 'dist',
      sourcemap: false,
    },
  }
})
