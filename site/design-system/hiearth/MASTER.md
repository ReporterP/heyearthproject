# HiEarth — Дизайн-система «Лес в ночи»

> **Источник правды** для редизайна `site/` (маркетинг + личный кабинет).
> Направление: **dark biophilic** — ночной лес, живая природа, спокойствие и приватность.
> Живое превью: `scratchpad/hiearth-preview.html` (artifact).
> `admin_web/` этой системе НЕ подчиняется — у него свой Linear-theme-lock.

Проверяя конкретную страницу, сперва смотри `pages/<page>.md`; если его нет — правила отсюда.

---

## 1. Принципы

1. **Природа, а не кибер.** Ни один конкурент-VPN так не выглядит. Играем на бренде-деревьях (Берёза/Дуб/Бамбук/Пальма). Органика, свечение, тепло — вместо неона и матрицы.
2. **Тёмная база, живой свет.** Глубокий лесной фон; emerald светится как биолюминесценция, янтарь — как светлячок. Свет — акцент, не заливка.
3. **Одна яркость на экран.** Максимум один primary-CTA в кадре. Остальное — тихое.
4. **Движение = смысл.** Анимация показывает причинно-следственную связь или живость природы, а не украшает. Всегда уважает `prefers-reduced-motion`.
5. **Тон — приватность и скорость.** НИКОГДА не «обход блокировок», «обход DPI», «обход цензуры». См. §7.

---

## 2. Цвет (токены)

Тёмная тема — основная и единственная (осознанный committed dark-world выбор).

```css
:root {
  /* grounds */
  --bg:        #0a130f;  /* глубокий ночной лес (не чёрный — зелёный подтон) */
  --surface:   #14231a;  /* карточки */
  --surface-2: #101d16;  /* вложенные поверхности, градиент-низ */
  --raise:     #1a3227;  /* выделенная карточка (feat) */

  /* brand greens */
  --emerald:    #34d399; /* основной акцент, свечение */
  --emerald-dk: #10b981; /* hover/pressed на светлом фоне */
  --moss:       #7ff0c0; /* hover-подсветка primary */
  --forest:     #064e3b; /* глубокая тень/градиент */

  /* warm accent — «светлячок / луна» */
  --sun:    #fcc94b;
  --sun-dk: #f0a91f;

  /* text */
  --ink:   #edf6f0;  /* заголовки/основной (тёплый белый с зелёным bias) */
  --sub:   #a4c3b5;  /* вторичный текст */
  --faint: #648677;  /* подписи, мелочь */

  /* lines */
  --line:   rgba(120,200,160,.10);
  --line-2: rgba(120,200,160,.19);

  /* semantic (отдельно от акцента!) */
  --ok:      #34d399;
  --warning: #fcc94b;
  --danger:  #f87171;
}
```

Фон страницы — не плоский: `radial-gradient(120% 80% at 82% -8%, rgba(16,60,45,.55), transparent 60%)` поверх `--bg` (лесное свечение сверху-справа).

**Контраст:** `--ink` на `--bg` ≈ 15:1, `--sub` на `--surface` ≥ 4.5:1. Проверять любую новую пару.

---

## 3. Типографика

- **Дисплей/заголовки:** **Lora** (serif, характер, италик для акцентов) → fallback `"Iowan Old Style","Palatino Linotype",Palatino,Georgia,serif`.
- **Текст/UI:** **Raleway** (гуманистический sans) → fallback `"Avenir Next",system-ui,-apple-system,"Segoe UI",sans-serif`.
- В прод подключать через `@fontsource` (self-hosted, НЕ CDN — CSP/скорость).

```
h1  clamp(38px, 6vw, 68px)  Lora 600, italic для выделенного слова, letter-spacing -.02em, line-height 1.05
h2  clamp(27px, 4vw, 44px)  Lora 600, -.015em
h3  18–19px                 Lora/Raleway 600
lead 15.5–18.5px            Raleway 500, line-height 1.68, max 47ch
body 14–16px                Raleway 500, line-height 1.62
kick/eyebrow 11.5px         Raleway 700, uppercase, letter-spacing .14em, цвет --emerald
price 30px                  Lora 600, tabular-nums
```

- Цифры в таблицах/ценах/метриках — `font-variant-numeric: tabular-nums`.
- Заголовкам — `text-wrap: balance`. Строка текста ≤ ~65 символов.

---

## 4. Пространство, форма, тень

```css
--radius-sm: 12px;  --radius: 16px;  --radius-lg: 22px;  --radius-xl: 28px;  --pill: 999px;
/* карточки 22px, кнопки/таги pill, модалки 28px */

/* space: 4px base → 8/12/16/20/24/32/44/64/100 ритм */
/* секции: clamp(52px, 7vw, 104px) вертикали */

--shadow-card:  0 26px 50px -28px rgba(0,0,0,.6);
--shadow-float: 0 40px 80px -40px rgba(0,0,0,.75);
--glow-emerald: 0 14px 34px -12px rgba(52,211,153,.55);

/* z: base 0 / raised 5 / nav 20 / toolbar 200 / modal 1000 */
```

Границы — **хайрлайн** (`--line`/`--line-2`), НЕ тяжёлые тени как разделители. Свечение (`--glow-emerald`) только на primary-действии и feat-карточке.

---

## 5. Motion-система ⭐ (анимации по всему сайту)

Движение — фирменная черта «Леса в ночи». Всё строится на этих токенах и **всё оборачивается в `@media (prefers-reduced-motion: no-preference)`** (или отключается в `reduce`).

### Токены
```css
--dur-fast: 180ms;  --dur-base: 240ms;  --dur-slow: 700ms;  --dur-ambient: 18–24s;
--ease-out:    cubic-bezier(.2,.7,.2,1);     /* входы, reveal */
--ease-spring: cubic-bezier(.34,1.56,.64,1); /* нажатия, «живые» отклики */
/* Правило: выход ≈ 60–70% длительности входа. */
```

### A. Ambient (фон, постоянно, тонко)
- **Светлячки** — `<canvas>`-слой в hero (и опц. в CTA-band). Частиц `max(10, w*h/26000)`: дрейф `cos/sin(angle)*speed`, angle гуляет `±.12/кадр`, мерцание `0.35..1` через `sin(t*0.002+phase)`, `shadowBlur 8`. ~28% частиц тёплые (`--sun`), остальные `--emerald`. Wrap по краям. DPR-aware, cap 2×. Переносить как React-компонент `<Fireflies/>` (референс — `#ff` в превью).
- **Blob drift** — 2 радиальных blob'а (emerald + sun) за hero, `drift 20–24s ease-in-out infinite alternate` (сдвиг ±26px + поворот ±8°).
- **Ground breathing** (опц., очень медленно) — микро-сдвиг радиального свечения фона, ≥30s, крошечная амплитуда.

### B. Entrance (появление / скролл)
- **Scroll reveal** — `.reveal{opacity:0; translateY(18px)}` → `.in` через `--dur-slow --ease-out`. IntersectionObserver, threshold .12, `rootMargin 0 0 -6% 0`, unobserve после срабатывания.
- **Stagger** — соседние элементы: `transition-delay = min(index*55ms, 220ms)`.
- **Count-up** — метрики (99.9%, 4, <60мс, цены) считаются от 0 при первом появлении, ~900ms `--ease-out`, tabular-nums чтобы ширина не дёргалась. reduce → сразу финал.

### C. Interaction (отклик)
- **Hover lift** — карточки: `translateY(-3px)` + усиление тени, `--dur-base --ease-out`.
- **Press scale** — кнопки/кликабельные карточки: `scale(.97)` на `:active`, `--ease-spring`.
- **Button shine** — primary: диагональный блик (псевдоэлемент, translateX от -120% к 120%) при hover, один проход ~600ms.
- **Tree sway** 🌳 — SVG-деревья в тарифах покачиваются при hover карточки (`rotate ±2.5deg`, `transform-origin: bottom center`, `--ease-spring`) — как ветка на ветру. Фирменная тематическая микроанимация.
- **Pulse** — индикатор «живой поток»: расходящееся кольцо `box-shadow 0→7px`, 2.4s infinite.
- **Route reveal** — строки маршрута появляются по очереди (stagger 60ms), опц. лёгкий scan-хайлайт слева-направо.

### D. Правила
- Анимируем **только `transform` и `opacity`** — без reflow/CLS.
- Микро 150–300ms; входы ≤700ms; ambient — секунды. Ничего блокирующего ввод.
- `reduce`: `.reveal` сразу видимы; светлячки/blob/pulse/sway/count-up — **off**, финальные состояния статичны.
- Один «оркестрованный момент» на экран (hero-load) важнее десяти разрозненных.

---

## 6. Компоненты

- **Button.primary** — `background:--emerald; color:#04231a; radius:pill; padding:14px 26px; shadow:--glow-emerald`. Hover → `--moss` + lift + shine. Focus-visible: `outline:2px --emerald offset 3px`.
- **Button.ghost** — прозрачный, `border:1.5px --line-2; color:--ink`. Hover → border `--emerald` + `bg rgba(52,211,153,.06)`.
- **Card** — `--surface`, `1px --line`, `radius-lg`, hover lift + `--shadow-card`.
- **Plan** — как Card; `.feat` → `1.5px --emerald`, градиент `--raise→--surface`, emerald-glow, badge `--sun`. Кнопка выбора внизу. Дерево-иконка со sway на hover.
- **Route-row** — `bg rgba(255,255,255,.02)`, `1px --line`, `radius 15px`; иконка-квадрат (ru=emerald, exit=sun); tag pill (direct=emerald, exit=sun); мс справа tabular-nums.
- **Eyebrow** — pill, `bg rgba(52,211,153,.08)`, `1px rgba(52,211,153,.2)`, текст --emerald uppercase.
- **Иконки** — только SVG stroke (Lucide-стиль, width 1.6–2.2), **никаких emoji как иконок**. Единая толщина линии в слое. Деревья — линейные SVG (референс в превью), толщина 1.6.

---

## 7. Голос и копирайт (branding — обязательно)

**Тон:** спокойный, природный, уверенный. Приватность · скорость · умная маршрутизация.

**НИКОГДА не писать:** «обход блокировок», «обход DPI», «обход цензуры», «разблокировка», «оператор видит шум», названия ядер (VLESS/Shadowsocks/Xray/Reality/XHTTP) в UI. Продукт — **«прокси-эко»**, не «VPN».

**Замены-эталоны (утверждены):**
- обход/разблокировка → «доступ», «маршрутизация», «работает без сбоев»
- «протоколы обхода» → «протоколы подключения» / «каналы подключения»
- «маскировка / DPI не видит» → «неотличимо от обычного защищённого HTTPS», «спокойное соединение»
- «не блокируют» → «работают без сбоев и капчей»
- конкретные ядра → «современные протоколы»

CTA — глагол результата: «Подключиться», «Начать бесплатно», «Выбрать». Ошибки — причина + как исправить, без извинений.

---

## 8. Анти-паттерны (avoid)

- ❌ Кислотно-зелёный неон на чистом чёрном (клише). У нас — тёплая многослойная палитра + serif + органика.
- ❌ Пёстрый разнобой градиентов (pink/violet/amber/blue вперемешку) — как в старом `site/`. Одна система токенов.
- ❌ Emoji как иконки. ❌ Инлайн-hex в компонентах (только токены). ❌ `rounded-lg` на всём — форма по шкале §4.
- ❌ Анимация ради анимации; анимация width/height; отсутствие reduced-motion.
- ❌ Более одного primary-CTA в кадре.
- ❌ Тяжёлые drop-shadow как декор; свечение — только на действии.

---

## 9. Чеклист перед мержем страницы

- [ ] Только токены §2 (ни одного сырого hex)
- [ ] Lora/Raleway self-hosted; fallback-стек на месте
- [ ] Контраст ink/sub ≥ 4.5:1 проверен
- [ ] Все интерактивные ≥ 44px, focus-visible виден
- [ ] Motion §5 через `prefers-reduced-motion`-гейт; проверено с `reduce`
- [ ] Только transform/opacity в анимациях (нет CLS)
- [ ] Копирайт §7 — ноль «обхода/блокировок/DPI», ноль названий ядер
- [ ] Проверено на 375 / 768 / 1024 / 1440
- [ ] Один primary-CTA на экран
