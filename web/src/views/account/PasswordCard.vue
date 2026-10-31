<script setup lang="ts">
import { NAlert, NButton, NCard, NFormItem, NInput, useMessage } from 'naive-ui'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import { useAsync } from '@/composables/useAsync'
import { minPasswordLength } from '@/setup/wizard'

const { t } = useI18n()
const message = useMessage()
const current = ref('')
const next = ref('')
const confirm = ref('')

const problem = computed(() => {
  if (next.value.length > 0 && next.value.length < minPasswordLength)
    return 'setup.owner.passwordTooShort'
  if (confirm.value && confirm.value !== next.value) return 'setup.owner.passwordMismatch'
  return ''
})
const ready = computed(
  () => current.value && next.value.length >= minPasswordLength && next.value === confirm.value,
)

const change = useAsync(async () => {
  const { error, response } = await api.PUT('/account/password', {
    body: { currentPassword: current.value, newPassword: next.value },
  })
  if (error) throw ApiError.fromProblem(response.status, error)
  current.value = next.value = confirm.value = ''
  message.success(t('account.password.changed'))
})
</script>

<template>
  <NCard :title="t('account.password.title')">
    <form class="flex flex-col gap-3" @submit.prevent="ready && change.run()">
      <p class="text-sm text-gray-600 dark:text-neutral-400">{{ t('account.password.hint') }}</p>
      <NFormItem
        :label="t('account.password.current')"
        :label-props="{ for: 'pw-current' }"
        :show-feedback="false"
      >
        <NInput
          v-model:value="current"
          type="password"
          show-password-on="click"
          :input-props="{ id: 'pw-current', autocomplete: 'current-password' }"
        />
      </NFormItem>
      <NFormItem
        :label="t('account.password.new')"
        :label-props="{ for: 'pw-new' }"
        :show-feedback="false"
      >
        <NInput
          v-model:value="next"
          type="password"
          show-password-on="click"
          :input-props="{ id: 'pw-new', autocomplete: 'new-password' }"
        />
      </NFormItem>
      <NFormItem
        :label="t('setup.owner.confirm')"
        :label-props="{ for: 'pw-confirm' }"
        :show-feedback="false"
      >
        <NInput
          v-model:value="confirm"
          type="password"
          show-password-on="click"
          :input-props="{ id: 'pw-confirm', autocomplete: 'new-password' }"
        />
      </NFormItem>
      <NAlert v-if="problem" type="warning" :show-icon="false">{{ t(problem) }}</NAlert>
      <NAlert v-if="change.error.value" type="error" :show-icon="false">
        {{ change.error.value.detail }}
      </NAlert>
      <div class="flex justify-end">
        <NButton
          type="primary"
          attr-type="submit"
          :disabled="!ready"
          :loading="change.loading.value"
        >
          {{ t('account.password.submit') }}
        </NButton>
      </div>
    </form>
  </NCard>
</template>
