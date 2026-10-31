<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

const { t } = useI18n()
</script>

<template>
  <main class="grid min-h-screen place-content-center place-items-center gap-4">
    <h1 class="text-2xl font-semibold">{{ t('notFound.title') }}</h1>
    <RouterLink to="/" class="text-emerald-600 underline dark:text-emerald-400">{{
      t('notFound.back')
    }}</RouterLink>
  </main>
</template>
