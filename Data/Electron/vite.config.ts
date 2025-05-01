////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Electron/vite.config.ts

import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  root: path.resolve(__dirname, 'renderer'),
  build: {
    outDir: path.resolve(__dirname, 'dist/renderer')
  },
  server: {
    port: 5173
  },
  plugins: [react()]
})
