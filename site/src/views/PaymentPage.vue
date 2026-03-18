<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { createPayment } from '../api/user.js'

const router = useRouter()
const token = localStorage.getItem('token')

const selectedPlan = ref('')
const promoCode = ref('')
const loading = ref(false)
const error = ref('')

const plans = [
  {
    id: 'standard',
    name: 'Стандарт',
    price: '200',
    period: 'мес',
    features: [
      'Эко-маршрутизация трафика',
      'Без ограничений по трафику',
      'Все устройства',
    ],
    highlighted: false,
  },
  {
    id: 'premium',
    name: 'Премиум',
    price: '250',
    period: 'мес',
    features: [
      'Все из Стандарт',
      'Приоритетная поддержка',
      'Максимальная оптимизация маршрутов',
    ],
    highlighted: true,
  },
]

async function handlePayment() {
  if (!selectedPlan.value) {
    error.value = 'Выберите тариф'
    return
  }
  if (!token) {
    router.push('/login')
    return
  }
  loading.value = true
  error.value = ''
  try {
    const returnUrl = window.location.origin + '/dashboard'
    const data = await createPayment(token, selectedPlan.value, returnUrl)
    if (data.payment_url || data.confirmation_url) {
      window.location.href = data.payment_url || data.confirmation_url
    } else {
      error.value = 'Не удалось получить ссылку на оплату'
    }
  } catch (e) {
    error.value = e.message || 'Ошибка создания платежа'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <section class="py-24">
    <div class="max-w-3xl mx-auto px-6">
      <h1 class="text-3xl font-bold text-center mb-2">Выберите тариф</h1>
      <p class="text-text-secondary text-center mb-12">Оплата через YooKassa. Подписка активируется мгновенно.</p>

      <div class="grid md:grid-cols-2 gap-8 mb-10">
        <div
          v-for="p in plans"
          :key="p.id"
          class="relative p-8 rounded-2xl border transition cursor-pointer"
          :class="[
            p.highlighted
              ? 'bg-gradient-to-b from-brand/10 to-transparent border-brand/40 shadow-xl shadow-brand/10'
              : 'bg-surface-card border-white/5 hover:border-white/10',
            selectedPlan === p.id ? 'ring-2 ring-brand' : ''
          ]"
          @click="selectedPlan = p.id"
        >
          <div v-if="p.highlighted" class="absolute -top-3 left-1/2 -translate-x-1/2 px-3 py-0.5 bg-brand rounded-full text-xs font-semibold">
            Рекомендуем
          </div>
          <h3 class="text-xl font-bold mb-1">{{ p.name }}</h3>
          <div class="flex items-baseline gap-1 mb-6">
            <span class="text-4xl font-extrabold">{{ p.price }}</span>
            <span class="text-text-secondary">&thinsp;&#8381; / {{ p.period }}</span>
          </div>
          <ul class="space-y-3 mb-6">
            <li v-for="feat in p.features" :key="feat" class="flex items-start gap-2 text-sm text-text-secondary">
              <svg class="w-4 h-4 mt-0.5 text-brand-light flex-shrink-0" fill="currentColor" viewBox="0 0 20 20">
                <path fill-rule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clip-rule="evenodd" />
              </svg>
              <span>{{ feat }}</span>
            </li>
          </ul>
          <div
            class="w-5 h-5 rounded-full border-2 mx-auto transition"
            :class="selectedPlan === p.id ? 'bg-brand border-brand' : 'border-white/20'"
          />
        </div>
      </div>

      <!-- Promo code -->
      <div class="mb-8">
        <label class="block text-sm text-text-secondary mb-2">Промокод (необязательно)</label>
        <input
          v-model="promoCode"
          type="text"
          placeholder="Введите промокод"
          class="w-full px-4 py-3 rounded-xl bg-surface-card border border-white/10 focus:border-brand/50 focus:outline-none transition"
        />
      </div>

      <p v-if="error" class="text-red-400 text-sm mb-4">{{ error }}</p>

      <button
        :disabled="loading || !selectedPlan"
        class="w-full py-3 rounded-xl bg-brand hover:bg-brand-dark text-white font-semibold transition disabled:opacity-50"
        @click="handlePayment"
      >
        {{ loading ? 'Перенаправление...' : 'Оплатить' }}
      </button>
    </div>
  </section>
</template>
