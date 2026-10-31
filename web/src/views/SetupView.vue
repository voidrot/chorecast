<script setup lang="ts">
import Button from 'primevue/button'
import Card from 'primevue/card'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Password from 'primevue/password'
import ProgressSpinner from 'primevue/progressspinner'
import Select from 'primevue/select'
import Step from 'primevue/step'
import StepList from 'primevue/steplist'
import StepPanel from 'primevue/steppanel'
import StepPanels from 'primevue/steppanels'
import Stepper from 'primevue/stepper'
import ToggleSwitch from 'primevue/toggleswitch'
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

function next(from: number, activate: (value: number) => void) {
  shown.value[from] = true
  if (problems.value[from]?.length) return
  activate(from + 1)
}

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
  <main class="center">
    <Card class="card">
      <template #title>{{ t('setup.title') }}</template>
      <template #content>
        <div v-if="phase === 'checking'" class="checking">
          <ProgressSpinner style="width: 2rem; height: 2rem" />
        </div>

        <p v-else-if="phase === 'no_token'">{{ t('setup.body') }}</p>

        <Message v-else-if="phase === 'invalid'" severity="error">{{
          t('setup.invalidToken')
        }}</Message>

        <div v-else-if="phase === 'done'" class="stack">
          <Message severity="success">{{
            t('setup.done.created', { name: form.household.name })
          }}</Message>
          <h3>{{ t('setup.done.devicesTitle') }}</h3>
          <p>{{ t('setup.done.devicesBody') }}</p>
          <p>
            <a :href="releasesURL" target="_blank" rel="noopener">{{ t('setup.done.download') }}</a>
          </p>
          <p class="hint">{{ t('setup.done.pairingLater') }}</p>
          <Button
            :label="t('setup.done.open')"
            icon="pi pi-arrow-right"
            icon-pos="right"
            @click="openAdmin"
          />
        </div>

        <Stepper v-else v-model:value="step" linear>
          <StepList>
            <Step :value="1">{{ t('setup.household.title') }}</Step>
            <Step :value="2">{{ t('setup.owner.title') }}</Step>
            <Step :value="3">{{ t('setup.kids.title') }}</Step>
          </StepList>
          <StepPanels>
            <StepPanel v-slot="{ activateCallback }" :value="1">
              <form class="stack" @submit.prevent="next(1, activateCallback)">
                <div class="field">
                  <label for="household-name">{{ t('setup.household.name') }}</label>
                  <InputText id="household-name" v-model="form.household.name" maxlength="60" />
                </div>
                <div class="field">
                  <label for="household-timezone">{{ t('setup.household.timezone') }}</label>
                  <Select
                    input-id="household-timezone"
                    v-model="form.household.timezone"
                    :options="zones"
                    filter
                  />
                </div>
                <div class="row">
                  <div class="field">
                    <label for="household-dayEnd">{{ t('setup.household.dayEnd') }}</label>
                    <InputText id="household-dayEnd" v-model="form.household.dayEnd" type="time" />
                  </div>
                  <div class="field">
                    <label for="household-weekStart">{{ t('setup.household.weekStart') }}</label>
                    <Select
                      input-id="household-weekStart"
                      v-model="form.household.weekStart"
                      :options="weekStartOptions"
                      option-label="label"
                      option-value="value"
                    />
                  </div>
                </div>
                <p class="hint">{{ t('setup.household.dayEndHint') }}</p>
                <Message v-for="p in shown[1] ? problems[1] : []" :key="p" severity="warn">{{
                  t(p)
                }}</Message>
                <div class="actions">
                  <Button
                    type="submit"
                    :label="t('setup.next')"
                    icon="pi pi-arrow-right"
                    icon-pos="right"
                  />
                </div>
              </form>
            </StepPanel>

            <StepPanel v-slot="{ activateCallback }" :value="2">
              <form class="stack" @submit.prevent="next(2, activateCallback)">
                <p class="hint">{{ t('setup.owner.intro') }}</p>
                <div class="field">
                  <label for="owner-displayName">{{ t('setup.owner.displayName') }}</label>
                  <InputText
                    id="owner-displayName"
                    v-model="form.owner.displayName"
                    maxlength="40"
                  />
                </div>
                <div class="field">
                  <label for="owner-username">{{ t('setup.owner.username') }}</label>
                  <InputText
                    id="owner-username"
                    v-model="form.owner.username"
                    autocomplete="username"
                    autocapitalize="none"
                    maxlength="32"
                    @update:model-value="form.owner.username = String($event ?? '').toLowerCase()"
                  />
                </div>
                <div class="field">
                  <label for="owner-password">{{ t('setup.owner.password') }}</label>
                  <Password
                    input-id="owner-password"
                    v-model="form.owner.password"
                    :feedback="false"
                    toggle-mask
                    fluid
                    :input-props="{ autocomplete: 'new-password' }"
                  />
                </div>
                <div class="field">
                  <label for="owner-confirm">{{ t('setup.owner.confirm') }}</label>
                  <Password
                    input-id="owner-confirm"
                    v-model="form.owner.confirm"
                    :feedback="false"
                    toggle-mask
                    fluid
                    :input-props="{ autocomplete: 'new-password' }"
                  />
                </div>
                <p class="hint">{{ t('setup.owner.laterHint') }}</p>
                <Message v-for="p in shown[2] ? problems[2] : []" :key="p" severity="warn">{{
                  t(p)
                }}</Message>
                <div class="actions">
                  <Button
                    :label="t('setup.back')"
                    severity="secondary"
                    text
                    @click="activateCallback(1)"
                  />
                  <Button
                    type="submit"
                    :label="t('setup.next')"
                    icon="pi pi-arrow-right"
                    icon-pos="right"
                  />
                </div>
              </form>
            </StepPanel>

            <StepPanel v-slot="{ activateCallback }" :value="3">
              <form class="stack" @submit.prevent="submit">
                <p class="hint">{{ t('setup.kids.intro') }}</p>
                <div v-for="(kid, i) in form.kids" :key="i" class="kid">
                  <div class="row">
                    <div class="field">
                      <label :for="`kid-${i}-name`">{{ t('setup.kids.name') }}</label>
                      <InputText :id="`kid-${i}-name`" v-model="kid.displayName" maxlength="40" />
                    </div>
                    <div class="field">
                      <label :for="`kid-${i}-birthDate`">{{ t('setup.kids.birthDate') }}</label>
                      <InputText
                        :id="`kid-${i}-birthDate`"
                        v-model="kid.birthDate"
                        type="date"
                        :max="today"
                      />
                    </div>
                  </div>
                  <div class="row kid-options">
                    <fieldset class="swatches">
                      <legend class="sr-only">{{ t('setup.kids.color') }}</legend>
                      <label
                        v-for="c in kidColors"
                        :key="c"
                        class="swatch"
                        :for="`kid-${i}-color-${c.slice(1)}`"
                        :class="{ selected: kid.color === c }"
                        :style="{ background: c }"
                      >
                        <input
                          v-model="kid.color"
                          class="sr-only"
                          type="radio"
                          :id="`kid-${i}-color-${c.slice(1)}`"
                          :name="`kid-${i}-color`"
                          :value="c"
                        >
                        <span class="sr-only">{{ c }}</span>
                      </label>
                    </fieldset>
                    <div class="toggle">
                      <ToggleSwitch v-model="kid.pinRequired" :input-id="`kid-${i}-pin`" />
                      <label :for="`kid-${i}-pin`">{{ t('setup.kids.pinRequired') }}</label>
                    </div>
                    <Button
                      icon="pi pi-trash"
                      severity="danger"
                      text
                      :aria-label="t('setup.kids.remove')"
                      @click="form.kids.splice(i, 1)"
                    />
                  </div>
                </div>
                <div>
                  <Button
                    :label="t('setup.kids.add')"
                    icon="pi pi-plus"
                    severity="secondary"
                    outlined
                    @click="addKid"
                  />
                </div>
                <p class="hint">{{ t('setup.kids.laterHint') }}</p>
                <Message v-for="p in shown[3] ? problems[3] : []" :key="p" severity="warn">{{
                  t(p)
                }}</Message>
                <Message v-if="submitError" severity="error">
                  {{ submitError.detail || submitError.code }}
                  <ul v-if="submitError.fieldErrors?.length">
                    <li v-for="(fe, j) in submitError.fieldErrors" :key="j">
                      {{ fe.location }}: {{ fe.message }}
                    </li>
                  </ul>
                </Message>
                <div class="actions">
                  <Button
                    :label="t('setup.back')"
                    severity="secondary"
                    text
                    @click="activateCallback(2)"
                  />
                  <Button
                    type="submit"
                    :label="form.kids.length ? t('setup.finish') : t('setup.finishWithoutKids')"
                    icon="pi pi-check"
                    :loading="submitting"
                  />
                </div>
              </form>
            </StepPanel>
          </StepPanels>
        </Stepper>
      </template>
    </Card>
  </main>
</template>

<style scoped>
.center {
  display: grid;
  place-items: center;
  min-height: 100vh;
  padding: 1rem;
  font-family: system-ui, sans-serif;
}
.card {
  width: min(40rem, 100%);
}
.checking {
  display: grid;
  place-items: center;
  padding: 2rem;
}
.stack {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.stack > h3,
.stack > p {
  margin: 0;
}
.row {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
}
.field > label {
  font-weight: 500;
}
.row > .field {
  flex: 1 1 12rem;
}
.hint {
  margin: 0;
  color: var(--p-text-muted-color);
  font-size: 0.875rem;
}
.actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}
.kid {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  padding: 1rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: var(--p-content-border-radius);
}
.kid-options {
  align-items: center;
  justify-content: space-between;
}
.swatches {
  display: flex;
  gap: 0.375rem;
  margin: 0;
  padding: 0;
  border: 0;
}
.swatch {
  width: 1.75rem;
  height: 1.75rem;
  border-radius: 50%;
  border: 2px solid transparent;
  cursor: pointer;
}
.swatch.selected,
.swatch:focus-within {
  outline: 2px solid var(--p-text-color);
  outline-offset: 2px;
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
.toggle {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
</style>
