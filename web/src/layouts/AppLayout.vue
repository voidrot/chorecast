<script setup lang="ts">
import Tag from 'primevue/tag'
import Toolbar from 'primevue/toolbar'
import { useI18n } from 'vue-i18n'
import { RouterLink, RouterView } from 'vue-router'
import { useSessionStore } from '@/stores/session'

const { t } = useI18n()
const session = useSessionStore()
</script>

<template>
  <div class="layout">
    <Toolbar class="toolbar">
      <template #start>
        <RouterLink to="/" class="brand">{{ t('app.name') }}</RouterLink>
        <nav class="nav">
          <RouterLink :to="{ name: 'home' }">{{ t('nav.home') }}</RouterLink>
        </nav>
      </template>
      <template #end>
        <Tag
          :value="session.isOwner ? t('session.owner') : t('session.parent')"
          severity="secondary"
        />
      </template>
    </Toolbar>
    <main class="content">
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.layout {
  min-height: 100vh;
  font-family: system-ui, sans-serif;
}
.toolbar {
  border-radius: 0;
  border-width: 0 0 1px;
}
.brand {
  font-weight: 700;
  margin-right: 1.5rem;
  color: inherit;
  text-decoration: none;
}
.nav a {
  margin-right: 1rem;
  color: inherit;
}
.nav a.router-link-exact-active {
  font-weight: 600;
}
.content {
  max-width: 64rem;
  margin: 0 auto;
  padding: 1.5rem 1rem;
}
</style>
