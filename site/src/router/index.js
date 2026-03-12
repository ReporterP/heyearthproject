import { createRouter, createWebHistory } from 'vue-router'
import LandingPage from '../views/LandingPage.vue'

const routes = [
  { path: '/', name: 'home', component: LandingPage },
  {
    path: '/business',
    name: 'business',
    component: () => import('../views/BusinessPage.vue'),
  },
  {
    path: '/business/signup',
    name: 'business-signup',
    component: () => import('../views/BusinessSignupPage.vue'),
  },
]

const router = createRouter({
  history: createWebHistory('/promo/'),
  routes,
  scrollBehavior(_to, _from, savedPosition) {
    return savedPosition ?? { top: 0 }
  },
})

export default router
