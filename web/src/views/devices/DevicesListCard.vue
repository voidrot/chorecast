<script setup lang="ts">
import { NAlert, NButton, NCard, NSpin, NTag, useDialog, useMessage } from 'naive-ui'
import { onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import type { components } from '@/api/schema'
import { useAsync } from '@/composables/useAsync'
import { formatDateTime } from '@/format'

type Listed = components['schemas']['ListedDevice']

const { t } = useI18n()
const dialog = useDialog()
const message = useMessage()

const devices = useAsync(async () => {
  const { data, error, response } = await api.GET('/devices')
  if (!data) throw ApiError.fromProblem(response.status, error)
  return data.items
})
// Newly paired devices and open sessions show up without a reload.
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void devices.run()
  timer = setInterval(() => void devices.run(), 15000)
})
onUnmounted(() => clearInterval(timer))

const act = useAsync(async (fn: () => Promise<{ error?: unknown; response: Response }>) => {
  const { error, response } = await fn()
  if (error) throw ApiError.fromProblem(response.status, error as never)
  await devices.run()
})

function lock(d: Listed) {
  void act
    .run(() => api.POST('/devices/{id}/lock', { params: { path: { id: d.id } } }))
    .then(() => {
      if (!act.error.value) message.success(t('devices.locked', { name: d.name }))
    })
}

function revoke(d: Listed) {
  dialog.warning({
    title: t('devices.revoke'),
    content: t('devices.revokeConfirm', { name: d.name }),
    positiveText: t('devices.revoke'),
    negativeText: t('account.cancel'),
    onPositiveClick: () =>
      act.run(() => api.DELETE('/devices/{id}', { params: { path: { id: d.id } } })),
  })
}
</script>

<template>
  <NCard :title="t('devices.listTitle')">
    <NAlert
      v-if="devices.error.value || act.error.value"
      type="error"
      :show-icon="false"
      class="mb-4"
    >
      {{ (act.error.value ?? devices.error.value)?.detail }}
    </NAlert>
    <NSpin v-if="devices.loading.value && !devices.data.value" />
    <p v-else-if="!devices.data.value?.length" class="text-gray-600 dark:text-neutral-400">
      {{ t('devices.none') }}
    </p>
    <ul v-else class="divide-y divide-gray-200 dark:divide-neutral-800">
      <li
        v-for="d in devices.data.value"
        :key="d.id"
        class="flex flex-wrap items-center justify-between gap-3 py-3"
        data-testid="device-row"
      >
        <div class="min-w-0">
          <div class="flex flex-wrap items-center gap-2 font-medium">
            {{ d.name }}
            <NTag size="small" :bordered="false">
              {{ d.shared ? t('devices.shared') : t('devices.forKid', { name: d.kidName }) }}
            </NTag>
            <NTag v-if="d.sessionUser" size="small" type="success" :bordered="false">
              {{ t('devices.inUse', { name: d.sessionUser }) }}
            </NTag>
          </div>
          <div class="text-sm text-gray-500 dark:text-neutral-400">
            {{ t('devices.lastSeen', { when: formatDateTime(d.lastSeen) }) }}
            <template v-if="d.appVersion">
              · {{ t('devices.version', { version: d.appVersion }) }}</template
            >
          </div>
        </div>
        <div class="flex gap-2">
          <NButton
            v-if="d.shared"
            size="small"
            secondary
            :disabled="!d.sessionUser"
            @click="lock(d)"
          >
            {{ t('devices.lock') }}
          </NButton>
          <NButton size="small" secondary type="error" @click="revoke(d)">{{
            t('devices.revoke')
          }}</NButton>
        </div>
      </li>
    </ul>
    <p class="mt-3 text-sm text-gray-500 dark:text-neutral-400">{{ t('devices.listHint') }}</p>
  </NCard>
</template>
