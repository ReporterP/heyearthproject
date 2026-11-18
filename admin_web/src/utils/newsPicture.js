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

export const sceneURL = (slug) => `/api/v1/tg/scenes/${slug}.gif`

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
