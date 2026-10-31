<script setup lang="ts">
import { NAlert, NButton, NCard, NFormItem, NInput, NTag, useMessage } from 'naive-ui'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import { useAsync } from '@/composables/useAsync'
import { useSessionStore } from '@/stores/session'

const { t } = useI18n()
const message = useMessage()
const session = useSessionStore()
const password = ref('')
const pin = ref('')

const pinValid = computed(() => /^\d{4,8}$/.test(pin.value))

const save = useAsync(async (clear: boolean) => {
  const { error, response } = await api.PUT('/account/pin', {
    body: { password: password.value, pin: clear ? '' : pin.value },
  })
  if (error) throw ApiError.fromProblem(response.status, error)
  password.value = pin.value = ''
  await session.load()
  message.success(clear ? t('account.pin.cleared') : t('account.pin.saved'))
})
</script>

<template>
  <NCard>
    <template #header>
      <div class="flex items-center gap-2">
        {{ t('account.pin.title') }}
        <NTag size="small" :type="session.me?.pinSet ? 'success' : 'default'" :bordered="false">
          {{ session.me?.pinSet ? t('account.pin.set') : t('account.pin.notSet') }}
        </NTag>
      </div>
    </template>
    <form class="flex flex-col gap-3" @submit.prevent="pinValid && password && save.run(false)">
      <p class="text-sm text-gray-600 dark:text-neutral-400">{{ t('account.pin.hint') }}</p>
      <NFormItem
        :label="t('account.pin.pin')"
        :label-props="{ for: 'pin-new' }"
        :show-feedback="false"
      >
        <NInput
          v-model:value="pin"
          type="password"
          :maxlength="8"
          :input-props="{ id: 'pin-new', inputmode: 'numeric', autocomplete: 'off' }"
        />
      </NFormItem>
      <NFormItem
        :label="t('account.confirmPassword')"
        :label-props="{ for: 'pin-password' }"
        :show-feedback="false"
      >
        <NInput
          v-model:value="password"
          type="password"
          :input-props="{ id: 'pin-password', autocomplete: 'current-password' }"
        />
      </NFormItem>
      <NAlert v-if="pin && !pinValid" type="warning" :show-icon="false">{{
        t('account.pin.invalid')
      }}</NAlert>
      <NAlert v-if="save.error.value" type="error" :show-icon="false">{{
        save.error.value.detail
      }}</NAlert>
      <div class="flex justify-end gap-2">
        <NButton
          v-if="session.me?.pinSet"
          secondary
          type="error"
          :disabled="!password"
          :loading="save.loading.value"
          @click="save.run(true)"
        >
          {{ t('account.pin.clear') }}
        </NButton>
        <NButton
          type="primary"
          attr-type="submit"
          :disabled="!pinValid || !password"
          :loading="save.loading.value"
        >
          {{ session.me?.pinSet ? t('account.pin.change') : t('account.pin.submit') }}
        </NButton>
      </div>
    </form>
  </NCard>
</template>
