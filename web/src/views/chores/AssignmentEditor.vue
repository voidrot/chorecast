<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCheckbox,
  NCheckboxGroup,
  NFormItem,
  NRadio,
  NRadioGroup,
} from 'naive-ui'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { components } from '@/api/schema'
import {
  ASSIGNMENT_MODES,
  type AssignmentFieldErrors,
  type AssignmentForm,
  moveKid,
} from '@/chores/form'

type Kid = components['schemas']['Kid']

const form = defineModel<AssignmentForm>({ required: true })
const props = defineProps<{
  /** Every kid, archived ones included; archived kids show only while assigned. */
  kids: Kid[]
  /** The server's 422 messages for these controls. */
  errors: AssignmentFieldErrors
  /** Saving would restart this chore's rotation. */
  resetsRotation: boolean
  /** The local validation problem to show (set after a save attempt), if any. */
  problem?: string
}>()

const { t } = useI18n()

const kidChoices = computed(() =>
  props.kids.filter((k) => !k.archivedAt || form.value.kidIds.includes(k.id)),
)
const kidName = (id: number) => {
  const k = props.kids.find((k) => k.id === id)
  if (!k) return `#${id}`
  return k.archivedAt ? t('chores.assignment.archivedKid', { name: k.displayName }) : k.displayName
}
const kidsError = computed(
  () => props.errors.kidIds ?? (props.problem ? t(props.problem) : undefined),
)

function move(i: number, by: -1 | 1) {
  form.value.kidIds = moveKid(form.value.kidIds, i, by)
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <NFormItem
      :label="t('chores.assignment.mode')"
      :show-feedback="!!errors.mode"
      :feedback="errors.mode"
      :validation-status="errors.mode ? 'error' : undefined"
    >
      <NRadioGroup v-model:value="form.mode" name="assignment-mode" class="flex flex-col gap-2">
        <NRadio v-for="m in ASSIGNMENT_MODES" :key="m" :value="m">
          <span class="font-medium">{{ t(`chores.assignment.modes.${m}`) }}</span>
          <span class="hint block">{{ t(`chores.assignment.modeHints.${m}`) }}</span>
        </NRadio>
      </NRadioGroup>
    </NFormItem>

    <fieldset v-if="form.mode !== 'everyone'" class="flex flex-col gap-1 border-0 p-0">
      <legend class="font-medium">{{ t('chores.assignment.kids') }}</legend>
      <p class="hint">{{ t(`chores.assignment.kidsHint.${form.mode}`) }}</p>
      <NCheckboxGroup v-model:value="form.kidIds">
        <div class="flex flex-wrap gap-x-4 gap-y-2">
          <NCheckbox v-for="k in kidChoices" :key="k.id" :value="k.id" :label="kidName(k.id)" />
        </div>
      </NCheckboxGroup>
      <p v-if="!kidChoices.length" class="hint">{{ t('chores.assignment.noKids') }}</p>
      <p v-if="kidsError" class="text-sm text-red-600 dark:text-red-400" role="alert">
        {{ kidsError }}
      </p>
    </fieldset>

    <div v-if="form.mode === 'rotation' && form.kidIds.length > 1" class="flex flex-col gap-1">
      <span id="lbl-assign-order" class="font-medium">{{ t('chores.assignment.order') }}</span>
      <p class="hint">{{ t('chores.assignment.orderHint') }}</p>
      <ol aria-labelledby="lbl-assign-order" class="flex max-w-sm flex-col gap-1">
        <li v-for="(id, i) in form.kidIds" :key="id" class="flex items-center gap-2">
          <span class="w-6 text-right text-sm text-gray-500">{{ i + 1 }}.</span>
          <span class="flex-1">{{ kidName(id) }}</span>
          <NButton
            quaternary
            size="small"
            :disabled="i === 0"
            :aria-label="t('chores.assignment.moveUp', { name: kidName(id) })"
            @click="move(i, -1)"
            >&uarr;</NButton
          >
          <NButton
            quaternary
            size="small"
            :disabled="i === form.kidIds.length - 1"
            :aria-label="t('chores.assignment.moveDown', { name: kidName(id) })"
            @click="move(i, 1)"
            >&darr;</NButton
          >
        </li>
      </ol>
    </div>

    <div v-if="form.mode === 'shared'" class="grid gap-4 sm:grid-cols-2">
      <NFormItem
        :label="t('chores.assignment.sharedPoints')"
        :show-feedback="!!errors.sharedPoints"
        :feedback="errors.sharedPoints"
        :validation-status="errors.sharedPoints ? 'error' : undefined"
      >
        <NRadioGroup
          v-model:value="form.sharedPoints"
          name="assignment-points"
          class="flex flex-col gap-2"
        >
          <NRadio value="completer_takes_all">{{
            t('chores.assignment.points.completer_takes_all')
          }}</NRadio>
          <NRadio value="split_evenly">{{ t('chores.assignment.points.split_evenly') }}</NRadio>
        </NRadioGroup>
      </NFormItem>
      <NFormItem
        :label="t('chores.assignment.sharedPenalty')"
        :show-feedback="!!errors.sharedPenalty"
        :feedback="errors.sharedPenalty"
        :validation-status="errors.sharedPenalty ? 'error' : undefined"
      >
        <NRadioGroup
          v-model:value="form.sharedPenalty"
          name="assignment-penalty"
          class="flex flex-col gap-2"
        >
          <NRadio value="all">{{ t('chores.assignment.penalty.all') }}</NRadio>
          <NRadio value="none">{{ t('chores.assignment.penalty.none') }}</NRadio>
        </NRadioGroup>
      </NFormItem>
    </div>

    <NAlert v-if="resetsRotation" type="warning" :show-icon="false">
      {{ t('chores.assignment.resetWarning') }}
    </NAlert>
  </div>
</template>

<style scoped>
@reference "../../style.css";
.hint {
  @apply text-sm text-gray-500 dark:text-neutral-400;
}
</style>
