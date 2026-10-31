<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCard,
  NCheckbox,
  NFormItem,
  NInput,
  NInputNumber,
  NRadioButton,
  NRadioGroup,
  NSelect,
  NSpin,
  NSwitch,
  NTimePicker,
  useDialog,
  useMessage,
} from 'naive-ui'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import type { components } from '@/api/schema'
import {
  type ChoreForm,
  choreProblems,
  emptyChoreForm,
  formFromChore,
  inputFromForm,
} from '@/chores/form'

type Kid = components['schemas']['Kid']
type Item = components['schemas']['CatalogItem']

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const message = useMessage()
const dialog = useDialog()

const isNew = computed(() => route.name === 'chore-new')
const id = computed(() => Number(route.params.id))
const form = ref<ChoreForm>(emptyChoreForm())
const loading = ref(true)
const saving = ref(false)
const error = ref<ApiError>()
const shown = ref(false)
const kids = ref<Kid[]>([])
const locations = ref<Item[]>([])
const equipment = ref<Item[]>([])

const problems = computed(() => choreProblems(form.value))
const locationOptions = computed(() => locations.value.map((l) => ({ value: l.id, label: l.name })))
const equipmentOptions = computed(() =>
  equipment.value.map((e) => ({ value: e.id, label: e.name })),
)
const kidName = (kidId: number) =>
  kids.value.find((k) => k.id === kidId)?.displayName ?? `#${kidId}`
const kidOptions = computed(() =>
  kids.value
    .filter((k) => !form.value.eligibility.some((e) => e.kidId === k.id))
    .map((k) => ({ value: k.id, label: k.displayName })),
)

onMounted(async () => {
  const [k, l, e] = await Promise.all([
    api.GET('/kids'),
    api.GET('/locations'),
    api.GET('/equipment'),
  ])
  kids.value = k.data?.items ?? []
  locations.value = l.data?.items ?? []
  equipment.value = e.data?.items ?? []
  if (!isNew.value) {
    const {
      data,
      error: err,
      response,
    } = await api.GET('/chores/{id}', { params: { path: { id: id.value } } })
    if (data) form.value = formFromChore(data)
    else error.value = ApiError.fromProblem(response.status, err)
  }
  loading.value = false
})

function addEligibility(kidId: number) {
  form.value.eligibility.push({ kidId, allow: true })
}

async function save() {
  shown.value = true
  if (problems.value.length) return
  saving.value = true
  error.value = undefined
  try {
    const body = inputFromForm(form.value)
    const res = isNew.value
      ? await api.POST('/chores', { body })
      : await api.PUT('/chores/{id}', { params: { path: { id: id.value } }, body })
    if (!res.data) {
      error.value = ApiError.fromProblem(res.response.status, res.error)
      return
    }
    message.success(t('chores.saved', { name: res.data.name }))
    await router.push({ name: 'chores' })
  } catch (e) {
    error.value = ApiError.network(e)
  } finally {
    saving.value = false
  }
}

function remove() {
  dialog.error({
    title: t('chores.delete'),
    content: t('chores.deleteConfirm', { name: form.value.name }),
    positiveText: t('chores.delete'),
    negativeText: t('account.cancel'),
    onPositiveClick: async () => {
      const { error: err, response } = await api.DELETE('/chores/{id}', {
        params: { path: { id: id.value } },
      })
      if (err) {
        error.value = ApiError.fromProblem(response.status, err)
        return
      }
      await router.push({ name: 'chores' })
    },
  })
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <h1 class="text-2xl font-semibold">{{ isNew ? t('chores.newTitle') : form.name }}</h1>
    <NSpin v-if="loading" />
    <form v-else class="flex flex-col gap-6" @submit.prevent="save">
      <NAlert v-if="error" type="error" :show-icon="false">
        {{ error.detail || error.code }}
        <ul v-if="error.fieldErrors?.length" class="mt-1 list-disc pl-5">
          <li v-for="(fe, i) in error.fieldErrors" :key="i">{{ fe.location }}: {{ fe.message }}</li>
        </ul>
      </NAlert>
      <div v-if="shown && problems.length" class="flex flex-col gap-2">
        <NAlert v-for="p in problems" :key="p" type="warning" :show-icon="false">{{ t(p) }}</NAlert>
      </div>

      <NCard :title="t('chores.basics')">
        <div class="grid gap-4 sm:grid-cols-2">
          <NFormItem
            :label="t('chores.name')"
            :label-props="{ for: 'ch-name' }"
            :show-feedback="false"
            class="sm:col-span-2"
          >
            <NInput v-model:value="form.name" :maxlength="80" :input-props="{ id: 'ch-name' }" />
          </NFormItem>
          <NFormItem
            :label="t('chores.description')"
            :label-props="{ for: 'ch-desc' }"
            :show-feedback="false"
            class="sm:col-span-2"
          >
            <NInput
              v-model:value="form.description"
              type="textarea"
              :autosize="{ minRows: 2 }"
              :maxlength="2000"
              :input-props="{ id: 'ch-desc' }"
            />
          </NFormItem>
          <NFormItem :label="t('chores.points')" :show-feedback="false">
            <NInputNumber v-model:value="form.points" :min="0" :max="100000" class="w-full" />
          </NFormItem>
          <NFormItem :label="t('chores.penalty')" :show-feedback="false">
            <NInputNumber v-model:value="form.penalty" :min="0" :max="100000" class="w-full" />
          </NFormItem>
          <NFormItem :label="t('chores.minutes')" :show-feedback="false">
            <NInputNumber
              v-model:value="form.estimatedMinutes"
              :min="1"
              :max="1440"
              clearable
              class="w-full"
            />
          </NFormItem>
          <NFormItem :label="t('chores.minAge')" :show-feedback="false">
            <NInputNumber v-model:value="form.minAge" :min="1" :max="25" clearable class="w-full" />
          </NFormItem>
        </div>
      </NCard>

      <NCard :title="t('chores.whenTitle')">
        <div class="flex flex-col gap-3">
          <p class="hint">{{ t('chores.slotsHint') }}</p>
          <div v-for="(_, i) in form.slots" :key="i" class="flex items-center gap-2">
            <NTimePicker
              :formatted-value="form.slots[i]"
              value-format="HH:mm"
              format="HH:mm"
              :clearable="false"
              class="max-w-36"
              @update:formatted-value="form.slots[i] = $event ?? form.slots[i]"
            />
            <NButton quaternary type="error" size="small" @click="form.slots.splice(i, 1)">{{
              t('chores.remove')
            }}</NButton>
          </div>
          <div>
            <NButton
              dashed
              size="small"
              :disabled="form.slots.length >= 6"
              @click="form.slots.push('18:00')"
            >
              {{ t('chores.addSlot') }}
            </NButton>
          </div>
          <p class="hint">{{ t('chores.scheduleSoon') }}</p>
        </div>
      </NCard>

      <NCard :title="t('chores.stepsTitle')">
        <div class="flex flex-col gap-2">
          <div v-for="(_, i) in form.steps" :key="i" class="flex items-center gap-2">
            <span class="w-6 text-right text-sm text-gray-500">{{ i + 1 }}.</span>
            <NInput
              v-model:value="form.steps[i]"
              :maxlength="200"
              :input-props="{ 'aria-label': t('chores.stepN', { n: i + 1 }) }"
            />
            <NButton quaternary type="error" size="small" @click="form.steps.splice(i, 1)">{{
              t('chores.remove')
            }}</NButton>
          </div>
          <div>
            <NButton
              dashed
              size="small"
              :disabled="form.steps.length >= 20"
              @click="form.steps.push('')"
            >
              {{ t('chores.addStep') }}
            </NButton>
          </div>
        </div>
      </NCard>

      <NCard :title="t('chores.checkingTitle')">
        <div class="flex flex-col gap-4">
          <div class="flex items-center gap-2">
            <NSwitch v-model:value="form.requiresApproval" aria-labelledby="lbl-approval" />
            <span id="lbl-approval">{{ t('chores.requiresApproval') }}</span>
          </div>
          <div v-if="form.requiresApproval" class="flex flex-wrap items-center gap-3">
            <span>{{ t('chores.autoApprove') }}</span>
            <NRadioGroup v-model:value="form.autoApprove" size="small">
              <NRadioButton value="household">{{ t('chores.autoHousehold') }}</NRadioButton>
              <NRadioButton value="never">{{ t('chores.autoNever') }}</NRadioButton>
              <NRadioButton value="hours">{{ t('chores.autoAfter') }}</NRadioButton>
            </NRadioGroup>
            <NInputNumber
              v-if="form.autoApprove === 'hours'"
              v-model:value="form.autoApproveHours"
              :min="1"
              :max="720"
              class="w-28"
              :input-props="{ 'aria-label': t('settings.hours') }"
            >
              <template #suffix>{{ t('settings.hoursSuffix') }}</template>
            </NInputNumber>
          </div>
          <div class="flex flex-wrap items-center gap-4">
            <span>{{ t('chores.evidence') }}</span>
            <NInputNumber
              v-model:value="form.photos"
              :min="0"
              :max="5"
              class="w-32"
              :input-props="{ 'aria-label': t('chores.photos') }"
            >
              <template #suffix>{{ t('chores.photosSuffix') }}</template>
            </NInputNumber>
            <NCheckbox v-model:checked="form.beforeAfter" :disabled="form.photos < 2">{{
              t('chores.beforeAfter')
            }}</NCheckbox>
            <NCheckbox v-model:checked="form.video">{{ t('chores.video') }}</NCheckbox>
            <NCheckbox
              v-model:checked="form.checklist"
              :disabled="!form.steps.some((s) => s.trim())"
            >
              {{ t('chores.checklist') }}
            </NCheckbox>
            <NCheckbox v-model:checked="form.note">{{ t('chores.note') }}</NCheckbox>
          </div>
          <div class="flex items-center gap-2">
            <NSwitch v-model:value="form.escalate" aria-labelledby="lbl-escalate" />
            <span id="lbl-escalate">{{ t('chores.escalate') }}</span>
          </div>
        </div>
      </NCard>

      <NCard :title="t('chores.whereTitle')">
        <div class="grid gap-4 sm:grid-cols-2">
          <NFormItem :label="t('chores.location')" :show-feedback="false">
            <NSelect v-model:value="form.locationId" :options="locationOptions" clearable />
          </NFormItem>
          <NFormItem :label="t('chores.equipment')" :show-feedback="false">
            <NSelect
              v-model:value="form.equipmentIds"
              :options="equipmentOptions"
              multiple
              clearable
            />
          </NFormItem>
        </div>
        <p class="hint mt-2">
          <RouterLink :to="{ name: 'chore-catalog' }" class="underline">{{
            t('chores.manageCatalog')
          }}</RouterLink>
        </p>
      </NCard>

      <NCard :title="t('chores.whoTitle')">
        <div class="flex flex-col gap-3">
          <p class="hint">{{ t('chores.eligibilityHint') }}</p>
          <div
            v-for="(e, i) in form.eligibility"
            :key="e.kidId"
            class="flex flex-wrap items-center gap-3"
          >
            <span class="w-32 font-medium">{{ kidName(e.kidId) }}</span>
            <NRadioGroup v-model:value="e.allow" size="small">
              <NRadioButton :value="true">{{ t('chores.allow') }}</NRadioButton>
              <NRadioButton :value="false">{{ t('chores.deny') }}</NRadioButton>
            </NRadioGroup>
            <NButton quaternary type="error" size="small" @click="form.eligibility.splice(i, 1)">{{
              t('chores.remove')
            }}</NButton>
          </div>
          <NSelect
            v-if="kidOptions.length"
            :value="null"
            :options="kidOptions"
            :placeholder="t('chores.addException')"
            class="max-w-xs"
            @update:value="addEligibility"
          />
          <div class="flex items-center gap-2">
            <NSwitch v-model:value="form.disabled" aria-labelledby="lbl-disabled" />
            <span id="lbl-disabled">{{ t('chores.disableLabel') }}</span>
          </div>
        </div>
      </NCard>

      <div class="flex flex-wrap justify-between gap-2">
        <NButton v-if="!isNew" secondary type="error" @click="remove">{{
          t('chores.delete')
        }}</NButton>
        <span v-else />
        <div class="flex gap-2">
          <RouterLink :to="{ name: 'chores' }"
            ><NButton quaternary>{{ t('account.cancel') }}</NButton></RouterLink
          >
          <NButton type="primary" attr-type="submit" :loading="saving">{{
            isNew ? t('chores.create') : t('kids.save')
          }}</NButton>
        </div>
      </div>
    </form>
  </div>
</template>

<style scoped>
@reference "../../style.css";
.hint {
  @apply text-sm text-gray-500 dark:text-neutral-400;
}
</style>
