import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// `npm run dev:mock` (vite --mode mock) serves a fake API from ./mock for development: every screen
// can be opened in every state (see http://localhost:5173/__mock). The mock is never bundled — it is
// imported only in mock mode and only for the dev server.
export default defineConfig(async ({ mode }) => {
  const plugins = [react(), tailwindcss()]
  if (mode === 'mock') {
    const { default: mockApi } = await import('./mock/mockApi.js')
    plugins.push(mockApi())
  }
  return { plugins, base: '/' }
})
