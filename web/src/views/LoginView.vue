<script setup lang="ts">
import Card from 'primevue/card'
import Message from 'primevue/message'
import { useI18n } from 'vue-i18n'
import { useSessionStore } from '@/stores/session'

const { t } = useI18n()
const session = useSessionStore()
</script>

<template>
  <main class="center">
    <Card class="card">
      <template #title>{{ t('login.title') }}</template>
      <template #content>
        <Message v-if="session.status === 'unknown' && session.error" severity="error">
          {{ t('login.unreachable', { detail: session.error.detail }) }}
        </Message>
        <Message v-else-if="session.status === 'forbidden'" severity="warn">{{
          t('login.notParent')
        }}</Message>
        <p>{{ t('login.comingSoon') }}</p>
      </template>
    </Card>
  </main>
</template>

<style scoped>
.center {
  display: grid;
  place-items: center;
  min-height: 100vh;
  padding: 1rem;
  font-family: system-ui, sans-serif;
}
.card {
  width: min(28rem, 100%);
}
</style>
