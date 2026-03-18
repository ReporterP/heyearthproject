<script setup>
import { RouterLink } from 'vue-router'

const features = [
  {
    icon: '🌿',
    title: 'Эко-маршрутизация',
    desc: 'Оптимизированные маршруты трафика снижают энергопотребление и ускоряют доступ к ресурсам.',
  },
  {
    icon: '📱',
    title: 'Все устройства',
    desc: 'Android, Windows, iOS, macOS, Linux. Один аккаунт — все платформы. Устойчивый доступ повсюду.',
  },
]

const plans = [
  {
    name: 'Стандарт',
    price: '200',
    period: 'мес',
    features: ['Эко-маршрутизация трафика', 'Без ограничений по объёму', 'Все устройства'],
    highlighted: false,
  },
  {
    name: 'Премиум',
    price: '250',
    period: 'мес',
    features: ['Всё из Стандарт', 'Приоритетная поддержка', 'Максимальная оптимизация маршрутов'],
    highlighted: true,
  },
]
</script>

<template>
  <section class="relative overflow-hidden pt-24 pb-32">
    <div class="absolute inset-0 bg-gradient-to-br from-brand/10 via-transparent to-accent/5 pointer-events-none" />
    <div class="relative max-w-4xl mx-auto px-6 text-center">
      <h1 class="text-5xl md:text-6xl font-extrabold tracking-tight leading-tight">
        Уменьшите свой <br class="hidden md:block" />
        <span class="text-transparent bg-clip-text bg-gradient-to-r from-brand-light to-accent">цифровой углеродный след</span>
      </h1>
      <p class="mt-6 text-lg text-text-secondary max-w-2xl mx-auto">
        HeYearth — устойчивый интернет-доступ с эко-маршрутизацией. Оптимизируйте трафик, снижайте энергопотребление и пользуйтесь быстрым доступом к ресурсам на всех устройствах.
      </p>
      <div class="mt-10 flex flex-wrap justify-center gap-4">
        <RouterLink
          to="/register"
          class="px-8 py-3 rounded-xl bg-brand hover:bg-brand-dark text-white font-semibold transition shadow-lg shadow-brand/25"
        >
          Начать
        </RouterLink>
        <a
          href="#plans"
          class="px-8 py-3 rounded-xl border border-white/10 hover:border-white/20 text-text-secondary hover:text-text-primary transition"
        >
          Тарифы
        </a>
      </div>
    </div>
  </section>

  <section class="py-20 bg-surface-alt/50">
    <div class="max-w-6xl mx-auto px-6">
      <h2 class="text-3xl font-bold text-center mb-14">Почему HeYearth</h2>
      <div class="grid md:grid-cols-2 lg:grid-cols-4 gap-8">
        <div
          v-for="f in features"
          :key="f.title"
          class="p-6 rounded-2xl bg-surface-card border border-white/5 hover:border-brand/30 transition"
        >
          <div class="text-3xl mb-4">{{ f.icon }}</div>
          <h3 class="text-lg font-semibold mb-2">{{ f.title }}</h3>
          <p class="text-sm text-text-secondary leading-relaxed">{{ f.desc }}</p>
        </div>
      </div>
    </div>
  </section>

  <section id="plans" class="py-20">
    <div class="max-w-4xl mx-auto px-6">
      <h2 class="text-3xl font-bold text-center mb-14">Тарифы</h2>
      <div class="grid md:grid-cols-2 gap-8">
        <div
          v-for="p in plans"
          :key="p.name"
          class="relative p-8 rounded-2xl border transition"
          :class="p.highlighted
            ? 'bg-gradient-to-b from-brand/10 to-transparent border-brand/40 shadow-xl shadow-brand/10'
            : 'bg-surface-card border-white/5 hover:border-white/10'"
        >
          <div v-if="p.highlighted" class="absolute -top-3 left-1/2 -translate-x-1/2 px-3 py-0.5 bg-brand rounded-full text-xs font-semibold">
            Популярный
          </div>
          <h3 class="text-xl font-bold mb-1">{{ p.name }}</h3>
          <div class="flex items-baseline gap-1 mb-6">
            <span class="text-4xl font-extrabold">{{ p.price }}</span>
            <span class="text-text-secondary">&thinsp;&#8381; / {{ p.period }}</span>
          </div>
          <ul class="space-y-3 mb-8">
            <li v-for="feat in p.features" :key="feat" class="flex items-start gap-2 text-sm text-text-secondary">
              <svg class="w-4 h-4 mt-0.5 text-brand-light flex-shrink-0" fill="currentColor" viewBox="0 0 20 20">
                <path fill-rule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clip-rule="evenodd" />
              </svg>
              <span>{{ feat }}</span>
            </li>
          </ul>
          <RouterLink
            to="/register"
            class="block text-center px-6 py-3 rounded-xl font-semibold transition"
            :class="p.highlighted
              ? 'bg-brand hover:bg-brand-dark text-white'
              : 'border border-white/10 hover:border-white/20 text-text-primary'"
          >
            Выбрать
          </RouterLink>
        </div>
      </div>
    </div>
  </section>

  <section class="py-20 bg-surface-alt/50">
    <div class="max-w-3xl mx-auto px-6 text-center">
      <h2 class="text-3xl font-bold mb-4">Для бизнеса</h2>
      <p class="text-text-secondary mb-8">
        Корпоративная эко-оптимизация трафика. Своя панель управления, контроль сотрудников, гибкие тарифы.
      </p>
      <router-link
        to="/business"
        class="inline-block px-8 py-3 rounded-xl border border-brand/40 hover:bg-brand/10 text-brand-light font-semibold transition"
      >
        Подробнее
      </router-link>
    </div>
  </section>
</template>
