<script setup lang="ts">
import { NAlert, NButton, NCard, NFormItem, NInput, NTag, useMessage } from 'naive-ui'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import { ssoErrorKey } from '@/auth/login'
import { useAsync } from '@/composables/useAsync'
import { useSessionStore } from '@/stores/session'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const message = useMessage()
const session = useSessionStore()
const password = ref('')
const ssoError = ref(ssoErrorKey(route.query.sso_error))
const linked = computed(() => session.me?.oidcLinked === true)

onMounted(async () => {
  // Coming back from the identity provider: report, then tidy the URL.
  if (route.query.sso === 'linked') message.success(t('sso.linked'))
  if (route.query.sso || route.query.sso_error) {
    await session.load()
    await router.replace({ query: {} })
  }
})

function link() {
  window.location.assign('/api/v1/account/oidc/link')
}

const unlink = useAsync(async () => {
  const { error, response } = await api.POST('/account/oidc/unlink', {
    body: { password: password.value },
  })
  if (error) throw ApiError.fromProblem(response.status, error)
  password.value = ''
  await session.load()
  message.success(t('sso.unlinked'))
})
</script>

<template>
  <NCard>
    <template #header>
      <div class="flex items-center gap-2">
        {{ t('sso.title') }}
        <NTag size="small" :type="linked ? 'success' : 'default'" :bordered="false">
          {{ linked ? t('sso.on') : t('sso.off') }}
        </NTag>
      </div>
    </template>
    <div class="flex flex-col gap-3">
      <NAlert v-if="ssoError" type="error" :show-icon="false">{{ t(ssoError) }}</NAlert>
      <template v-if="!linked">
        <p class="text-sm text-gray-600 dark:text-neutral-400">{{ t('sso.hint') }}</p>
        <div class="flex justify-end">
          <NButton type="primary" @click="link">{{ t('sso.link') }}</NButton>
        </div>
      </template>
      <form v-else class="flex flex-col gap-3" @submit.prevent="password && unlink.run()">
        <p class="text-sm text-gray-600 dark:text-neutral-400">{{ t('sso.linkedHint') }}</p>
        <NFormItem
          :label="t('account.confirmPassword')"
          :label-props="{ for: 'sso-password' }"
          :show-feedback="false"
        >
          <NInput
            v-model:value="password"
            type="password"
            :input-props="{ id: 'sso-password', autocomplete: 'current-password' }"
          />
        </NFormItem>
        <NAlert v-if="unlink.error.value" type="error" :show-icon="false">
          {{ unlink.error.value.detail }}
        </NAlert>
        <div class="flex justify-end">
          <NButton
            secondary
            type="error"
            attr-type="submit"
            :disabled="!password"
            :loading="unlink.loading.value"
          >
            {{ t('sso.unlink') }}
          </NButton>
        </div>
      </form>
    </div>
  </NCard>
</template>
