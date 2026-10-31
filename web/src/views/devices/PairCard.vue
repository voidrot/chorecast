<script setup lang="ts">
import { NAlert, NButton, NCard, NQrCode, NSelect } from 'naive-ui'
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import type { components } from '@/api/schema'
import { useAsync } from '@/composables/useAsync'
import { isLocalServer, secondsLeft } from '@/devices/pairing'

type Pairing = components['schemas']['DevicePairing']

const { t } = useI18n()
const target = ref<string>('shared')
const pairing = ref<Pairing>()
const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | undefined

const kids = useAsync(async () => {
  const { data, error, response } = await api.GET('/kids')
  if (!data) throw ApiError.fromProblem(response.status, error)
  return data.items
})
onMounted(() => {
  void kids.run()
  timer = setInterval(() => (now.value = Date.now()), 1000)
})
onUnmounted(() => clearInterval(timer))

const options = computed(() => [
  { value: 'shared', label: t('devices.shared') },
  ...(kids.data.value ?? []).map((k) => ({
    value: k.slug,
    label: t('devices.forKid', { name: k.displayName }),
  })),
])

const create = useAsync(async () => {
  const { data, error, response } = await api.POST('/devices/pairing', {
    body: target.value === 'shared' ? {} : { kid: target.value },
  })
  if (!data) throw ApiError.fromProblem(response.status, error)
  pairing.value = data
})

const remaining = computed(() =>
  pairing.value ? secondsLeft(pairing.value.expiresAt, now.value) : 0,
)
const countdown = computed(() => {
  const s = remaining.value
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
})
</script>

<template>
  <NCard :title="t('devices.pairTitle')">
    <div class="flex flex-col gap-4">
      <p class="text-sm text-gray-600 dark:text-neutral-400">{{ t('devices.pairHint') }}</p>
      <div class="flex flex-wrap items-center gap-2">
        <NSelect
          v-model:value="target"
          :options="options"
          class="w-72"
          :aria-label="t('devices.pairFor')"
        />
        <NButton type="primary" :loading="create.loading.value" @click="create.run()">
          {{ t('devices.pair') }}
        </NButton>
      </div>
      <NAlert v-if="create.error.value" type="error" :show-icon="false">{{
        create.error.value.detail
      }}</NAlert>

      <div
        v-if="pairing"
        class="flex flex-col items-center gap-3 rounded-lg border border-gray-200 p-4 dark:border-neutral-700"
        data-testid="pairing"
      >
        <template v-if="remaining > 0">
          <div class="rounded-md bg-white p-2"><NQrCode :value="pairing.qr" :size="200" /></div>
          <div class="text-center">
            <div class="text-sm text-gray-500 dark:text-neutral-400">{{ t('devices.orType') }}</div>
            <div class="font-mono text-2xl tracking-widest" data-testid="pairing-code">
              {{ pairing.code }}
            </div>
            <div class="text-sm text-gray-500 dark:text-neutral-400">
              {{ t('devices.server', { url: pairing.server }) }}
            </div>
          </div>
          <p class="text-sm">{{ t('devices.expiresIn', { time: countdown }) }}</p>
          <NAlert v-if="isLocalServer(pairing.server)" type="warning" :show-icon="false">
            {{ t('devices.localWarning') }}
          </NAlert>
        </template>
        <template v-else>
          <p>{{ t('devices.expired') }}</p>
          <NButton @click="create.run()">{{ t('devices.newCode') }}</NButton>
        </template>
      </div>
    </div>
  </NCard>
</template>
