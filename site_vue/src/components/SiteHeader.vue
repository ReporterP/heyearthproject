<script setup>
import { ref, computed } from 'vue'
import { RouterLink } from 'vue-router'

const mobileOpen = ref(false)

const isLoggedIn = computed(() => !!localStorage.getItem('token'))
</script>

<template>
  <header class="sticky top-0 z-50 bg-surface/80 backdrop-blur-xl border-b border-white/5">
    <div class="max-w-6xl mx-auto px-6 h-16 flex items-center justify-between">
      <RouterLink to="/" class="text-xl font-bold tracking-tight">
        <span class="text-brand-light">Hey</span><span>Earth</span>
      </RouterLink>

      <nav class="hidden md:flex items-center gap-8 text-sm text-text-secondary">
        <RouterLink to="/" class="hover:text-text-primary transition">Для пользователей</RouterLink>
        <RouterLink to="/business" class="hover:text-text-primary transition">Для бизнеса</RouterLink>
      </nav>

      <div class="hidden md:flex items-center gap-3">
        <RouterLink
          v-if="isLoggedIn"
          to="/dashboard"
          class="px-4 py-2 rounded-lg bg-brand hover:bg-brand-dark text-white text-sm font-medium transition"
        >
          Кабинет
        </RouterLink>
        <RouterLink
          v-else
          to="/login"
          class="px-4 py-2 rounded-lg bg-brand hover:bg-brand-dark text-white text-sm font-medium transition"
        >
          Войти
        </RouterLink>
      </div>

      <button
        class="md:hidden p-2 text-text-secondary hover:text-text-primary"
        @click="mobileOpen = !mobileOpen"
      >
        <svg xmlns="http://www.w3.org/2000/svg" class="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path v-if="!mobileOpen" stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16" />
          <path v-else stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
        </svg>
      </button>
    </div>

    <div v-if="mobileOpen" class="md:hidden border-t border-white/5 bg-surface px-6 py-4 space-y-3">
      <RouterLink to="/" class="block text-text-secondary hover:text-text-primary" @click="mobileOpen = false">Для пользователей</RouterLink>
      <RouterLink to="/business" class="block text-text-secondary hover:text-text-primary" @click="mobileOpen = false">Для бизнеса</RouterLink>
      <RouterLink
        v-if="isLoggedIn"
        to="/dashboard"
        class="block px-4 py-2 rounded-lg bg-brand text-white text-center text-sm font-medium"
        @click="mobileOpen = false"
      >
        Кабинет
      </RouterLink>
      <RouterLink
        v-else
        to="/login"
        class="block px-4 py-2 rounded-lg bg-brand text-white text-center text-sm font-medium"
        @click="mobileOpen = false"
      >
        Войти
      </RouterLink>
    </div>
  </header>
</template>
