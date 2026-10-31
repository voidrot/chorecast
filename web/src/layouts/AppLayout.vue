<script setup lang="ts">
import { NButton, NTag } from 'naive-ui'
import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, RouterView, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { householdTimeZone } from '@/format'
import { useSessionStore } from '@/stores/session'

const { t } = useI18n()
const router = useRouter()
const session = useSessionStore()

// Show dates in household time everywhere in the admin.
onMounted(async () => {
  const { data } = await api.GET('/household')
  if (data) householdTimeZone.value = data.timezone
})

async function signOut() {
  await session.logout()
  await router.replace({ name: 'login' })
}
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
          <RouterLink :to="{ name: 'kids' }" active-class="font-semibold">{{
            t('nav.kids')
          }}</RouterLink>
          <RouterLink :to="{ name: 'parents' }" exact-active-class="font-semibold">{{
            t('nav.parents')
          }}</RouterLink>
          <RouterLink :to="{ name: 'settings' }" exact-active-class="font-semibold">{{
            t('nav.settings')
          }}</RouterLink>
        </nav>
      </div>
      <div class="flex items-center gap-3">
        <RouterLink :to="{ name: 'account' }" exact-active-class="font-semibold" class="text-sm">
          {{ session.me?.displayName ?? t('nav.account') }}
        </RouterLink>
        <NTag size="small" :bordered="false">
          {{ session.isOwner ? t('session.owner') : t('session.parent') }}
        </NTag>
        <NButton size="small" quaternary @click="signOut">{{ t('nav.signOut') }}</NButton>
      </div>
    </header>
    <main class="mx-auto max-w-5xl px-4 py-6">
      <RouterView />
    </main>
  </div>
</template>
