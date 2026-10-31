<script setup lang="ts">
import { NAlert, NCard, NTag } from 'naive-ui'
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
  <h1 class="mb-4 text-2xl font-semibold">{{ t('home.title') }}</h1>
  <NAlert v-if="server.error.value" type="error">{{ server.error.value.detail }}</NAlert>
  <NCard v-else-if="server.data.value" :title="t('home.server')" class="max-w-md">
    <div class="flex flex-col gap-2">
      <div>
        <NTag :type="server.data.value.status === 'ok' ? 'success' : 'warning'" :bordered="false">
          {{ t(`home.status.${server.data.value.status}`) }}
        </NTag>
      </div>
      <p>{{ t('home.version', { version: server.data.value.version }) }}</p>
    </div>
  </NCard>
</template>
