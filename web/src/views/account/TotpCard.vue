<script setup lang="ts">
import { NAlert, NButton, NCard, NFormItem, NInput, NQrCode, NTag } from 'naive-ui'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import { useAsync } from '@/composables/useAsync'
import { useSessionStore } from '@/stores/session'

const { t } = useI18n()
const session = useSessionStore()

const enrolment = ref<{ secret: string; otpauthUrl: string }>()
const code = ref('')
const password = ref('')
/** Recovery codes, shown once after enabling or regenerating. */
const recoveryCodes = ref<string[]>()

const begin = useAsync(async () => {
  const { data, error, response } = await api.POST('/account/totp/setup')
  if (!data) throw ApiError.fromProblem(response.status, error)
  enrolment.value = data
  code.value = ''
})
const enable = useAsync(async () => {
  const { data, error, response } = await api.POST('/account/totp/enable', {
    body: { code: code.value },
  })
  if (!data) throw ApiError.fromProblem(response.status, error)
  recoveryCodes.value = data.recoveryCodes
  enrolment.value = undefined
  await session.load()
})
const regenerate = useAsync(async () => {
  const { data, error, response } = await api.POST('/account/recovery-codes', {
    body: { password: password.value },
  })
  if (!data) throw ApiError.fromProblem(response.status, error)
  recoveryCodes.value = data.recoveryCodes
  password.value = ''
})
const disable = useAsync(async () => {
  const { error, response } = await api.POST('/account/totp/disable', {
    body: { password: password.value },
  })
  if (error) throw ApiError.fromProblem(response.status, error)
  password.value = ''
  recoveryCodes.value = undefined
  await session.load()
})

function download() {
  const blob = new Blob(
    [`${t('account.totp.fileHeader')}\n\n${recoveryCodes.value?.join('\n')}\n`],
    {
      type: 'text/plain',
    },
  )
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = 'chorecast-recovery-codes.txt'
  a.click()
  URL.revokeObjectURL(a.href)
}

const errorOf = () =>
  begin.error.value ?? enable.error.value ?? regenerate.error.value ?? disable.error.value
</script>

<template>
  <NCard>
    <template #header>
      <div class="flex items-center gap-2">
        {{ t('account.totp.title') }}
        <NTag
          size="small"
          :type="session.me?.totpEnabled ? 'success' : 'default'"
          :bordered="false"
        >
          {{ session.me?.totpEnabled ? t('account.totp.on') : t('account.totp.off') }}
        </NTag>
      </div>
    </template>

    <div class="flex flex-col gap-3">
      <NAlert v-if="errorOf()" type="error" :show-icon="false">{{ errorOf()?.detail }}</NAlert>

      <!-- Recovery codes, shown once -->
      <div v-if="recoveryCodes" class="flex flex-col gap-3">
        <NAlert type="warning">{{ t('account.totp.saveCodes') }}</NAlert>
        <ul
          class="grid grid-cols-2 gap-1 rounded-md bg-gray-100 p-3 font-mono text-sm dark:bg-neutral-800"
          data-testid="recovery-codes"
        >
          <li v-for="c in recoveryCodes" :key="c">{{ c }}</li>
        </ul>
        <div class="flex justify-end gap-2">
          <NButton secondary @click="download">{{ t('account.totp.download') }}</NButton>
          <NButton type="primary" @click="recoveryCodes = undefined">{{
            t('account.totp.saved')
          }}</NButton>
        </div>
      </div>

      <!-- Off: start enrolment -->
      <template v-else-if="!session.me?.totpEnabled && !enrolment">
        <p class="text-sm text-gray-600 dark:text-neutral-400">{{ t('account.totp.hint') }}</p>
        <div class="flex justify-end">
          <NButton type="primary" :loading="begin.loading.value" @click="begin.run()">
            {{ t('account.totp.setUp') }}
          </NButton>
        </div>
      </template>

      <!-- Enrolling: scan, then confirm with a code -->
      <form
        v-else-if="enrolment"
        class="flex flex-col gap-3"
        @submit.prevent="code && enable.run()"
      >
        <p class="text-sm">{{ t('account.totp.scan') }}</p>
        <div class="flex justify-center rounded-md bg-white p-2">
          <NQrCode :value="enrolment.otpauthUrl" :size="180" error-correction-level="M" />
        </div>
        <p class="text-sm text-gray-600 dark:text-neutral-400">
          {{ t('account.totp.manual') }}
          <code class="break-all font-mono">{{ enrolment.secret }}</code>
        </p>
        <NFormItem
          :label="t('login.code')"
          :label-props="{ for: 'totp-code' }"
          :show-feedback="false"
        >
          <NInput
            v-model:value="code"
            :maxlength="6"
            :input-props="{ id: 'totp-code', inputmode: 'numeric', autocomplete: 'one-time-code' }"
          />
        </NFormItem>
        <div class="flex justify-end gap-2">
          <NButton quaternary @click="enrolment = undefined">{{ t('account.cancel') }}</NButton>
          <NButton
            type="primary"
            attr-type="submit"
            :disabled="code.length !== 6"
            :loading="enable.loading.value"
          >
            {{ t('account.totp.enable') }}
          </NButton>
        </div>
      </form>

      <!-- On: regenerate codes or turn off (password required) -->
      <template v-else>
        <p class="text-sm text-gray-600 dark:text-neutral-400">{{ t('account.totp.onHint') }}</p>
        <NFormItem
          :label="t('account.confirmPassword')"
          :label-props="{ for: 'totp-password' }"
          :show-feedback="false"
        >
          <NInput
            v-model:value="password"
            type="password"
            :input-props="{ id: 'totp-password', autocomplete: 'current-password' }"
          />
        </NFormItem>
        <div class="flex flex-wrap justify-end gap-2">
          <NButton
            secondary
            :disabled="!password"
            :loading="regenerate.loading.value"
            @click="regenerate.run()"
          >
            {{ t('account.totp.regenerate') }}
          </NButton>
          <NButton
            secondary
            type="error"
            :disabled="!password"
            :loading="disable.loading.value"
            @click="disable.run()"
          >
            {{ t('account.totp.disable') }}
          </NButton>
        </div>
      </template>
    </div>
  </NCard>
</template>
