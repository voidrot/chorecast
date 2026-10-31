<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCard,
  NDatePicker,
  NFormItem,
  NInput,
  NSelect,
  NSpin,
  NStep,
  NSteps,
  NSwitch,
  NTimePicker,
} from 'naive-ui'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import {
  buildRequest,
  householdProblems,
  kidColors,
  kidProblems,
  newForm,
  newKid,
  ownerProblems,
  timeZones,
  todayISO,
  weekStarts,
} from '@/setup/wizard'
import { useSessionStore } from '@/stores/session'

const releasesURL = 'https://github.com/voidrot/chorecast/releases'
const tokenKey = 'chorecast.setupToken'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()

/** checking → (no_token | invalid | ready) → done */
type Phase = 'checking' | 'no_token' | 'invalid' | 'ready' | 'done'
const phase = ref<Phase>('checking')
const token = ref('')
const step = ref(1)
const form = ref(newForm())
const zones = timeZones()
const today = todayISO()
const submitting = ref(false)
const submitError = ref<ApiError>()
const shown = ref<Record<number, boolean>>({}) // steps whose problems are shown (after a "next" click)

const zoneOptions = zones.map((z) => ({ value: z, label: z }))
const weekStartOptions = weekStarts.map((value) => ({
  value,
  label: t(`setup.household.weekdays.${value}`),
}))

const problems = computed<Record<number, string[]>>(() => ({
  1: householdProblems(form.value.household),
  2: ownerProblems(form.value.owner),
  3: kidProblems(form.value.kids, today),
}))

function storage(): Storage | undefined {
  try {
    return globalThis.sessionStorage
  } catch {
    return undefined
  }
}

onMounted(async () => {
  // Keep the token out of the address bar (and history) once read; survive a page refresh.
  const fromQuery = typeof route.query.token === 'string' ? route.query.token : ''
  if (fromQuery) {
    storage()?.setItem(tokenKey, fromQuery)
    await router.replace({ name: 'setup' })
  }
  token.value = fromQuery || storage()?.getItem(tokenKey) || ''
  if (!token.value) {
    phase.value = 'no_token'
    return
  }
  try {
    const { error, response } = await api.POST('/setup/verify', { body: { token: token.value } })
    if (!error) phase.value = 'ready'
    else if (ApiError.fromProblem(response.status, error).code === 'setup_complete') await toLogin()
    else phase.value = 'invalid'
  } catch (e) {
    phase.value = 'ready' // unreachable now; submitting will report it
    submitError.value = ApiError.network(e)
  }
})

function next(from: number) {
  shown.value[from] = true
  if (problems.value[from]?.length) return
  step.value = from + 1
}

const isFuture = (ts: number) => ts > Date.now()

function addKid() {
  form.value.kids.push(newKid(form.value.kids))
}

async function submit() {
  shown.value[3] = true
  if (problems.value[3]?.length) return
  submitting.value = true
  submitError.value = undefined
  try {
    const { error, response } = await api.POST('/setup', {
      body: buildRequest(token.value, form.value),
    })
    if (!error) {
      storage()?.removeItem(tokenKey)
      phase.value = 'done'
      return
    }
    const err = ApiError.fromProblem(response.status, error)
    if (err.code === 'setup_complete') await toLogin()
    else if (err.code === 'invalid_setup_token') phase.value = 'invalid'
    else submitError.value = err
  } catch (e) {
    submitError.value = ApiError.network(e)
  } finally {
    submitting.value = false
  }
}

async function toLogin() {
  storage()?.removeItem(tokenKey)
  await session.load()
  await router.replace({ name: 'login' })
}

async function openAdmin() {
  await session.load()
  await router.replace({ name: 'home' })
}
</script>

<template>
  <main class="grid min-h-screen place-items-center p-4">
    <NCard :title="t('setup.title')" class="w-full max-w-2xl">
      <div v-if="phase === 'checking'" class="grid place-items-center p-8">
        <NSpin />
      </div>

      <p v-else-if="phase === 'no_token'">{{ t('setup.body') }}</p>

      <NAlert v-else-if="phase === 'invalid'" type="error">{{ t('setup.invalidToken') }}</NAlert>

      <div v-else-if="phase === 'done'" class="flex flex-col gap-4">
        <NAlert type="success">{{ t('setup.done.created', { name: form.household.name }) }}</NAlert>
        <h3 class="text-lg font-semibold">{{ t('setup.done.devicesTitle') }}</h3>
        <p>{{ t('setup.done.devicesBody') }}</p>
        <a
          :href="releasesURL"
          target="_blank"
          rel="noopener"
          class="text-emerald-600 underline dark:text-emerald-400"
          >{{
            t('setup.done.download')
          }}</a
        >
        <p class="text-sm text-gray-500 dark:text-neutral-400">
          {{ t('setup.done.pairingLater') }}
        </p>
        <NButton type="primary" block @click="openAdmin">{{ t('setup.done.open') }}</NButton>
      </div>

      <div v-else class="flex flex-col gap-6">
        <NSteps :current="step" size="small">
          <NStep :title="t('setup.household.title')" />
          <NStep :title="t('setup.owner.title')" />
          <NStep :title="t('setup.kids.title')" />
        </NSteps>

        <form v-if="step === 1" class="flex flex-col" @submit.prevent="next(1)">
          <NFormItem
            :label="t('setup.household.name')"
            :label-props="{ for: 'household-name' }"
            :show-feedback="false"
            class="mb-4"
          >
            <NInput
              v-model:value="form.household.name"
              :maxlength="60"
              :input-props="{ id: 'household-name' }"
            />
          </NFormItem>
          <NFormItem :label="t('setup.household.timezone')" :show-feedback="false" class="mb-4">
            <NSelect v-model:value="form.household.timezone" :options="zoneOptions" filterable />
          </NFormItem>
          <div class="grid gap-4 sm:grid-cols-2">
            <NFormItem :label="t('setup.household.dayEnd')" :show-feedback="false">
              <NTimePicker
                :formatted-value="form.household.dayEnd"
                value-format="HH:mm"
                format="HH:mm"
                :clearable="false"
                class="w-full"
                @update:formatted-value="form.household.dayEnd = $event ?? ''"
              />
            </NFormItem>
            <NFormItem :label="t('setup.household.weekStart')" :show-feedback="false">
              <NSelect v-model:value="form.household.weekStart" :options="weekStartOptions" />
            </NFormItem>
          </div>
          <p class="hint mt-2">{{ t('setup.household.dayEndHint') }}</p>
          <div v-if="shown[1] && problems[1]?.length" class="mt-4 flex flex-col gap-2">
            <NAlert v-for="p in problems[1]" :key="p" type="warning" :show-icon="false">{{
              t(p)
            }}</NAlert>
          </div>
          <div class="mt-6 flex justify-end gap-2">
            <NButton type="primary" attr-type="submit">{{ t('setup.next') }}</NButton>
          </div>
        </form>

        <form v-else-if="step === 2" class="flex flex-col" @submit.prevent="next(2)">
          <p class="hint mb-4">{{ t('setup.owner.intro') }}</p>
          <NFormItem
            :label="t('setup.owner.displayName')"
            :label-props="{ for: 'owner-name' }"
            :show-feedback="false"
            class="mb-4"
          >
            <NInput
              v-model:value="form.owner.displayName"
              :maxlength="40"
              :input-props="{ id: 'owner-name' }"
            />
          </NFormItem>
          <NFormItem
            :label="t('setup.owner.username')"
            :label-props="{ for: 'owner-username' }"
            :show-feedback="false"
            class="mb-4"
          >
            <NInput
              :value="form.owner.username"
              :maxlength="32"
              :input-props="{
                autocomplete: 'username',
                autocapitalize: 'none',
                id: 'owner-username',
              }"
              @update:value="form.owner.username = $event.toLowerCase()"
            />
          </NFormItem>
          <NFormItem
            :label="t('setup.owner.password')"
            :label-props="{ for: 'owner-password' }"
            :show-feedback="false"
            class="mb-4"
          >
            <NInput
              v-model:value="form.owner.password"
              type="password"
              show-password-on="click"
              :input-props="{ autocomplete: 'new-password', id: 'owner-password' }"
            />
          </NFormItem>
          <NFormItem
            :label="t('setup.owner.confirm')"
            :label-props="{ for: 'owner-confirm' }"
            :show-feedback="false"
          >
            <NInput
              v-model:value="form.owner.confirm"
              type="password"
              show-password-on="click"
              :input-props="{ autocomplete: 'new-password', id: 'owner-confirm' }"
            />
          </NFormItem>
          <p class="hint mt-2">{{ t('setup.owner.laterHint') }}</p>
          <div v-if="shown[2] && problems[2]?.length" class="mt-4 flex flex-col gap-2">
            <NAlert v-for="p in problems[2]" :key="p" type="warning" :show-icon="false">{{
              t(p)
            }}</NAlert>
          </div>
          <div class="mt-6 flex justify-end gap-2">
            <NButton quaternary @click="step = 1">{{ t('setup.back') }}</NButton>
            <NButton type="primary" attr-type="submit">{{ t('setup.next') }}</NButton>
          </div>
        </form>

        <form v-else class="flex flex-col gap-4" @submit.prevent="submit">
          <p class="hint">{{ t('setup.kids.intro') }}</p>
          <div
            v-for="(kid, i) in form.kids"
            :key="i"
            class="flex flex-col gap-3 rounded-lg border border-gray-200 p-4 dark:border-neutral-700"
          >
            <div class="grid gap-4 sm:grid-cols-2">
              <NFormItem
                :label="t('setup.kids.name')"
                :label-props="{ for: `kid-${i}-name` }"
                :show-feedback="false"
              >
                <NInput
                  v-model:value="kid.displayName"
                  :maxlength="40"
                  :input-props="{ id: `kid-${i}-name` }"
                />
              </NFormItem>
              <NFormItem :label="t('setup.kids.birthDate')" :show-feedback="false">
                <NDatePicker
                  :formatted-value="kid.birthDate || null"
                  type="date"
                  value-format="yyyy-MM-dd"
                  clearable
                  class="w-full"
                  :is-date-disabled="isFuture"
                  :input-readonly="false"
                  @update:formatted-value="kid.birthDate = $event ?? ''"
                />
              </NFormItem>
            </div>
            <div class="flex flex-wrap items-center justify-between gap-3">
              <fieldset class="flex gap-1.5">
                <legend class="sr-only">{{ t('setup.kids.color') }}</legend>
                <label
                  v-for="c in kidColors"
                  :key="c"
                  :for="`kid-${i}-color-${c.slice(1)}`"
                  class="size-7 cursor-pointer rounded-full outline-offset-2 focus-within:outline-2"
                  :class="kid.color === c ? 'outline-2 outline-gray-900 dark:outline-white' : ''"
                  :style="{ background: c }"
                >
                  <input
                    :id="`kid-${i}-color-${c.slice(1)}`"
                    v-model="kid.color"
                    class="sr-only"
                    type="radio"
                    :name="`kid-${i}-color`"
                    :value="c"
                  >
                  <span class="sr-only">{{ c }}</span>
                </label>
              </fieldset>
              <div class="flex items-center gap-2">
                <NSwitch v-model:value="kid.pinRequired" :aria-labelledby="`kid-${i}-pin-label`" />
                <span :id="`kid-${i}-pin-label`">{{ t('setup.kids.pinRequired') }}</span>
              </div>
              <NButton
                quaternary
                type="error"
                size="small"
                :aria-label="t('setup.kids.remove')"
                @click="form.kids.splice(i, 1)"
                >{{
                  t('setup.kids.remove')
                }}</NButton
              >
            </div>
          </div>
          <div>
            <NButton dashed @click="addKid">{{ t('setup.kids.add') }}</NButton>
          </div>
          <p class="hint">{{ t('setup.kids.laterHint') }}</p>
          <div v-if="shown[3] && problems[3]?.length" class="flex flex-col gap-2">
            <NAlert v-for="p in problems[3]" :key="p" type="warning" :show-icon="false">{{
              t(p)
            }}</NAlert>
          </div>
          <NAlert v-if="submitError" type="error">
            {{ submitError.detail || submitError.code }}
            <ul v-if="submitError.fieldErrors?.length" class="mt-1 list-disc pl-5">
              <li v-for="(fe, j) in submitError.fieldErrors" :key="j">
                {{ fe.location }}: {{ fe.message }}
              </li>
            </ul>
          </NAlert>
          <div class="flex justify-end gap-2">
            <NButton quaternary @click="step = 2">{{ t('setup.back') }}</NButton>
            <NButton type="primary" attr-type="submit" :loading="submitting">
              {{ form.kids.length ? t('setup.finish') : t('setup.finishWithoutKids') }}
            </NButton>
          </div>
        </form>
      </div>
    </NCard>
  </main>
</template>

<style scoped>
@reference "../style.css";
.hint {
  @apply text-sm text-gray-500 dark:text-neutral-400;
}
</style>
