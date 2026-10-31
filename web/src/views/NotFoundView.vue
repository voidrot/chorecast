<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

const { t } = useI18n()
</script>

<template>
  <main class="center">
    <h1>{{ t('notFound.title') }}</h1>
    <RouterLink to="/">{{ t('notFound.back') }}</RouterLink>
  </main>
</template>

<style scoped>
.center {
  display: grid;
  place-items: center;
  align-content: center;
  gap: 1rem;
  min-height: 100vh;
  font-family: system-ui, sans-serif;
}
</style>
