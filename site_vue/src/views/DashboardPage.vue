<script setup>
import { ref, onMounted, computed } from 'vue'
import { useRouter, RouterLink } from 'vue-router'
import { getProfile, getSubscription, getConnections, getPayments, getDownloads } from '../api/user.js'

const router = useRouter()
const token = localStorage.getItem('token')

const profile = ref(null)
const subscription = ref(null)
const connections = ref(null)
const payments = ref([])
const downloads = ref([])
const loading = ref(true)
const error = ref('')
const copiedField = ref('')
const activeTab = ref('apple')

const tabs = [
  { id: 'apple', label: 'iOS / macOS' },
  { id: 'other', label: 'Windows / Android / Linux' },
]

onMounted(async () => {
  if (!token) {
    router.push('/login')
    return
  }
  try {
    const [profileData, subData, connData, payData, dlData] = await Promise.all([
      getProfile(token),
      getSubscription(token).catch(() => null),
      getConnections(token).catch(() => null),
      getPayments(token).catch(() => []),
      getDownloads().catch(() => []),
    ])
    profile.value = profileData
    subscription.value = subData
    connections.value = connData
    payments.value = Array.isArray(payData) ? payData : payData?.payments || []
    downloads.value = Array.isArray(dlData) ? dlData : dlData?.downloads || []
  } catch (e) {
    if (e.message?.includes('401') || e.message?.includes('Unauthorized')) {
      localStorage.removeItem('token')
      router.push('/login')
      return
    }
    error.value = e.message || 'Ошибка загрузки данных'
  } finally {
    loading.value = false
  }
})

const userId = computed(() => profile.value?.id || profile.value?.heyearth_id || '...')

const subPlan = computed(() => subscription.value?.plan || 'Нет подписки')
const subValidUntil = computed(() => {
  if (!subscription.value?.valid_until) return null
  return new Date(subscription.value.valid_until).toLocaleDateString('ru-RU')
})

const vlessUri = computed(() => connections.value?.vless_uri || '')
const ssUri = computed(() => connections.value?.ss_uri || '')
const socks5Addr = computed(() => connections.value?.socks5 || '')
const httpProxy = computed(() => connections.value?.http_proxy || '')

async function copyText(text, field) {
  try {
    await navigator.clipboard.writeText(text)
    copiedField.value = field
    setTimeout(() => (copiedField.value = ''), 2000)
  } catch {
    /* clipboard unavailable */
  }
}

function formatDate(d) {
  if (!d) return '---'
  return new Date(d).toLocaleDateString('ru-RU')
}

function formatAmount(amount) {
  if (!amount && amount !== 0) return '---'
  return Number(amount).toLocaleString('ru-RU') + ' \u20BD'
}
</script>

<template>
  <section class="py-16">
    <div class="max-w-4xl mx-auto px-6">
      <div class="flex items-center justify-between mb-10">
        <h1 class="text-3xl font-bold">Личный кабинет</h1>
        <RouterLink to="/settings" class="text-sm text-brand-light hover:text-brand transition">
          Настройки
        </RouterLink>
      </div>

      <!-- Loading -->
      <div v-if="loading" class="text-center py-20 text-text-secondary">Загрузка...</div>

      <!-- Error -->
      <div v-else-if="error" class="text-center py-20">
        <p class="text-red-400 mb-4">{{ error }}</p>
        <button class="px-6 py-2 rounded-xl bg-brand hover:bg-brand-dark text-white transition" @click="$router.go(0)">
          Повторить
        </button>
      </div>

      <template v-else>
        <!-- HeYearth ID -->
        <div class="p-6 rounded-2xl bg-surface-card border border-white/5 mb-6">
          <p class="text-sm text-text-secondary mb-1">Ваш HeYearth ID</p>
          <div class="flex items-center gap-3">
            <span class="text-2xl font-mono font-bold text-brand-light">{{ userId }}</span>
            <button
              class="px-3 py-1 rounded-lg text-xs border border-white/10 hover:border-white/20 transition"
              @click="copyText(userId, 'id')"
            >
              {{ copiedField === 'id' ? 'Скопировано!' : 'Копировать' }}
            </button>
          </div>
        </div>

        <!-- Subscription -->
        <div class="p-6 rounded-2xl bg-surface-card border border-white/5 mb-6">
          <div class="flex items-center justify-between mb-2">
            <p class="text-sm text-text-secondary">Подписка</p>
            <RouterLink to="/payment" class="text-sm text-brand-light hover:text-brand transition">
              {{ subscription ? 'Продлить' : 'Оформить' }}
            </RouterLink>
          </div>
          <p class="text-lg font-semibold">{{ subPlan }}</p>
          <p v-if="subValidUntil" class="text-sm text-text-secondary mt-1">
            Действует до {{ subValidUntil }}
          </p>
        </div>

        <!-- Connections -->
        <div class="p-6 rounded-2xl bg-surface-card border border-white/5 mb-6">
          <h2 class="text-lg font-semibold mb-4">Подключение</h2>

          <!-- Tabs -->
          <div class="flex gap-2 mb-6">
            <button
              v-for="tab in tabs"
              :key="tab.id"
              class="px-4 py-2 rounded-lg text-sm transition"
              :class="activeTab === tab.id
                ? 'bg-brand text-white'
                : 'bg-surface border border-white/10 text-text-secondary hover:text-text-primary'"
              @click="activeTab = tab.id"
            >
              {{ tab.label }}
            </button>
          </div>

          <!-- iOS / macOS -->
          <div v-if="activeTab === 'apple'">
            <div v-if="vlessUri" class="mb-4">
              <p class="text-sm text-text-secondary mb-1">VLESS URI</p>
              <div class="flex items-center gap-2">
                <code class="flex-1 text-xs bg-surface rounded-lg px-3 py-2 break-all border border-white/5">{{ vlessUri }}</code>
                <button
                  class="px-3 py-2 rounded-lg text-xs border border-white/10 hover:border-white/20 transition shrink-0"
                  @click="copyText(vlessUri, 'vless')"
                >
                  {{ copiedField === 'vless' ? 'OK' : 'Копировать' }}
                </button>
              </div>
            </div>

            <div v-if="ssUri" class="mb-4">
              <p class="text-sm text-text-secondary mb-1">Shadowsocks URI</p>
              <div class="flex items-center gap-2">
                <code class="flex-1 text-xs bg-surface rounded-lg px-3 py-2 break-all border border-white/5">{{ ssUri }}</code>
                <button
                  class="px-3 py-2 rounded-lg text-xs border border-white/10 hover:border-white/20 transition shrink-0"
                  @click="copyText(ssUri, 'ss')"
                >
                  {{ copiedField === 'ss' ? 'OK' : 'Копировать' }}
                </button>
              </div>
            </div>

            <div class="text-sm text-text-secondary space-y-1">
              <p>Поддерживаемые приложения:</p>
              <ul class="list-disc list-inside ml-2 space-y-1">
                <li>Shadowrocket (iOS)</li>
                <li>Happ (macOS)</li>
              </ul>
            </div>

            <div v-if="!vlessUri && !ssUri" class="text-text-secondary text-sm">
              Данные подключения появятся после оформления подписки.
            </div>
          </div>

          <!-- Windows / Android / Linux -->
          <div v-if="activeTab === 'other'">
            <p class="text-text-secondary text-sm mb-4">
              Скачайте приложение HeYearth для вашей платформы:
            </p>
            <div v-if="downloads.length" class="flex flex-wrap gap-3">
              <a
                v-for="dl in downloads"
                :key="dl.platform || dl.name"
                :href="dl.url || dl.download_url"
                target="_blank"
                class="inline-flex items-center gap-2 px-5 py-3 rounded-xl bg-brand hover:bg-brand-dark text-white font-semibold transition"
              >
                {{ dl.platform || dl.name || 'Скачать' }}
              </a>
            </div>
            <p v-else class="text-text-secondary text-sm">
              Загрузки пока недоступны. Скоро добавим.
            </p>
          </div>

          <!-- Common: proxy addresses -->
          <div v-if="socks5Addr || httpProxy" class="mt-6 pt-6 border-t border-white/5">
            <h3 class="text-sm font-semibold mb-3">Прокси</h3>
            <div v-if="socks5Addr" class="mb-3">
              <p class="text-xs text-text-secondary mb-1">SOCKS5</p>
              <div class="flex items-center gap-2">
                <code class="flex-1 text-xs bg-surface rounded-lg px-3 py-2 border border-white/5">{{ socks5Addr }}</code>
                <button
                  class="px-3 py-2 rounded-lg text-xs border border-white/10 hover:border-white/20 transition shrink-0"
                  @click="copyText(socks5Addr, 'socks5')"
                >
                  {{ copiedField === 'socks5' ? 'OK' : 'Копировать' }}
                </button>
              </div>
            </div>
            <div v-if="httpProxy">
              <p class="text-xs text-text-secondary mb-1">HTTP</p>
              <div class="flex items-center gap-2">
                <code class="flex-1 text-xs bg-surface rounded-lg px-3 py-2 border border-white/5">{{ httpProxy }}</code>
                <button
                  class="px-3 py-2 rounded-lg text-xs border border-white/10 hover:border-white/20 transition shrink-0"
                  @click="copyText(httpProxy, 'http')"
                >
                  {{ copiedField === 'http' ? 'OK' : 'Копировать' }}
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- Payment history -->
        <div class="p-6 rounded-2xl bg-surface-card border border-white/5">
          <div class="flex items-center justify-between mb-4">
            <h2 class="text-lg font-semibold">История платежей</h2>
          </div>
          <div v-if="payments.length === 0" class="text-text-secondary text-sm">
            Платежей пока нет.
          </div>
          <div v-else class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead>
                <tr class="text-left text-text-secondary border-b border-white/5">
                  <th class="pb-2 pr-4 font-medium">Дата</th>
                  <th class="pb-2 pr-4 font-medium">Тариф</th>
                  <th class="pb-2 pr-4 font-medium">Сумма</th>
                  <th class="pb-2 font-medium">Статус</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="pay in payments" :key="pay.id" class="border-b border-white/5 last:border-0">
                  <td class="py-3 pr-4">{{ formatDate(pay.created_at || pay.date) }}</td>
                  <td class="py-3 pr-4">{{ pay.plan || '---' }}</td>
                  <td class="py-3 pr-4">{{ formatAmount(pay.amount) }}</td>
                  <td class="py-3">
                    <span
                      class="px-2 py-0.5 rounded-full text-xs"
                      :class="pay.status === 'succeeded' || pay.status === 'paid'
                        ? 'bg-green-500/20 text-green-300'
                        : 'bg-yellow-500/20 text-yellow-300'"
                    >
                      {{ pay.status === 'succeeded' || pay.status === 'paid' ? 'Оплачен' : pay.status || '---' }}
                    </span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </template>
    </div>
  </section>
</template>
