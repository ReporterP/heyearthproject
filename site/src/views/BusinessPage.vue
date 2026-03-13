<script setup>
const advantages = [
  {
    icon: '🏢',
    title: 'Своя админ-панель',
    desc: 'Управляйте только своими пользователями и серверами. Полный контроль.',
  },
  {
    icon: '🔧',
    title: 'Свои или наши серверы',
    desc: 'Подключите собственные серверы или используйте нашу инфраструктуру.',
  },
  {
    icon: '💰',
    title: 'Оплата по потреблению',
    desc: 'Кошелёк с ежемесячным списанием. Платите только за то, что используете.',
  },
  {
    icon: '🔐',
    title: 'Коды для сотрудников',
    desc: 'Раздайте коды компании — сотрудники получат доступ без регистрации.',
  },
]
</script>

<template>
  <section class="pt-24 pb-16">
    <div class="max-w-4xl mx-auto px-6 text-center">
      <h1 class="text-4xl md:text-5xl font-extrabold tracking-tight">
        VPN <span class="text-transparent bg-clip-text bg-gradient-to-r from-brand-light to-accent">для бизнеса</span>
      </h1>
      <p class="mt-6 text-lg text-text-secondary max-w-2xl mx-auto">
        Защитите корпоративный трафик. Управляйте доступом сотрудников из удобной панели.
        По договорённой цене — без скрытых платежей.
      </p>
    </div>
  </section>

  <section class="py-16">
    <div class="max-w-6xl mx-auto px-6 grid md:grid-cols-2 gap-8">
      <div
        v-for="a in advantages"
        :key="a.title"
        class="p-8 rounded-2xl bg-surface-card border border-white/5 hover:border-brand/30 transition"
      >
        <div class="text-3xl mb-4">{{ a.icon }}</div>
        <h3 class="text-xl font-semibold mb-2">{{ a.title }}</h3>
        <p class="text-text-secondary leading-relaxed">{{ a.desc }}</p>
      </div>
    </div>
  </section>

  <section class="py-16 bg-surface-alt/50">
    <div class="max-w-3xl mx-auto px-6">
      <h2 class="text-2xl font-bold text-center mb-10">Тарификация</h2>
      <div class="space-y-4 text-sm">
        <div class="flex justify-between p-4 rounded-xl bg-surface-card border border-white/5">
          <span class="text-text-secondary">Пользователь</span>
          <span class="font-semibold">250 ₽ / мес</span>
        </div>
        <div class="flex justify-between p-4 rounded-xl bg-surface-card border border-white/5">
          <span class="text-text-secondary">Использование наших серверов</span>
          <span class="font-semibold">1 000 ₽ / мес</span>
        </div>
        <div class="flex justify-between p-4 rounded-xl bg-surface-card border border-white/5">
          <span class="text-text-secondary">Свои серверы (до 5 включены)</span>
          <span class="font-semibold">+100 ₽ / доп. сервер</span>
        </div>
        <div class="flex justify-between p-4 rounded-xl bg-surface-card border border-white/5">
          <span class="text-text-secondary">Премиум для пользователя</span>
          <span class="font-semibold">+50 ₽ / мес</span>
        </div>
      </div>
      <p class="text-xs text-text-secondary text-center mt-4">Цены настраиваются индивидуально. Указаны базовые значения.</p>
    </div>
  </section>

  <section class="py-20">
    <div class="max-w-3xl mx-auto px-6 text-center">
      <h2 class="text-3xl font-bold mb-4">Готовы начать?</h2>
      <p class="text-text-secondary mb-8">
        Выберите параметры, укажите почту — и сразу получите доступ в панель управления. Без предоплаты.
      </p>
      <router-link
        to="/business/signup"
        class="inline-block px-8 py-3 rounded-xl bg-brand hover:bg-brand-dark text-white font-semibold transition shadow-lg shadow-brand/25"
      >
        Подключить бизнес
      </router-link>
    </div>
  </section>
</template>
