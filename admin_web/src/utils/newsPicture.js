// The picture of a news post: a hedgehog scene (spec 12 §12.6), an uploaded image, or none.
// The API takes one of scene/image, never both.

export const NEWS_SCENES = [
  { slug: 'news', label: 'газета' },
  { slug: 'wave', label: 'машет лапой' },
  { slug: 'coin', label: 'монетка' },
  { slug: 'piggy', label: 'копилка' },
  { slug: 'gift', label: 'подарок' },
  { slug: 'alarm', label: 'будильник' },
  { slug: 'wallet', label: 'пустой кошелёк' },
  { slug: 'sleep', label: 'спит' },
  { slug: 'support', label: 'гарнитура' },
]

// Public pictures (scenes, uploaded news images) are served by the site's API: the admin's own
// domain (admin.<domain>) proxies only /api/admin/, so they load from the site's origin. Local dev
// (no admin. prefix) keeps relative paths.
export function publicOrigin(hostname = globalThis.location?.hostname || '', protocol = globalThis.location?.protocol || 'https:') {
  return hostname.startsWith('admin.') ? `${protocol}//${hostname.slice('admin.'.length)}` : ''
}

export const sceneURL = (slug, origin = publicOrigin()) => `${origin}/api/v1/tg/scenes/${slug}.gif`

export const newsImageURL = (name, origin = publicOrigin()) => (name ? `${origin}/api/v1/news/images/${name}` : '')

export function newsPicture(form) {
  if (form?.scene) return 'scene'
  if (form?.image) return 'image'
  return 'none'
}

// switchPicture moves the form to another kind of picture; only one of scene/image stays.
export function switchPicture(form, kind) {
  if (kind === 'scene') return { ...form, scene: form.scene || 'news', image: '' }
  if (kind === 'image') return { ...form, scene: '' }
  return { ...form, scene: '', image: '' }
}
