<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCard,
  NFormItem,
  NInput,
  NInputNumber,
  NSelect,
  NSpin,
  NSwitch,
  NTimePicker,
  useMessage,
} from 'naive-ui'
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import type { components } from '@/api/schema'
import { householdTimeZone } from '@/format'
import {
  type HouseholdFieldErrors,
  householdFieldErrors,
  milestoneProblem,
  type StreakMilestone,
  sortMilestones,
} from '@/settings/form'
import { timeZones, weekStarts } from '@/setup/wizard'

type Settings = components['schemas']['HouseholdSettings']

const { t } = useI18n()
const message = useMessage()
const form = ref<Settings>()
const autoApproveOn = ref(false)
const autoApproveHours = ref(24)
const saving = ref(false)
const error = ref<ApiError>()
const fieldErrors = ref<HouseholdFieldErrors>({})
const milestones = ref<StreakMilestone[]>([])
const milestoneError = ref<string>()

const zoneOptions = timeZones().map((z) => ({ value: z, label: z }))
const weekOptions = weekStarts.map((value) => ({
  value,
  label: t(`setup.household.weekdays.${value}`),
}))
const visibilityOptions = [
  { value: 'private', label: t('settings.visibility.private') },
  { value: 'leaderboard', label: t('settings.visibility.leaderboard') },
]

function addMilestone() {
  const last = milestones.value.at(-1)
  milestones.value.push({ days: (last?.days ?? 0) + 7, points: last?.points ?? 10 })
}

function load(s: Settings) {
  form.value = { ...s }
  milestones.value = (s.streakMilestones ?? []).map((m) => ({ ...m }))
  autoApproveOn.value = s.autoApproveHours != null
  autoApproveHours.value = s.autoApproveHours ?? 24
  householdTimeZone.value = s.timezone
}

onMounted(async () => {
  const { data, error: e, response } = await api.GET('/household')
  if (data) load(data)
  else error.value = ApiError.fromProblem(response.status, e)
})

async function save() {
  if (!form.value) return
  milestoneError.value = milestoneProblem(milestones.value)
  if (milestoneError.value) {
    fieldErrors.value = { streakMilestones: true }
    return
  }
  saving.value = true
  error.value = undefined
  fieldErrors.value = {}
  try {
    const { autoApproveHours: _, streakMilestones: __, ...rest } = form.value
    const sorted = sortMilestones(milestones.value)
    const body: Settings = {
      ...rest,
      ...(sorted.length ? { streakMilestones: sorted } : {}),
      ...(autoApproveOn.value ? { autoApproveHours: autoApproveHours.value } : {}),
    }
    const { data, error: e, response } = await api.PUT('/household', { body })
    if (!data) {
      error.value = ApiError.fromProblem(response.status, e)
      fieldErrors.value = householdFieldErrors(error.value.fieldErrors)
      return
    }
    load(data)
    message.success(t('settings.saved'))
  } catch (e) {
    error.value = ApiError.network(e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <NCard :title="t('settings.household')">
    <NSpin v-if="!form && !error" />
    <NAlert v-if="error" type="error" :show-icon="false" class="mb-4">
      {{ error.detail || error.code }}
      <template v-if="Object.keys(fieldErrors).length"> {{ t('settings.fieldInvalid') }}</template>
    </NAlert>
    <form v-if="form" class="flex flex-col gap-6" @submit.prevent="save">
      <section class="grid gap-4 sm:grid-cols-2">
        <NFormItem
          :label="t('setup.household.name')"
          :label-props="{ for: 'hh-name' }"
          :show-feedback="false"
        >
          <NInput
            v-model:value="form.name"
            :maxlength="60"
            :status="fieldErrors.name ? 'error' : undefined"
            :input-props="{ id: 'hh-name' }"
          />
        </NFormItem>
        <NFormItem :label="t('setup.household.timezone')" :show-feedback="false">
          <NSelect v-model:value="form.timezone" :options="zoneOptions" filterable />
        </NFormItem>
        <NFormItem :label="t('setup.household.dayEnd')" :show-feedback="false">
          <NTimePicker
            :formatted-value="form.dayEnd"
            value-format="HH:mm"
            format="HH:mm"
            :clearable="false"
            class="w-full"
            @update:formatted-value="form.dayEnd = $event ?? form.dayEnd"
          />
        </NFormItem>
        <NFormItem :label="t('setup.household.weekStart')" :show-feedback="false">
          <NSelect v-model:value="form.weekStart" :options="weekOptions" />
        </NFormItem>
      </section>
      <p class="hint -mt-4">{{ t('settings.calendarHint') }}</p>

      <section class="flex flex-col gap-4">
        <h3 class="font-medium">{{ t('settings.points') }}</h3>
        <div class="flex items-center gap-2">
          <NSwitch v-model:value="form.allowNegativeBalance" aria-labelledby="lbl-negative" />
          <span id="lbl-negative">{{ t('settings.allowNegative') }}</span>
        </div>
        <p class="hint -mt-2">{{ t('settings.allowNegativeHint') }}</p>
        <NFormItem :label="t('settings.visibility.label')" :show-feedback="false" class="max-w-md">
          <NSelect v-model:value="form.siblingVisibility" :options="visibilityOptions" />
        </NFormItem>
      </section>

      <section class="flex flex-col gap-3">
        <h3 class="font-medium">{{ t('settings.streaks') }}</h3>
        <p class="hint">{{ t('settings.streaksHint') }}</p>
        <ul class="flex flex-col gap-2">
          <li v-for="(m, i) in milestones" :key="i" class="flex items-center gap-2">
            <NInputNumber
              v-model:value="m.days"
              :min="1"
              :max="3650"
              :precision="0"
              :status="fieldErrors.streakMilestones ? 'error' : undefined"
              class="w-32"
              :input-props="{ 'aria-label': t('settings.milestoneDaysLabel', { n: i + 1 }) }"
            >
              <template #suffix>{{ t('settings.daysSuffix') }}</template>
            </NInputNumber>
            <NInputNumber
              v-model:value="m.points"
              :min="1"
              :max="100000"
              :precision="0"
              :status="fieldErrors.streakMilestones ? 'error' : undefined"
              class="w-32"
              :input-props="{ 'aria-label': t('settings.milestonePointsLabel', { n: i + 1 }) }"
            >
              <template #suffix>{{ t('settings.milestonePointsSuffix') }}</template>
            </NInputNumber>
            <NButton
              quaternary
              size="small"
              :aria-label="t('settings.milestoneRemove', { n: i + 1 })"
              @click="milestones.splice(i, 1)"
              >&times;</NButton
            >
          </li>
        </ul>
        <p v-if="milestoneError" class="text-sm text-red-600" role="alert">
          {{ t(milestoneError) }}
        </p>
        <div>
          <NButton size="small" :disabled="milestones.length >= 20" @click="addMilestone">{{
            t('settings.milestoneAdd')
          }}</NButton>
        </div>
      </section>

      <section class="flex flex-col gap-4">
        <h3 class="font-medium">{{ t('settings.approvals') }}</h3>
        <div class="flex flex-wrap items-center gap-3">
          <NSwitch v-model:value="autoApproveOn" aria-labelledby="lbl-auto" />
          <span id="lbl-auto">{{ t('settings.autoApprove') }}</span>
          <NInputNumber
            v-if="autoApproveOn"
            v-model:value="autoApproveHours"
            :status="fieldErrors.autoApproveHours ? 'error' : undefined"
            :min="1"
            :max="720"
            class="w-32"
            :input-props="{ 'aria-label': t('settings.hours') }"
          >
            <template #suffix>{{ t('settings.hoursSuffix') }}</template>
          </NInputNumber>
        </div>
        <p class="hint -mt-2">{{ t('settings.autoApproveHint') }}</p>
        <div class="grid gap-4 sm:grid-cols-2">
          <NFormItem :label="t('settings.retention')" :show-feedback="false">
            <NInputNumber
              v-model:value="form.mediaRetentionDays"
              :status="fieldErrors.mediaRetentionDays ? 'error' : undefined"
              :min="1"
              :max="3650"
              class="w-full"
            >
              <template #suffix>{{ t('settings.daysSuffix') }}</template>
            </NInputNumber>
          </NFormItem>
          <NFormItem :label="t('settings.grace')" :show-feedback="false">
            <NInputNumber
              v-model:value="form.offlineGraceMinutes"
              :status="fieldErrors.offlineGraceMinutes ? 'error' : undefined"
              :min="0"
              :max="1440"
              class="w-full"
            >
              <template #suffix>{{ t('settings.minutesSuffix') }}</template>
            </NInputNumber>
          </NFormItem>
        </div>
        <p class="hint -mt-2">{{ t('settings.retentionHint') }} {{ t('settings.graceHint') }}</p>
        <NFormItem
          :label="t('settings.defaultMinutes')"
          :show-feedback="false"
          class="sm:max-w-[calc(50%-0.5rem)]"
        >
          <NInputNumber
            v-model:value="form.defaultChoreMinutes"
            :status="fieldErrors.defaultChoreMinutes ? 'error' : undefined"
            :min="1"
            :max="240"
            :precision="0"
            class="w-full"
          >
            <template #suffix>{{ t('settings.minutesSuffix') }}</template>
          </NInputNumber>
        </NFormItem>
        <p class="hint -mt-2">{{ t('settings.defaultMinutesHint') }}</p>
      </section>

      <div class="flex justify-end">
        <NButton type="primary" attr-type="submit" :loading="saving">{{ t('kids.save') }}</NButton>
      </div>
    </form>
  </NCard>
</template>

<style scoped>
@reference "../../style.css";
.hint {
  @apply text-sm text-gray-500 dark:text-neutral-400;
}
</style>
