<script setup lang="ts">
import Card from 'primevue/card'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import { useAsync } from '@/composables/useAsync'

const { t } = useI18n()

const server = useAsync(async () => {
  const [health, version] = await Promise.all([api.GET('/health'), api.GET('/version')])
  if (!version.data) throw ApiError.fromProblem(version.response.status, version.error)
  return { status: health.data?.status ?? 'unreachable', version: version.data.version }
})

onMounted(() => server.run())
</script>

<template>
  <h1>{{ t('home.title') }}</h1>
  <Message v-if="server.error.value" severity="error">{{ server.error.value.detail }}</Message>
  <Card v-else-if="server.data.value">
    <template #title>{{ t('home.server') }}</template>
    <template #content>
      <p>
        <Tag
          :value="t(`home.status.${server.data.value.status}`)"
          :severity="server.data.value.status === 'ok' ? 'success' : 'warn'"
        />
      </p>
      <p>{{ t('home.version', { version: server.data.value.version }) }}</p>
    </template>
  </Card>
</template>
