<script setup lang="ts">
import { NAlert, NCard, NSpin, NTag } from 'naive-ui'
import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import { useAsync } from '@/composables/useAsync'

const { t, te } = useI18n()
const server = useAsync(async () => {
  const { data, error, response } = await api.GET('/server/settings')
  if (!data) throw ApiError.fromProblem(response.status, error)
  return data
})
onMounted(() => server.run())
</script>

<template>
  <NCard :title="t('settings.server')">
    <NSpin v-if="server.loading.value && !server.data.value" />
    <NAlert v-else-if="server.error.value" type="error" :show-icon="false">{{
      server.error.value.detail
    }}</NAlert>
    <template v-else-if="server.data.value">
      <p class="mb-3 text-sm text-gray-600 dark:text-neutral-400">
        {{ t('settings.serverHint', { version: server.data.value.version }) }}
      </p>
      <dl class="divide-y divide-gray-200 dark:divide-neutral-800">
        <div
          v-for="s in server.data.value.items"
          :key="s.key"
          class="grid gap-1 py-2 sm:grid-cols-[14rem_1fr_auto] sm:items-center sm:gap-4"
        >
          <dt class="font-medium">
            {{ te(`settings.keys.${s.key}`) ? t(`settings.keys.${s.key}`) : s.key }}
          </dt>
          <dd class="min-w-0 break-all font-mono text-sm">
            <template v-if="s.secret">{{
              s.source === 'environment' ? t('settings.secretSet') : t('settings.notSet')
            }}</template>
            <template v-else>{{ s.value || t('settings.notSet') }}</template>
          </dd>
          <dd>
            <NTag v-if="s.source === 'environment'" size="small" :bordered="false" :title="s.env">
              {{ t('settings.setByEnv') }}
            </NTag>
            <code v-else class="text-xs text-gray-500 dark:text-neutral-400">{{ s.env }}</code>
          </dd>
        </div>
      </dl>
    </template>
  </NCard>
</template>
