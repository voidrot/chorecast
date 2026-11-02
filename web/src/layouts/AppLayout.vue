<script setup lang="ts">
import { NBadge, NButton, NTag } from 'naive-ui'
import { onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { householdTimeZone } from '@/format'
import { useReviewQueueStore } from '@/stores/reviewQueue'
import { useSessionStore } from '@/stores/session'

const { t } = useI18n()
const router = useRouter()
const route = useRoute()
const session = useSessionStore()
const reviewQueue = useReviewQueueStore()

// Show dates in household time everywhere in the admin.
onMounted(async () => {
  const { data } = await api.GET('/household')
  if (data) householdTimeZone.value = data.timezone
})

// The review badge refreshes on first load and on every navigation.
watch(
  () => route.name,
  () => void reviewQueue.refresh(),
  { immediate: true },
)

async function signOut() {
  await session.logout()
  await router.replace({ name: 'login' })
}
</script>

<template>
  <div class="min-h-screen">
    <header
      class="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 border-b border-gray-200 bg-white px-4 py-3 dark:border-neutral-800 dark:bg-neutral-900"
    >
      <div class="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-2">
        <RouterLink to="/" class="font-bold">{{ t('app.name') }}</RouterLink>
        <nav class="flex flex-wrap gap-x-4 gap-y-1">
          <RouterLink :to="{ name: 'home' }" exact-active-class="font-semibold">{{
            t('nav.home')
          }}</RouterLink>
          <RouterLink :to="{ name: 'today' }" active-class="font-semibold">{{
            t('nav.today')
          }}</RouterLink>
          <RouterLink :to="{ name: 'chores' }" active-class="font-semibold">{{
            t('nav.chores')
          }}</RouterLink>
          <RouterLink
            :to="{ name: 'review' }"
            active-class="font-semibold"
            :aria-label="
              reviewQueue.badge ? t('nav.reviewLabel', { n: reviewQueue.badge }) : undefined
            "
          >
            {{ t('nav.review') }}
            <NBadge v-if="reviewQueue.badge" :value="reviewQueue.badge" :offset="[4, -6]" />
          </RouterLink>
          <RouterLink :to="{ name: 'kids' }" active-class="font-semibold">{{
            t('nav.kids')
          }}</RouterLink>
          <RouterLink :to="{ name: 'devices' }" exact-active-class="font-semibold">{{
            t('nav.devices')
          }}</RouterLink>
          <RouterLink :to="{ name: 'parents' }" exact-active-class="font-semibold">{{
            t('nav.parents')
          }}</RouterLink>
          <RouterLink :to="{ name: 'settings' }" exact-active-class="font-semibold">{{
            t('nav.settings')
          }}</RouterLink>
          <RouterLink
            v-if="session.isOwner"
            :to="{ name: 'audit' }"
            exact-active-class="font-semibold"
            >{{
              t('nav.audit')
            }}</RouterLink
          >
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
    <main class="mx-auto max-w-5xl px-3 py-4 sm:px-4 sm:py-6">
      <RouterView />
    </main>
  </div>
</template>
