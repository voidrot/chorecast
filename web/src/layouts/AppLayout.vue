<script setup lang="ts">
import { NTag } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { RouterLink, RouterView } from 'vue-router'
import { useSessionStore } from '@/stores/session'

const { t } = useI18n()
const session = useSessionStore()
</script>

<template>
  <div class="min-h-screen">
    <header
      class="flex items-center justify-between border-b border-gray-200 bg-white px-4 py-3 dark:border-neutral-800 dark:bg-neutral-900"
    >
      <div class="flex items-center gap-6">
        <RouterLink to="/" class="font-bold">{{ t('app.name') }}</RouterLink>
        <nav class="flex gap-4">
          <RouterLink :to="{ name: 'home' }" exact-active-class="font-semibold">{{
            t('nav.home')
          }}</RouterLink>
        </nav>
      </div>
      <NTag size="small" :bordered="false">
        {{ session.isOwner ? t('session.owner') : t('session.parent') }}
      </NTag>
    </header>
    <main class="mx-auto max-w-5xl px-4 py-6">
      <RouterView />
    </main>
  </div>
</template>
