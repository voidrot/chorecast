<script setup lang="ts">
import { NAlert, NButton, NCard, NDivider, NFormItem, NInput, NSpin } from 'naive-ui'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import type { components } from '@/api/schema'
import { minPasswordLength, usernamePattern } from '@/setup/wizard'
import { useSessionStore } from '@/stores/session'

type InviteInfo = components['schemas']['InviteInfoOutputBody']

const tokenKey = 'chorecast.inviteToken'
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()

const phase = ref<'checking' | 'no_token' | 'invalid' | 'ready'>('checking')
const token = ref('')
const info = ref<InviteInfo>()
const displayName = ref('')
const username = ref('')
const password = ref('')
const confirm = ref('')
const shown = ref(false)
const submitting = ref(false)
const submitError = ref<ApiError>()
const ssoError = computed(() => {
  const code = route.query.sso_error
  if (typeof code !== 'string' || !code) return undefined
  return code === 'username_taken' ? 'invite.errors.usernameTaken' : 'invite.errors.sso'
})

function storage(): Storage | undefined {
  try {
    return globalThis.sessionStorage
  } catch {
    return undefined
  }
}

const problems = computed(() => {
  const out: string[] = []
  if (!displayName.value.trim()) out.push('setup.owner.displayNameRequired')
  if (!usernamePattern.test(username.value)) out.push('setup.owner.usernameInvalid')
  if (password.value.length < minPasswordLength) out.push('setup.owner.passwordTooShort')
  else if (password.value !== confirm.value) out.push('setup.owner.passwordMismatch')
  return out
})

onMounted(async () => {
  // Like the setup link: move the token out of the address bar, and survive a refresh.
  const fromQuery = typeof route.query.token === 'string' ? route.query.token : ''
  if (fromQuery) {
    storage()?.setItem(tokenKey, fromQuery)
    await router.replace({
      name: 'invite',
      query: route.query.sso_error ? { sso_error: route.query.sso_error } : {},
    })
  }
  token.value = fromQuery || storage()?.getItem(tokenKey) || ''
  if (!token.value) {
    phase.value = 'no_token'
    return
  }
  try {
    const { data } = await api.POST('/auth/invite', { body: { token: token.value } })
    if (data) {
      info.value = data
      phase.value = 'ready'
    } else {
      phase.value = 'invalid'
    }
  } catch (e) {
    phase.value = 'ready'
    submitError.value = ApiError.network(e)
  }
})

async function done() {
  storage()?.removeItem(tokenKey)
  await session.load()
  await router.replace({ name: 'home' })
}

async function submit() {
  shown.value = true
  if (problems.value.length) return
  submitting.value = true
  submitError.value = undefined
  try {
    const { data, error, response } = await api.POST('/auth/invite/accept', {
      body: {
        token: token.value,
        displayName: displayName.value.trim(),
        username: username.value,
        password: password.value,
      },
    })
    if (data) return await done()
    const err = ApiError.fromProblem(response.status, error)
    if (err.code === 'invalid_invite') phase.value = 'invalid'
    else submitError.value = err
  } catch (e) {
    submitError.value = ApiError.network(e)
  } finally {
    submitting.value = false
  }
}

function joinWithSSO() {
  shown.value = true
  if (!displayName.value.trim()) return
  const q = new URLSearchParams({
    client: 'invite',
    invite: token.value,
    displayName: displayName.value.trim(),
  })
  if (usernamePattern.test(username.value)) q.set('username', username.value)
  window.location.assign(`/api/v1/auth/oidc/start?${q}`)
}
</script>

<template>
  <main class="grid min-h-screen place-items-center p-4">
    <NCard :title="t('invite.title')" class="w-full max-w-md">
      <div v-if="phase === 'checking'" class="grid place-items-center p-8"><NSpin /></div>
      <p v-else-if="phase === 'no_token'">{{ t('invite.noToken') }}</p>
      <NAlert v-else-if="phase === 'invalid'" type="error">{{ t('invite.invalid') }}</NAlert>

      <form v-else class="flex flex-col gap-4" @submit.prevent="submit">
        <p v-if="info">
          {{
            t(info.role === 'owner' ? 'invite.introOwner' : 'invite.intro', {
              household: info.householdName,
              name: info.invitedBy || t('invite.someone'),
            })
          }}
        </p>
        <NAlert v-if="ssoError" type="error" :show-icon="false">{{ t(ssoError) }}</NAlert>
        <NFormItem
          :label="t('setup.owner.displayName')"
          :label-props="{ for: 'inv-name' }"
          :show-feedback="false"
        >
          <NInput v-model:value="displayName" :maxlength="40" :input-props="{ id: 'inv-name' }" />
        </NFormItem>
        <NFormItem
          :label="t('setup.owner.username')"
          :label-props="{ for: 'inv-username' }"
          :show-feedback="false"
        >
          <NInput
            :value="username"
            :maxlength="32"
            :input-props="{ id: 'inv-username', autocomplete: 'username', autocapitalize: 'none' }"
            @update:value="username = $event.toLowerCase()"
          />
        </NFormItem>
        <NFormItem
          :label="t('setup.owner.password')"
          :label-props="{ for: 'inv-password' }"
          :show-feedback="false"
        >
          <NInput
            v-model:value="password"
            type="password"
            show-password-on="click"
            :input-props="{ id: 'inv-password', autocomplete: 'new-password' }"
          />
        </NFormItem>
        <NFormItem
          :label="t('setup.owner.confirm')"
          :label-props="{ for: 'inv-confirm' }"
          :show-feedback="false"
        >
          <NInput
            v-model:value="confirm"
            type="password"
            show-password-on="click"
            :input-props="{ id: 'inv-confirm', autocomplete: 'new-password' }"
          />
        </NFormItem>
        <div v-if="shown && problems.length" class="flex flex-col gap-2">
          <NAlert v-for="p in problems" :key="p" type="warning" :show-icon="false">{{
            t(p)
          }}</NAlert>
        </div>
        <NAlert v-if="submitError" type="error" :show-icon="false">
          {{
            submitError.code === 'username_taken'
              ? t('invite.errors.usernameTaken')
              : submitError.detail
          }}
        </NAlert>
        <NButton type="primary" attr-type="submit" block :loading="submitting">{{
          t('invite.submit')
        }}</NButton>

        <template v-if="info?.oidc">
          <NDivider>{{ t('sso.or') }}</NDivider>
          <p class="text-sm text-gray-600 dark:text-neutral-400">{{ t('invite.ssoHint') }}</p>
          <NButton block secondary @click="joinWithSSO">{{ t('invite.sso') }}</NButton>
        </template>
      </form>
    </NCard>
  </main>
</template>
