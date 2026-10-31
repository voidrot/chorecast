<script setup lang="ts">
import { NAlert, NButton, NCard, NQrCode, NSelect, useMessage } from 'naive-ui'
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import { useAsync } from '@/composables/useAsync'
import { formatDateTime } from '@/format'

const { t } = useI18n()
const message = useMessage()
const role = ref<'parent' | 'owner'>('parent')
/** The link just created: shown once (the server keeps only a hash). */
const created = ref<{ url: string; role: string; expiresAt: string }>()

const roleOptions = [
  { value: 'parent', label: t('parents.roleParent') },
  { value: 'owner', label: t('parents.roleOwner') },
]

const invites = useAsync(async () => {
  const { data, error, response } = await api.GET('/invites')
  if (!data) throw ApiError.fromProblem(response.status, error)
  return data.items
})
onMounted(() => invites.run())

const create = useAsync(async () => {
  const { data, error, response } = await api.POST('/invites', { body: { role: role.value } })
  if (!data) throw ApiError.fromProblem(response.status, error)
  created.value = data
  await invites.run()
})
const revoke = useAsync(async (id: number) => {
  const { error, response } = await api.DELETE('/invites/{id}', { params: { path: { id } } })
  if (error) throw ApiError.fromProblem(response.status, error)
  await invites.run()
})

async function copy() {
  if (!created.value) return
  try {
    await navigator.clipboard.writeText(created.value.url)
    message.success(t('parents.copied'))
  } catch {
    message.error(t('parents.copyFailed'))
  }
}

const errorOf = () => invites.error.value ?? create.error.value ?? revoke.error.value
</script>

<template>
  <NCard :title="t('parents.invitesTitle')">
    <div class="flex flex-col gap-4">
      <NAlert v-if="errorOf()" type="error" :show-icon="false">{{ errorOf()?.detail }}</NAlert>
      <p class="text-sm text-gray-600 dark:text-neutral-400">{{ t('parents.invitesHint') }}</p>
      <div class="flex flex-wrap items-center gap-2">
        <NSelect
          v-model:value="role"
          :options="roleOptions"
          class="w-48"
          :aria-label="t('parents.role')"
        />
        <NButton type="primary" :loading="create.loading.value" @click="create.run()">
          {{ t('parents.createInvite') }}
        </NButton>
      </div>

      <div
        v-if="created"
        class="flex flex-col items-center gap-3 rounded-lg border border-gray-200 p-4 dark:border-neutral-700"
        data-testid="invite-link"
      >
        <div class="rounded-md bg-white p-2"><NQrCode :value="created.url" :size="180" /></div>
        <code class="break-all text-center font-mono text-sm">{{ created.url }}</code>
        <p class="text-sm text-gray-500 dark:text-neutral-400">
          {{ t('parents.linkHint', { when: formatDateTime(created.expiresAt) }) }}
        </p>
        <div class="flex gap-2">
          <NButton secondary @click="copy">{{ t('parents.copy') }}</NButton>
          <NButton quaternary @click="created = undefined">{{ t('parents.hideLink') }}</NButton>
        </div>
      </div>

      <div v-if="invites.data.value?.length">
        <h3 class="mb-1 font-medium">{{ t('parents.pending') }}</h3>
        <ul class="divide-y divide-gray-200 dark:divide-neutral-800">
          <li
            v-for="i in invites.data.value"
            :key="i.id"
            class="flex flex-wrap items-center justify-between gap-2 py-2 text-sm"
          >
            <span>
              {{ i.role === 'owner' ? t('parents.roleOwner') : t('parents.roleParent') }}
              ·
              {{ t('parents.expires', { when: formatDateTime(i.expiresAt) }) }}
              <template v-if="i.createdBy">
                · {{ t('parents.by', { name: i.createdBy }) }}</template
              >
            </span>
            <NButton
              size="tiny"
              secondary
              type="error"
              :loading="revoke.loading.value"
              @click="revoke.run(i.id)"
            >
              {{ t('parents.revoke') }}
            </NButton>
          </li>
        </ul>
      </div>
    </div>
  </NCard>
</template>
