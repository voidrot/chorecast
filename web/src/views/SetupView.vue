<script setup lang="ts">
import Card from 'primevue/card'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <main class="center">
    <Card class="card">
      <template #title>{{ t('setup.title') }}</template>
      <template #content>
        <p>{{ t('setup.body') }}</p>
      </template>
    </Card>
  </main>
</template>

<style scoped>
.center {
  display: grid;
  place-items: center;
  min-height: 100vh;
  padding: 1rem;
  font-family: system-ui, sans-serif;
}
.card {
  width: min(32rem, 100%);
}
</style>
