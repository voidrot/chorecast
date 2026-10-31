import { createRouter, createWebHistory } from 'vue-router'
import AppLayout from '@/layouts/AppLayout.vue'
import { useSessionStore } from '@/stores/session'
import HomeView from '@/views/HomeView.vue'
import { decide } from './guard'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('@/views/LoginView.vue'),
      meta: { public: true },
    },
    {
      path: '/setup',
      name: 'setup',
      component: () => import('@/views/SetupView.vue'),
      meta: { public: true },
    },
    {
      path: '/invite',
      name: 'invite',
      component: () => import('@/views/InviteView.vue'),
      meta: { public: true },
    },
    {
      path: '/',
      component: AppLayout,
      children: [
        { path: '', name: 'home', component: HomeView },
        {
          path: 'kids',
          name: 'kids',
          component: () => import('@/views/kids/KidsView.vue'),
        },
        {
          path: 'kids/new',
          name: 'kid-new',
          component: () => import('@/views/kids/KidEditView.vue'),
        },
        {
          path: 'kids/:kid',
          name: 'kid-edit',
          component: () => import('@/views/kids/KidEditView.vue'),
        },
        {
          path: 'parents',
          name: 'parents',
          component: () => import('@/views/parents/ParentsView.vue'),
        },
        {
          path: 'settings',
          name: 'settings',
          component: () => import('@/views/settings/SettingsView.vue'),
        },
        {
          path: 'account',
          name: 'account',
          component: () => import('@/views/account/AccountView.vue'),
        },
      ],
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: () => import('@/views/NotFoundView.vue'),
      meta: { public: true },
    },
  ],
})

router.beforeEach(async (to) => {
  const session = useSessionStore()
  if (session.status === 'unknown') await session.load()
  return decide(session.status, to)
})

export default router
