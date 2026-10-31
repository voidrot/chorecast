<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import CatalogCard from './CatalogCard.vue'

const { t } = useI18n()
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-semibold">{{ t('chores.catalog') }}</h1>
      <RouterLink :to="{ name: 'chores' }" class="text-sm underline">{{
        t('chores.backToChores')
      }}</RouterLink>
    </div>
    <div class="grid gap-6 lg:grid-cols-2">
      <CatalogCard kind="locations" />
      <CatalogCard kind="equipment" />
    </div>
  </div>
</template>
