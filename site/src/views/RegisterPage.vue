<script setup>
import { ref } from 'vue'
import { useRouter, RouterLink } from 'vue-router'
import { registerUser } from '../api/user.js'

const router = useRouter()

const loading = ref(false)
const error = ref('')
const generatedId = ref('')
const copied = ref(false)

async function handleRegister() {
  loading.value = true
  error.value = ''
  try {
    const data = await registerUser('web', '')
    generatedId.value = data.id || data.user_id || data.heyearth_id || ''
    if (data.token) {
      localStorage.setItem('token', data.token)
    }
  } catch (e) {
    error.value = e.message || 'Ошибка регистрации. Попробуйте позже.'
  } finally {
    loading.value = false
  }
}

async function copyId() {
  try {
    await navigator.clipboard.writeText(generatedId.value)
    copied.value = true
    setTimeout(() => (copied.value = false), 2000)
  } catch {
    /* clipboard may be unavailable */
  }
}

function goToDashboard() {
  router.push('/dashboard')
}
</script>

<template>
  <section class="py-24">
    <div class="max-w-md mx-auto px-6">
      <!-- Registration form -->
      <template v-if="!generatedId">
        <h1 class="text-3xl font-bold text-center mb-2">Регистрация</h1>
        <p class="text-text-secondary text-center mb-10">
          Получите свой уникальный HeYearth ID за один клик
        </p>

        <p v-if="error" class="text-red-400 text-sm text-center mb-4">{{ error }}</p>

        <button
          :disabled="loading"
          class="w-full py-4 rounded-xl bg-brand hover:bg-brand-dark text-white text-lg font-bold transition shadow-lg shadow-brand/25 disabled:opacity-50"
          @click="handleRegister"
        >
          {{ loading ? 'Создание...' : 'Получить HeYearth ID' }}
        </button>

        <p class="text-center text-sm text-text-secondary mt-8">
          Уже есть аккаунт?
          <RouterLink to="/login" class="text-brand-light hover:text-brand transition">Войти</RouterLink>
        </p>
      </template>

      <!-- Success: show generated ID -->
      <template v-else>
        <div class="text-center">
          <div class="text-5xl mb-6">&#127881;</div>
          <h2 class="text-2xl font-bold mb-2">Ваш HeYearth ID</h2>

          <div class="my-6 p-6 rounded-2xl bg-surface-card border border-brand/30">
            <p class="text-4xl md:text-5xl font-mono font-extrabold tracking-wider text-brand-light break-all">
              {{ generatedId }}
            </p>
          </div>

          <button
            class="inline-flex items-center gap-2 px-6 py-3 rounded-xl border border-white/10 hover:border-white/20 text-text-primary transition mb-6"
            @click="copyId"
          >
            <svg xmlns="http://www.w3.org/2000/svg" class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
            </svg>
            {{ copied ? 'Скопировано!' : 'Копировать ID' }}
          </button>

          <div class="p-4 rounded-xl bg-yellow-500/10 border border-yellow-500/30 text-yellow-200 text-sm mb-8">
            <p class="font-semibold mb-1">Важно! Сохраните ваш ID!</p>
            <p>Без него вы не сможете войти в аккаунт. Запишите его в надёжное место.</p>
          </div>

          <button
            class="w-full py-3 rounded-xl bg-brand hover:bg-brand-dark text-white font-semibold transition"
            @click="goToDashboard"
          >
            Перейти в личный кабинет
          </button>
        </div>
      </template>
    </div>
  </section>
</template>
