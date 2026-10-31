<script setup lang="ts">
import {
  NAlert,
  NCheckbox,
  NCheckboxGroup,
  NDatePicker,
  NFormItem,
  NInputNumber,
  NRadioButton,
  NRadioGroup,
  NSelect,
  NSpin,
} from 'naive-ui'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import {
  ruleFromScheduleForm,
  type ScheduleForm,
  type ScheduleOccurrence,
  scheduleProblems,
  WEEKDAYS,
  type Weekday,
} from '@/chores/schedule'
import { formatDate } from '@/format'

const form = defineModel<ScheduleForm>({ required: true })
const props = defineProps<{ slots: string[] }>()

const { t, locale } = useI18n()

const PREVIEW_COUNT = 8

const weekdayName = (w: Weekday, style: 'short' | 'long' = 'short') =>
  new Intl.DateTimeFormat(locale.value, { weekday: style, timeZone: 'UTC' }).format(
    new Date(Date.UTC(2026, 10, 1 + WEEKDAYS.indexOf(w))), // 2026-11-01 is a Sunday
  )

const modeOptions = computed(() =>
  (['none', 'once', 'daily', 'weekly', 'monthly', 'flexible_weekly'] as const).map((m) => ({
    value: m,
    label: t(`schedule.modes.${m}`),
  })),
)
const monthDayOptions = computed(() => [
  ...Array.from({ length: 31 }, (_, i) => ({ value: i + 1, label: String(i + 1) })),
  { value: -1, label: t('schedule.lastDay') },
])
const nthOptions = computed(() =>
  [1, 2, 3, 4, 5, -1].map((n) => ({ value: n, label: t(`schedule.nth.${n}`) })),
)
const weekdayOptions = computed(() =>
  WEEKDAYS.map((w) => ({ value: w, label: weekdayName(w, 'long') })),
)
const unit = computed(() =>
  form.value.mode === 'weekly' ? 'weeks' : form.value.mode === 'monthly' ? 'months' : 'days',
)

const rule = computed(() => ruleFromScheduleForm(form.value))
const problems = computed(() => scheduleProblems(form.value))

const preview = ref<ScheduleOccurrence[]>([])
const previewError = ref<ApiError>()
const previewing = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
let seq = 0

async function loadPreview() {
  const mine = ++seq
  if (!rule.value || problems.value.length) {
    preview.value = []
    previewError.value = undefined
    return
  }
  previewing.value = true
  try {
    const res = await api.POST('/chores/preview-schedule', {
      body: { rule: rule.value, slots: [...props.slots], count: PREVIEW_COUNT },
    })
    if (mine !== seq) return // a newer request is on its way
    if (res.data) {
      preview.value = res.data.occurrences
      previewError.value = undefined
    } else {
      preview.value = []
      previewError.value = ApiError.fromProblem(res.response.status, res.error)
    }
  } catch (e) {
    if (mine === seq) previewError.value = ApiError.network(e)
  } finally {
    if (mine === seq) previewing.value = false
  }
}

watch(
  () => JSON.stringify([rule.value, props.slots]),
  () => {
    clearTimeout(timer)
    timer = setTimeout(loadPreview, 300)
  },
  { immediate: true },
)
onBeforeUnmount(() => clearTimeout(timer))

// "Every 2 weeks" shouldn't silently become "every 2 months" when the parent switches type.
// Only for edits of the same form: a freshly loaded form keeps its interval.
watch(
  () => [form.value, form.value.mode] as const,
  ([f, mode], [prev, prevMode]) => {
    if (f === prev && mode !== prevMode) f.interval = 1
  },
)

/** Occurrences to list; a flexible week's completions are one row with a count. */
const rows = computed(() => {
  const out: { o: ScheduleOccurrence; count: number }[] = []
  for (const o of preview.value) {
    const last = out[out.length - 1]
    if (o.flexible && last?.o.flexible && last.o.date === o.date) last.count++
    else out.push({ o, count: 1 })
  }
  return out
})

function occurrenceDate(o: ScheduleOccurrence): string {
  const opts: Intl.DateTimeFormatOptions = o.flexible
    ? { month: 'short', day: 'numeric' }
    : { weekday: 'short', month: 'short', day: 'numeric', year: 'numeric' }
  const d = formatDate(o.date, opts, locale.value)
  return o.flexible ? t('schedule.weekOf', { date: d }) : d
}

function occurrenceWhen(o: ScheduleOccurrence, count: number): string {
  if (o.flexible) return t('schedule.byWeekEnd', { n: count }, count)
  return o.slot ?? t('schedule.byDayEnd')
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <NFormItem :label="t('schedule.repeats')" :show-feedback="false" class="max-w-xs">
      <NSelect v-model:value="form.mode" :options="modeOptions" data-testid="schedule-mode" />
    </NFormItem>

    <template v-if="form.mode === 'once'">
      <NFormItem :label="t('schedule.date')" :show-feedback="false" class="max-w-xs">
        <NDatePicker
          v-model:formatted-value="form.date"
          value-format="yyyy-MM-dd"
          type="date"
          :clearable="false"
        />
      </NFormItem>
    </template>

    <template v-else-if="form.mode !== 'none'">
      <div
        v-if="form.mode === 'daily' || form.mode === 'weekly' || form.mode === 'monthly'"
        class="flex items-center gap-2"
      >
        <span id="lbl-every">{{ t('schedule.every') }}</span>
        <NInputNumber
          v-model:value="form.interval"
          :min="1"
          :max="99"
          :precision="0"
          class="max-w-28"
          :input-props="{ 'aria-labelledby': 'lbl-every lbl-unit' }"
          @update:value="form.interval = $event ?? 1"
        />
        <span id="lbl-unit">{{ t(`schedule.units.${unit}`, form.interval) }}</span>
      </div>

      <NFormItem v-if="form.mode === 'weekly'" :label="t('schedule.on')" :show-feedback="false">
        <NCheckboxGroup v-model:value="form.weekdays">
          <div class="flex flex-wrap gap-3">
            <NCheckbox v-for="w in WEEKDAYS" :key="w" :value="w" :label="weekdayName(w)" />
          </div>
        </NCheckboxGroup>
      </NFormItem>

      <template v-if="form.mode === 'monthly'">
        <NRadioGroup v-model:value="form.monthlyBy">
          <NRadioButton value="days">{{ t('schedule.byDays') }}</NRadioButton>
          <NRadioButton value="nth">{{ t('schedule.byNth') }}</NRadioButton>
        </NRadioGroup>
        <div v-if="form.monthlyBy === 'days'" class="flex flex-col gap-1">
          <NFormItem :label="t('schedule.monthDays')" :show-feedback="false" class="max-w-md">
            <NSelect
              v-model:value="form.monthDays"
              :options="monthDayOptions"
              multiple
              :max-tag-count="8"
            />
          </NFormItem>
          <p class="hint">{{ t('schedule.shortMonths') }}</p>
        </div>
        <div v-else class="flex flex-wrap gap-3">
          <NFormItem :label="t('schedule.nthLabel')" :show-feedback="false" class="w-32">
            <NSelect v-model:value="form.nthN" :options="nthOptions" />
          </NFormItem>
          <NFormItem :label="t('schedule.weekday')" :show-feedback="false" class="w-44">
            <NSelect v-model:value="form.nthWeekday" :options="weekdayOptions" />
          </NFormItem>
        </div>
      </template>

      <div v-if="form.mode === 'flexible_weekly'" class="flex flex-col gap-1">
        <div class="flex items-center gap-2">
          <NInputNumber
            v-model:value="form.timesPerWeek"
            :min="1"
            :max="14"
            :precision="0"
            class="max-w-28"
            :input-props="{ 'aria-labelledby': 'lbl-times' }"
            @update:value="form.timesPerWeek = $event ?? 1"
          />
          <span id="lbl-times">{{ t('schedule.timesPerWeek', form.timesPerWeek) }}</span>
        </div>
        <p class="hint">{{ t('schedule.flexibleHint') }}</p>
      </div>

      <div class="flex flex-wrap gap-3">
        <NFormItem :label="t('schedule.startDate')" :show-feedback="false" class="w-48">
          <NDatePicker
            v-model:formatted-value="form.startDate"
            value-format="yyyy-MM-dd"
            type="date"
            :clearable="false"
          />
        </NFormItem>
        <NFormItem :label="t('schedule.endDate')" :show-feedback="false" class="w-48">
          <NDatePicker
            v-model:formatted-value="form.endDate"
            value-format="yyyy-MM-dd"
            type="date"
            clearable
          />
        </NFormItem>
      </div>
    </template>

    <div v-if="form.mode !== 'none'" class="flex flex-col gap-2" data-testid="schedule-preview">
      <div class="flex items-center gap-2 font-medium">
        {{ t('schedule.previewTitle') }}
        <NSpin v-if="previewing" :size="14" />
      </div>
      <NAlert v-if="previewError" type="warning" :show-icon="false">
        {{ previewError.fieldErrors?.[0]?.message || previewError.detail || previewError.code }}
      </NAlert>
      <p v-else-if="problems.length" class="hint">{{ t(problems[0]) }}</p>
      <p v-else-if="!previewing && !preview.length" class="hint">{{ t('schedule.previewNone') }}</p>
      <ul v-else class="flex flex-col gap-1 text-sm">
        <li v-for="(r, i) in rows" :key="i" class="flex gap-3">
          <span class="w-48">{{ occurrenceDate(r.o) }}</span>
          <span class="text-gray-500 dark:text-neutral-400">{{
            occurrenceWhen(r.o, r.count)
          }}</span>
        </li>
      </ul>
    </div>
  </div>
</template>

<style scoped>
@reference "../../style.css";
.hint {
  @apply text-sm text-gray-500 dark:text-neutral-400;
}
</style>
