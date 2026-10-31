<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useServerInfo } from '@/composables/useServerInfo'
import { useSessionStore } from '@/stores/session'
import PasswordCard from './PasswordCard.vue'
import PinCard from './PinCard.vue'
import SessionsCard from './SessionsCard.vue'
import SsoCard from './SsoCard.vue'
import TotpCard from './TotpCard.vue'

const { t } = useI18n()
const session = useSessionStore()
const server = useServerInfo()
</script>

<template>
  <div class="flex flex-col gap-6">
    <div>
      <h1 class="text-2xl font-semibold">{{ t('account.title') }}</h1>
      <p v-if="session.me" class="text-gray-600 dark:text-neutral-400">
        {{
          t('account.signedInAs', { name: session.me.displayName, username: session.me.username })
        }}
      </p>
    </div>
    <div class="grid gap-6 lg:grid-cols-2">
      <TotpCard />
      <PasswordCard />
      <PinCard />
      <SsoCard v-if="server?.oidc || session.me?.oidcLinked" />
      <SessionsCard class="lg:col-span-2" />
    </div>
  </div>
</template>
