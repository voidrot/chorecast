<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useSessionStore } from '@/stores/session'
import InvitesCard from './InvitesCard.vue'
import ParentsCard from './ParentsCard.vue'

const { t } = useI18n()
const session = useSessionStore()
</script>

<template>
  <div class="flex flex-col gap-6">
    <h1 class="text-2xl font-semibold">{{ t('parents.title') }}</h1>
    <ParentsCard />
    <InvitesCard v-if="session.isOwner" />
  </div>
</template>
