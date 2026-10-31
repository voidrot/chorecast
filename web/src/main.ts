import Aura from '@primeuix/themes/aura'
import { createPinia } from 'pinia'
import PrimeVue from 'primevue/config'
import { createApp } from 'vue'
import 'primeicons/primeicons.css'

import App from './App.vue'
import { apiEvents } from './api/client'
import { i18n } from './i18n'
import router from './router'
import { useSessionStore } from './stores/session'

const app = createApp(App)
  .use(createPinia())
  .use(router)
  .use(i18n)
  .use(PrimeVue, { theme: { preset: Aura } })

const session = useSessionStore()
// Only a session that was valid and expired mid-use is handled here; on the first load the router
// guard decides where to go.
apiEvents.onUnauthorized = () => {
  if (session.status === 'authenticated' || session.status === 'forbidden') {
    session.signedOut()
    void router.push({ name: 'login', query: { redirect: router.currentRoute.value.fullPath } })
  }
}
// Likewise: during the first load the guard routes to setup itself, keeping the link's token.
apiEvents.onSetupRequired = () => {
  if (session.status === 'unknown' || session.status === 'setup_required') return
  session.status = 'setup_required'
  void router.push({ name: 'setup' })
}

app.mount('#app')
