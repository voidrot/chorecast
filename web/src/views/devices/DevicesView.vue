<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import DevicesListCard from './DevicesListCard.vue'
import PairCard from './PairCard.vue'

const { t } = useI18n()
</script>

<template>
  <div class="flex flex-col gap-6">
    <h1 class="text-2xl font-semibold">{{ t('devices.title') }}</h1>
    <DevicesListCard />
    <PairCard />
  </div>
</template>
