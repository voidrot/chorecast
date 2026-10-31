<script setup lang="ts">
import { NAlert, NCard } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { useSessionStore } from '@/stores/session'

const { t } = useI18n()
const session = useSessionStore()
</script>

<template>
  <main class="grid min-h-screen place-items-center p-4">
    <NCard :title="t('login.title')" class="w-full max-w-md">
      <div class="flex flex-col gap-4">
        <NAlert v-if="session.status === 'unknown' && session.error" type="error">
          {{ t('login.unreachable', { detail: session.error.detail }) }}
        </NAlert>
        <NAlert v-else-if="session.status === 'forbidden'" type="warning">
          {{ t('login.notParent') }}
        </NAlert>
        <p>{{ t('login.comingSoon') }}</p>
      </div>
    </NCard>
  </main>
</template>
