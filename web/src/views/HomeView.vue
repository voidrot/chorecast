<script setup lang="ts">
import Card from 'primevue/card'
import Tag from 'primevue/tag'
import { onMounted, ref } from 'vue'
import { api } from '@/api/client'

const version = ref<string>()
const status = ref<string>('checking…')

onMounted(async () => {
  const [health, ver] = await Promise.all([api.GET('/health'), api.GET('/version')])
  status.value = health.data?.status ?? 'unreachable'
  version.value = ver.data?.version
})
</script>

<template>
  <main class="home">
    <Card>
      <template #title>Chorecast</template>
      <template #content>
        <p>Server: <Tag :value="status" :severity="status === 'ok' ? 'success' : 'warn'" /></p>
        <p v-if="version">Version {{ version }}</p>
      </template>
    </Card>
  </main>
</template>

<style scoped>
.home {
  max-width: 40rem;
  margin: 4rem auto;
  font-family: system-ui, sans-serif;
}
</style>
