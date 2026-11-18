import { test } from 'node:test'
import assert from 'node:assert/strict'
import { NEWS_SCENES, newsImageURL, newsPicture, publicOrigin, sceneURL, switchPicture } from './newsPicture.js'

test('news picture: a scene, an own image or none', () => {
  assert.equal(newsPicture({ scene: 'news', image: '' }), 'scene')
  assert.equal(newsPicture({ scene: '', image: 'a.png' }), 'image')
  assert.equal(newsPicture({ scene: '', image: '' }), 'none')
  assert.equal(newsPicture({}), 'none')
})

test('switching the picture keeps only one of scene/image', () => {
  const withImage = { title: 't', scene: '', image: 'a.png' }
  assert.deepEqual(switchPicture(withImage, 'scene'), { title: 't', scene: 'news', image: '' })
  assert.deepEqual(switchPicture({ scene: 'gift', image: '' }, 'scene'), { scene: 'gift', image: '' })
  assert.deepEqual(switchPicture({ scene: 'gift', image: '' }, 'image'), { scene: '', image: '' })
  assert.deepEqual(switchPicture(withImage, 'image'), { title: 't', scene: '', image: 'a.png' })
  assert.deepEqual(switchPicture(withImage, 'none'), { title: 't', scene: '', image: '' })
})

test('the nine scenes, newspaper first, served by the API', () => {
  assert.deepEqual(NEWS_SCENES.map(s => s.slug), ['news', 'wave', 'coin', 'piggy', 'gift', 'alarm', 'wallet', 'sleep', 'support'])
  assert.equal(sceneURL('coin'), '/api/v1/tg/scenes/coin.gif')
})

test('public pictures come from the site: admin.<domain> proxies only /api/admin/', () => {
  assert.equal(publicOrigin('admin.hiearth.ru', 'https:'), 'https://hiearth.ru')
  assert.equal(publicOrigin('localhost', 'http:'), '')
  assert.equal(publicOrigin('127.0.0.1', 'http:'), '')
  assert.equal(sceneURL('coin', 'https://hiearth.ru'), 'https://hiearth.ru/api/v1/tg/scenes/coin.gif')
  assert.equal(sceneURL('coin', ''), '/api/v1/tg/scenes/coin.gif')
  assert.equal(newsImageURL('a1.png', 'https://hiearth.ru'), 'https://hiearth.ru/api/v1/news/images/a1.png')
  assert.equal(newsImageURL('', 'https://hiearth.ru'), '')
})
