<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useSessionStore } from '@/stores/session'
import HouseholdCard from './HouseholdCard.vue'
import ServerCard from './ServerCard.vue'

const { t } = useI18n()
const session = useSessionStore()
</script>

<template>
  <div class="flex flex-col gap-6">
    <h1 class="text-2xl font-semibold">{{ t('settings.title') }}</h1>
    <HouseholdCard />
    <ServerCard v-if="session.isOwner" />
  </div>
</template>
