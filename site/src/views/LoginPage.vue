<script setup>
import { ref } from 'vue'
import { useRouter, RouterLink } from 'vue-router'
import { loginUser } from '../api/user.js'

const router = useRouter()

const idOrUsername = ref('')
const password = ref('')
const showPassword = ref(false)
const loading = ref(false)
const error = ref('')

async function handleLogin() {
  if (!idOrUsername.value.trim()) {
    error.value = 'Введите HeYearth ID или имя пользователя'
    return
  }
  loading.value = true
  error.value = ''
  try {
    const data = await loginUser(idOrUsername.value.trim(), password.value)
    localStorage.setItem('token', data.token)
    router.push('/dashboard')
  } catch (e) {
    error.value = e.message || 'Ошибка входа. Попробуйте позже.'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <section class="py-24">
    <div class="max-w-md mx-auto px-6">
      <h1 class="text-3xl font-bold text-center mb-2">Вход</h1>
      <p class="text-text-secondary text-center mb-10">Войдите в свой аккаунт HeYearth</p>

      <form @submit.prevent="handleLogin" class="space-y-5">
        <div>
          <label class="block text-sm text-text-secondary mb-2">HeYearth ID или имя пользователя</label>
          <input
            v-model="idOrUsername"
            type="text"
            placeholder="Введите ваш ID или username"
            class="w-full px-4 py-3 rounded-xl bg-surface-card border border-white/10 focus:border-brand/50 focus:outline-none transition"
          />
        </div>

        <div>
          <button
            v-if="!showPassword"
            type="button"
            class="text-sm text-brand-light hover:text-brand transition"
            @click="showPassword = true"
          >
            У меня есть облачный пароль
          </button>
          <template v-else>
            <label class="block text-sm text-text-secondary mb-2">Облачный пароль</label>
            <input
              v-model="password"
              type="password"
              placeholder="Введите пароль"
              class="w-full px-4 py-3 rounded-xl bg-surface-card border border-white/10 focus:border-brand/50 focus:outline-none transition"
            />
          </template>
        </div>

        <p v-if="error" class="text-red-400 text-sm">{{ error }}</p>

        <button
          type="submit"
          :disabled="loading"
          class="w-full py-3 rounded-xl bg-brand hover:bg-brand-dark text-white font-semibold transition disabled:opacity-50"
        >
          {{ loading ? 'Вход...' : 'Войти' }}
        </button>
      </form>

      <p class="text-center text-sm text-text-secondary mt-8">
        Нет аккаунта?
        <RouterLink to="/register" class="text-brand-light hover:text-brand transition">Зарегистрироваться</RouterLink>
      </p>
    </div>
  </section>
</template>
