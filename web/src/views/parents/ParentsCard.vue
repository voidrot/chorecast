<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCard,
  NDropdown,
  NModal,
  NSpin,
  NTag,
  useDialog,
  useMessage,
} from 'naive-ui'
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import type { components } from '@/api/schema'
import { useAsync } from '@/composables/useAsync'
import { useSessionStore } from '@/stores/session'

type Parent = components['schemas']['Parent']

const { t } = useI18n()
const dialog = useDialog()
const message = useMessage()
const session = useSessionStore()
const tempPassword = ref<{ name: string; password: string }>()

const parents = useAsync(async () => {
  const { data, error, response } = await api.GET('/parents')
  if (!data) throw ApiError.fromProblem(response.status, error)
  return data.items
})
onMounted(() => parents.run())

const act = useAsync(async (fn: () => Promise<{ error?: unknown; response: Response }>) => {
  const { error, response } = await fn()
  if (error) throw ApiError.fromProblem(response.status, error as never)
  await parents.run()
  await session.load() // our own role may have changed
})

function options(p: Parent) {
  return [
    { key: 'role', label: p.owner ? t('parents.makeParent') : t('parents.makeOwner') },
    { key: 'reset', label: t('parents.resetPassword'), disabled: !p.hasPassword && !p.username },
    { key: 'totp', label: t('parents.disableTotp'), disabled: !p.totpEnabled },
    { type: 'divider', key: 'd' },
    {
      key: 'remove',
      label: t('parents.remove'),
      props: { style: 'color: var(--n-option-text-color-error, #d03050)' },
    },
  ]
}

function onSelect(p: Parent, key: string) {
  const path = { params: { path: { id: p.id } } }
  switch (key) {
    case 'role':
      void act.run(() => api.PUT('/parents/{id}/role', { ...path, body: { owner: !p.owner } }))
      break
    case 'totp':
      dialog.warning({
        title: t('parents.disableTotp'),
        content: t('parents.disableTotpConfirm', { name: p.displayName }),
        positiveText: t('parents.disableTotp'),
        negativeText: t('account.cancel'),
        onPositiveClick: () =>
          act
            .run(() => api.POST('/parents/{id}/disable-totp', path))
            .then(() => {
              if (!act.error.value)
                message.success(t('parents.totpDisabled', { name: p.displayName }))
            }),
      })
      break
    case 'reset':
      dialog.warning({
        title: t('parents.resetPassword'),
        content: t('parents.resetConfirm', { name: p.displayName }),
        positiveText: t('parents.resetPassword'),
        negativeText: t('account.cancel'),
        onPositiveClick: async () => {
          const { data, error, response } = await api.POST('/parents/{id}/reset-password', path)
          if (!data) {
            act.error.value = ApiError.fromProblem(response.status, error)
            return
          }
          tempPassword.value = { name: p.displayName, password: data.password }
        },
      })
      break
    case 'remove':
      dialog.error({
        title: t('parents.remove'),
        content: t('parents.removeConfirm', { name: p.displayName }),
        positiveText: t('parents.remove'),
        negativeText: t('account.cancel'),
        onPositiveClick: () => act.run(() => api.DELETE('/parents/{id}', path)),
      })
      break
  }
}
</script>

<template>
  <NCard :title="t('parents.listTitle')">
    <NAlert
      v-if="parents.error.value || act.error.value"
      type="error"
      class="mb-4"
      :show-icon="false"
    >
      {{ (act.error.value ?? parents.error.value)?.detail }}
    </NAlert>
    <NSpin v-if="parents.loading.value && !parents.data.value" />
    <ul v-else class="divide-y divide-gray-200 dark:divide-neutral-800">
      <li
        v-for="p in parents.data.value ?? []"
        :key="p.id"
        class="flex flex-wrap items-center justify-between gap-2 py-3"
        data-testid="parent-row"
      >
        <div>
          <div class="flex flex-wrap items-center gap-2 font-medium">
            {{ p.displayName }}
            <NTag v-if="p.owner" size="small" type="success" :bordered="false">{{
              t('session.owner')
            }}</NTag>
            <NTag v-if="p.id === session.me?.userId" size="small" :bordered="false">{{
              t('parents.you')
            }}</NTag>
            <NTag v-if="p.totpEnabled" size="small" :bordered="false">{{
              t('parents.twoFactor')
            }}</NTag>
            <NTag v-if="p.oidcLinked" size="small" :bordered="false">{{ t('parents.sso') }}</NTag>
          </div>
          <div class="text-sm text-gray-500 dark:text-neutral-400">
            {{ p.username ? `@${p.username}` : t('parents.ssoOnly') }}
          </div>
        </div>
        <NDropdown
          v-if="session.isOwner"
          trigger="click"
          :options="options(p)"
          @select="(key: string) => onSelect(p, key)"
        >
          <NButton size="small" secondary :loading="act.loading.value">{{
            t('parents.manage')
          }}</NButton>
        </NDropdown>
      </li>
    </ul>

    <NModal
      :show="!!tempPassword"
      preset="dialog"
      type="info"
      :title="t('parents.tempTitle')"
      :positive-text="t('parents.tempDone')"
      @positive-click="tempPassword = undefined"
      @close="tempPassword = undefined"
    >
      <p class="mb-3">{{ t('parents.tempBody', { name: tempPassword?.name }) }}</p>
      <code
        class="block rounded-md bg-gray-100 p-3 text-center font-mono text-lg dark:bg-neutral-800"
        data-testid="temp-password"
      >
        {{ tempPassword?.password }}
      </code>
    </NModal>
  </NCard>
</template>
