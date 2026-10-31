<script setup lang="ts">
import { NAlert, NButton, NCard, NFormItem, NInput, useMessage } from 'naive-ui'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import { type LoginFailure, type LoginStep, loginFailure, safeRedirect } from '@/auth/login'
import { useSessionStore } from '@/stores/session'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const message = useMessage()

const step = ref<LoginStep>('password')
const username = ref('')
const password = ref('')
const code = ref('')
const submitting = ref(false)
const failure = ref<LoginFailure>()

async function submit() {
  if (!username.value || !password.value) return
  submitting.value = true
  failure.value = undefined
  try {
    const { data, error, response } = await api.POST('/auth/login', {
      body: {
        username: username.value,
        password: password.value,
        client: 'web',
        ...(step.value === 'totp' && code.value ? { totpCode: code.value } : {}),
        ...(step.value === 'recovery' && code.value ? { recoveryCode: code.value } : {}),
      },
    })
    if (data) {
      const left = data.recoveryCodesLeft
      if (left !== undefined && left !== null && left <= 3) {
        message.warning(t('login.recoveryCodesLeft', { count: left }, left), { duration: 10000 })
      }
      await session.load()
      await router.replace(safeRedirect(route.query.redirect))
      return
    }
    const f = loginFailure(
      ApiError.fromProblem(response.status, error),
      response.headers.get('Retry-After'),
      step.value,
    )
    if (f.step && f.step !== step.value) {
      step.value = f.step
      code.value = ''
    }
    if (f.step === 'password') password.value = ''
    if (f.messageKey) failure.value = f
  } catch (e) {
    failure.value = loginFailure(ApiError.network(e), null, step.value)
  } finally {
    submitting.value = false
  }
}

function useRecovery(on: boolean) {
  step.value = on ? 'recovery' : 'totp'
  code.value = ''
  failure.value = undefined
}

function startOver() {
  step.value = 'password'
  password.value = ''
  code.value = ''
  failure.value = undefined
}
</script>

<template>
  <main class="grid min-h-screen place-items-center p-4">
    <NCard :title="t('login.title')" class="w-full max-w-sm">
      <NAlert v-if="session.status === 'unknown' && session.error" type="error" class="mb-4">
        {{ t('login.unreachable', { detail: session.error.detail }) }}
      </NAlert>
      <NAlert v-else-if="session.status === 'forbidden'" type="warning" class="mb-4">
        {{ t('login.notParent') }}
      </NAlert>

      <form class="flex flex-col gap-4" @submit.prevent="submit">
        <template v-if="step === 'password'">
          <NFormItem
            :label="t('login.username')"
            :label-props="{ for: 'login-username' }"
            :show-feedback="false"
          >
            <NInput
              v-model:value="username"
              :input-props="{
                id: 'login-username',
                autocomplete: 'username',
                autocapitalize: 'none',
              }"
            />
          </NFormItem>
          <NFormItem
            :label="t('login.password')"
            :label-props="{ for: 'login-password' }"
            :show-feedback="false"
          >
            <NInput
              v-model:value="password"
              type="password"
              show-password-on="click"
              :input-props="{ id: 'login-password', autocomplete: 'current-password' }"
            />
          </NFormItem>
        </template>

        <template v-else>
          <p class="text-sm text-gray-600 dark:text-neutral-300">
            {{ step === 'totp' ? t('login.totpPrompt') : t('login.recoveryPrompt') }}
          </p>
          <NFormItem
            :label="step === 'totp' ? t('login.code') : t('login.recoveryCode')"
            :label-props="{ for: 'login-code' }"
            :show-feedback="false"
          >
            <NInput
              v-model:value="code"
              :input-props="{
                id: 'login-code',
                autocomplete: 'one-time-code',
                inputmode: step === 'totp' ? 'numeric' : 'text',
              }"
              :maxlength="step === 'totp' ? 6 : 20"
            />
          </NFormItem>
          <div class="flex justify-between text-sm">
            <NButton text type="primary" @click="useRecovery(step === 'totp')">
              {{ step === 'totp' ? t('login.useRecovery') : t('login.useCode') }}
            </NButton>
            <NButton text @click="startOver">{{ t('login.startOver') }}</NButton>
          </div>
        </template>

        <NAlert v-if="failure" type="error" :show-icon="false">
          {{ t(failure.messageKey, failure.params ?? {}) }}
        </NAlert>

        <NButton type="primary" attr-type="submit" block :loading="submitting">
          {{ step === 'password' ? t('login.submit') : t('login.verify') }}
        </NButton>
      </form>
    </NCard>
  </main>
</template>
