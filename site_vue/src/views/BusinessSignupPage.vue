<script setup>
import { ref, computed } from 'vue'
import { createTenant } from '../api/tenant.js'

const step = ref(1)

const specs = ref({
  users: 10,
  ownServers: 0,
  useProviderServers: true,
  premiumEnabled: false,
  unlimited: false,
})

const email = ref('')
const companyName = ref('')
const submitting = ref(false)
const submitted = ref(false)
const error = ref('')

const monthlyEstimate = computed(() => {
  if (specs.value.unlimited) return 20000
  let total = specs.value.users * 250
  if (specs.value.useProviderServers) total += 1000
  const extraServers = Math.max(0, specs.value.ownServers - 5)
  total += extraServers * 100
  if (specs.value.premiumEnabled) total += specs.value.users * 50
  return total
})

function nextStep() {
  if (step.value < 2) step.value++
}

async function submit() {
  if (!email.value.trim()) {
    error.value = 'Укажите email'
    return
  }
  if (!companyName.value.trim()) {
    error.value = 'Укажите название компании'
    return
  }
  submitting.value = true
  error.value = ''
  try {
    await createTenant({
      email: email.value,
      companyName: companyName.value,
      specs: specs.value,
    })
    submitted.value = true
  } catch (e) {
    error.value = e.message || 'Ошибка. Попробуйте позже.'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <section class="py-24">
    <div class="max-w-2xl mx-auto px-6">
      <h1 class="text-3xl font-bold text-center mb-2">Подключение бизнес-аккаунта</h1>
      <p class="text-text-secondary text-center mb-12">Без предоплаты на сайте. Оплата — после входа в панель управления.</p>

      <template v-if="submitted">
        <div class="text-center p-10 rounded-2xl bg-surface-card border border-brand/30">
          <div class="text-5xl mb-4">✅</div>
          <h2 class="text-2xl font-bold mb-2">Готово!</h2>
          <p class="text-text-secondary">
            Данные для входа в панель управления отправлены на <span class="text-text-primary font-medium">{{ email }}</span>.
            Пополните кошелёк и активируйте выбранные параметры.
          </p>
        </div>
      </template>

      <template v-else>
        <div class="flex items-center justify-center gap-4 mb-10">
          <div
            v-for="s in [1, 2]"
            :key="s"
            class="flex items-center gap-2"
          >
            <div
              class="w-8 h-8 rounded-full flex items-center justify-center text-sm font-bold transition"
              :class="step >= s ? 'bg-brand text-white' : 'bg-surface-card border border-white/10 text-text-secondary'"
            >
              {{ s }}
            </div>
            <span class="text-sm" :class="step >= s ? 'text-text-primary' : 'text-text-secondary'">
              {{ s === 1 ? 'Параметры' : 'Контакт' }}
            </span>
            <div v-if="s < 2" class="w-12 h-px bg-white/10" />
          </div>
        </div>

        <div v-if="step === 1" class="space-y-6">
          <template v-if="!specs.unlimited">
            <div>
              <label class="block text-sm text-text-secondary mb-2">Количество пользователей</label>
              <input
                v-model.number="specs.users"
                type="number"
                min="1"
                class="w-full px-4 py-3 rounded-xl bg-surface-card border border-white/10 focus:border-brand/50 focus:outline-none transition"
              />
            </div>

            <div>
              <label class="block text-sm text-text-secondary mb-2">Своих серверов (5 включены)</label>
              <input
                v-model.number="specs.ownServers"
                type="number"
                min="0"
                class="w-full px-4 py-3 rounded-xl bg-surface-card border border-white/10 focus:border-brand/50 focus:outline-none transition"
              />
            </div>

            <label class="flex items-center gap-3 cursor-pointer">
              <input v-model="specs.useProviderServers" type="checkbox" class="accent-brand w-5 h-5" />
              <span>Использовать наши серверы (+1 000 ₽/мес)</span>
            </label>

            <label class="flex items-center gap-3 cursor-pointer">
              <input v-model="specs.premiumEnabled" type="checkbox" class="accent-brand w-5 h-5" />
              <span>Премиум для пользователей (+50 ₽/мес за каждого)</span>
            </label>
          </template>

          <div class="p-6 rounded-xl bg-gradient-to-r from-brand/10 to-transparent border border-brand/20">
            <div class="flex justify-between items-baseline">
              <span class="text-text-secondary">Ориентировочная стоимость</span>
              <span class="text-2xl font-extrabold">{{ monthlyEstimate.toLocaleString('ru-RU') }} ₽<span class="text-sm font-normal text-text-secondary"> / мес</span></span>
            </div>
          </div>

          <button
            class="w-full py-3 rounded-xl bg-brand hover:bg-brand-dark text-white font-semibold transition"
            @click="nextStep"
          >
            Далее
          </button>
        </div>

        <div v-if="step === 2" class="space-y-6">
          <div>
            <label class="block text-sm text-text-secondary mb-2">Email *</label>
            <input
              v-model="email"
              type="email"
              placeholder="you@company.com"
              class="w-full px-4 py-3 rounded-xl bg-surface-card border border-white/10 focus:border-brand/50 focus:outline-none transition"
            />
          </div>

          <div>
            <label class="block text-sm text-text-secondary mb-2">Название компании</label>
            <input
              v-model="companyName"
              type="text"
              placeholder="ООО «Компания»"
              class="w-full px-4 py-3 rounded-xl bg-surface-card border border-white/10 focus:border-brand/50 focus:outline-none transition"
            />
          </div>

          <div class="p-6 rounded-xl bg-surface-card border border-white/5 text-sm text-text-secondary">
            <p class="mb-1"><span class="text-text-primary font-medium">Итого:</span> {{ monthlyEstimate.toLocaleString('ru-RU') }} ₽ / мес</p>
            <p>После отправки вы получите доступ в панель управления. Оплата — через пополнение кошелька в панели.</p>
          </div>

          <p v-if="error" class="text-red-400 text-sm">{{ error }}</p>

          <div class="flex gap-3">
            <button
              class="flex-1 py-3 rounded-xl border border-white/10 hover:border-white/20 text-text-secondary transition"
              @click="step = 1"
            >
              Назад
            </button>
            <button
              class="flex-1 py-3 rounded-xl bg-brand hover:bg-brand-dark text-white font-semibold transition disabled:opacity-50"
              :disabled="submitting"
              @click="submit"
            >
              {{ submitting ? 'Отправка...' : 'Получить доступ' }}
            </button>
          </div>
        </div>
      </template>
    </div>
  </section>
</template>
