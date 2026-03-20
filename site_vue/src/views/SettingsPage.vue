<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { getProfile, updateProfile } from '../api/user.js'

const router = useRouter()
const token = localStorage.getItem('token')

const loading = ref(true)
const username = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const currentPassword = ref('')
const savingUsername = ref(false)
const savingPassword = ref(false)
const usernameMsg = ref('')
const usernameError = ref('')
const passwordMsg = ref('')
const passwordError = ref('')

onMounted(async () => {
  if (!token) {
    router.push('/login')
    return
  }
  try {
    const data = await getProfile(token)
    username.value = data.username || ''
  } catch (e) {
    if (e.message?.includes('401') || e.message?.includes('Unauthorized')) {
      localStorage.removeItem('token')
      router.push('/login')
    }
  } finally {
    loading.value = false
  }
})

async function saveUsername() {
  if (!username.value.trim()) {
    usernameError.value = 'Введите имя пользователя'
    return
  }
  savingUsername.value = true
  usernameMsg.value = ''
  usernameError.value = ''
  try {
    await updateProfile(token, { username: username.value.trim() })
    usernameMsg.value = 'Имя пользователя сохранено'
  } catch (e) {
    usernameError.value = e.message || 'Ошибка сохранения'
  } finally {
    savingUsername.value = false
  }
}

async function savePassword() {
  passwordMsg.value = ''
  passwordError.value = ''
  if (!newPassword.value) {
    passwordError.value = 'Введите новый пароль'
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    passwordError.value = 'Пароли не совпадают'
    return
  }
  savingPassword.value = true
  try {
    await updateProfile(token, {
      newPassword: newPassword.value,
      currentPassword: currentPassword.value || undefined,
    })
    passwordMsg.value = 'Пароль сохранён'
    newPassword.value = ''
    confirmPassword.value = ''
    currentPassword.value = ''
  } catch (e) {
    passwordError.value = e.message || 'Ошибка сохранения'
  } finally {
    savingPassword.value = false
  }
}

function logout() {
  localStorage.removeItem('token')
  router.push('/')
}
</script>

<template>
  <section class="py-24">
    <div class="max-w-lg mx-auto px-6">
      <h1 class="text-3xl font-bold text-center mb-10">Настройки</h1>

      <div v-if="loading" class="text-center py-10 text-text-secondary">Загрузка...</div>

      <template v-else>
        <!-- Username -->
        <div class="p-6 rounded-2xl bg-surface-card border border-white/5 mb-6">
          <h2 class="text-lg font-semibold mb-4">Имя пользователя</h2>
          <input
            v-model="username"
            type="text"
            placeholder="username"
            class="w-full px-4 py-3 rounded-xl bg-surface border border-white/10 focus:border-brand/50 focus:outline-none transition mb-3"
          />
          <p v-if="usernameMsg" class="text-green-400 text-sm mb-3">{{ usernameMsg }}</p>
          <p v-if="usernameError" class="text-red-400 text-sm mb-3">{{ usernameError }}</p>
          <button
            :disabled="savingUsername"
            class="px-6 py-2 rounded-xl bg-brand hover:bg-brand-dark text-white text-sm font-semibold transition disabled:opacity-50"
            @click="saveUsername"
          >
            {{ savingUsername ? 'Сохранение...' : 'Сохранить' }}
          </button>
        </div>

        <!-- Password -->
        <div class="p-6 rounded-2xl bg-surface-card border border-white/5 mb-6">
          <h2 class="text-lg font-semibold mb-4">Облачный пароль</h2>
          <div class="space-y-3">
            <div>
              <label class="block text-sm text-text-secondary mb-1">Текущий пароль (если установлен)</label>
              <input
                v-model="currentPassword"
                type="password"
                placeholder="Текущий пароль"
                class="w-full px-4 py-3 rounded-xl bg-surface border border-white/10 focus:border-brand/50 focus:outline-none transition"
              />
            </div>
            <div>
              <label class="block text-sm text-text-secondary mb-1">Новый пароль</label>
              <input
                v-model="newPassword"
                type="password"
                placeholder="Новый пароль"
                class="w-full px-4 py-3 rounded-xl bg-surface border border-white/10 focus:border-brand/50 focus:outline-none transition"
              />
            </div>
            <div>
              <label class="block text-sm text-text-secondary mb-1">Подтвердите пароль</label>
              <input
                v-model="confirmPassword"
                type="password"
                placeholder="Повторите пароль"
                class="w-full px-4 py-3 rounded-xl bg-surface border border-white/10 focus:border-brand/50 focus:outline-none transition"
              />
            </div>
          </div>
          <p v-if="passwordMsg" class="text-green-400 text-sm mt-3">{{ passwordMsg }}</p>
          <p v-if="passwordError" class="text-red-400 text-sm mt-3">{{ passwordError }}</p>
          <button
            :disabled="savingPassword"
            class="mt-4 px-6 py-2 rounded-xl bg-brand hover:bg-brand-dark text-white text-sm font-semibold transition disabled:opacity-50"
            @click="savePassword"
          >
            {{ savingPassword ? 'Сохранение...' : 'Сохранить пароль' }}
          </button>
        </div>

        <!-- Logout -->
        <button
          class="w-full py-3 rounded-xl border border-red-500/30 text-red-400 hover:bg-red-500/10 transition text-sm font-medium"
          @click="logout"
        >
          Выйти из аккаунта
        </button>
      </template>
    </div>
  </section>
</template>
