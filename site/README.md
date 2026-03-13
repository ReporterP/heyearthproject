# Vue 3 + Vite

Requires **Node.js 20+** (Tailwind 4). In WSL: `nvm install 20 && nvm use 20` or install Node 20.

This template should help you get started developing with Vue 3 in Vite. The template uses Vue 3 `<script setup>` SFCs, check out the [script setup docs](https://v3.vuejs.org/api/sfc-script-setup.html#sfc-script-setup) to learn more.

Learn more about IDE Support for Vue in the [Vue Docs Scaling up Guide](https://vuejs.org/guide/scaling-up/tooling.html#ide-support).

## Project on WSL (Windows)

If the repo lives under WSL (e.g. `\\wsl.localhost\Ubuntu-24.04\home\...`) and you open it from Windows, **do not run `npm install` in the Windows terminal** — UNC paths break postinstall scripts (e.g. esbuild). Use one of:

- **Reopen in WSL:** In Cursor/VS Code use **File → Reopen Folder in WSL** (or the “Open in WSL” link in the bottom-left). Then the integrated terminal will be Linux; run `npm install` there.
- **Or** open a real WSL shell (Ubuntu app or `wsl` in CMD), then:
  ```bash
  cd /home/alexadmin/heyearthproject/site
  rm -rf node_modules package-lock.json
  npm install
  ```
