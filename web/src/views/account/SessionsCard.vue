<script setup lang="ts">
import { NAlert, NButton, NCard, NSpin, NTag } from 'naive-ui'
import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import { useAsync } from '@/composables/useAsync'
import { formatDateTime } from '@/format'

const { t } = useI18n()

const sessions = useAsync(async () => {
  const { data, error, response } = await api.GET('/account/sessions')
  if (!data) throw ApiError.fromProblem(response.status, error)
  return data.items
})
const revoke = useAsync(async (id: number) => {
  const { error, response } = await api.DELETE('/account/sessions/{id}', {
    params: { path: { id } },
  })
  if (error) throw ApiError.fromProblem(response.status, error)
  await sessions.run()
})

onMounted(() => sessions.run())
</script>

<template>
  <NCard :title="t('account.sessions.title')">
    <NAlert v-if="sessions.error.value || revoke.error.value" type="error" class="mb-4">
      {{ (sessions.error.value ?? revoke.error.value)?.detail }}
    </NAlert>
    <NSpin v-if="sessions.loading.value && !sessions.data.value" />
    <ul v-else class="divide-y divide-gray-200 dark:divide-neutral-800">
      <li
        v-for="s in sessions.data.value ?? []"
        :key="s.id"
        class="flex flex-wrap items-center justify-between gap-2 py-3"
      >
        <div>
          <div class="flex items-center gap-2 font-medium">
            {{ s.label || t(`account.sessions.kind.${s.kind}`) }}
            <NTag size="small" :bordered="false">{{ t(`account.sessions.kind.${s.kind}`) }}</NTag>
            <NTag v-if="s.current" size="small" type="success" :bordered="false">
              {{ t('account.sessions.current') }}
            </NTag>
          </div>
          <div class="text-sm text-gray-500 dark:text-neutral-400">
            {{
              s.lastUsedAt
                ? t('account.sessions.lastUsed', { when: formatDateTime(s.lastUsedAt) })
                : t('account.sessions.created', { when: formatDateTime(s.createdAt) })
            }}
          </div>
        </div>
        <NButton
          v-if="!s.current"
          size="small"
          secondary
          type="error"
          :loading="revoke.loading.value"
          @click="revoke.run(s.id)"
        >
          {{ t('account.sessions.revoke') }}
        </NButton>
      </li>
    </ul>
  </NCard>
</template>
