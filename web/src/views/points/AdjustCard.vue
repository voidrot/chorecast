<script setup lang="ts">
import { NAlert, NButton, NCard, NInput, NInputNumber, NRadioButton, NRadioGroup } from 'naive-ui'
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  type AdjustmentBody,
  AMOUNT_MAX,
  buildAdjustment,
  NOTE_MAX,
  noteLength,
} from '@/points/ledger'

defineProps<{ busy: boolean; error?: string }>()
const emit = defineEmits<{ submit: [body: AdjustmentBody, key: string, done: () => void] }>()

const { t } = useI18n()
const sign = ref<'add' | 'subtract'>('add')
const amount = ref<number | null>(null)
const note = ref('')
const problem = ref<string>()

// One key per filled-in form: a retry of the same submission replays the first response, while a
// success or any edit starts a new submission.
let key = crypto.randomUUID()
watch([sign, amount, note], () => {
  key = crypto.randomUUID()
})

function submit() {
  const result = buildAdjustment(sign.value, amount.value, note.value)
  if ('problem' in result) {
    problem.value = result.problem
    return
  }
  problem.value = undefined
  emit('submit', result.body, key, () => {
    amount.value = null
    note.value = ''
    key = crypto.randomUUID()
  })
}
</script>

<template>
  <NCard :title="t('points.adjust.title')">
    <form class="flex flex-col gap-4" @submit.prevent="submit">
      <div class="flex flex-col gap-1">
        <span id="adj-sign" class="text-sm font-medium">{{ t('points.adjust.direction') }}</span>
        <NRadioGroup v-model:value="sign" name="adj-sign" aria-labelledby="adj-sign">
          <NRadioButton value="add">{{ t('points.adjust.add') }}</NRadioButton>
          <NRadioButton value="subtract">{{ t('points.adjust.subtract') }}</NRadioButton>
        </NRadioGroup>
      </div>
      <div class="flex flex-col gap-1">
        <label for="adj-amount" class="text-sm font-medium">{{ t('points.adjust.amount') }}</label>
        <NInputNumber
          v-model:value="amount"
          :min="1"
          :max="AMOUNT_MAX"
          :precision="0"
          :show-button="false"
          :input-props="{ id: 'adj-amount' }"
        />
      </div>
      <div class="flex flex-col gap-1">
        <label for="adj-note" class="text-sm font-medium">{{ t('points.adjust.note') }}</label>
        <NInput
          v-model:value="note"
          type="textarea"
          :allow-input="(v: string) => noteLength(v) <= NOTE_MAX"
          :count-graphemes="noteLength"
          show-count
          :autosize="{ minRows: 2 }"
          :input-props="{ id: 'adj-note' }"
        />
        <p class="hint text-sm text-gray-500 dark:text-neutral-400">
          {{ t('points.adjust.noteHint') }}
        </p>
      </div>
      <NAlert v-if="problem" type="warning" :show-icon="false">{{
        t(`points.adjust.${problem}`)
      }}</NAlert>
      <NAlert v-if="error" type="error" :show-icon="false">{{ error }}</NAlert>
      <div>
        <NButton type="primary" attr-type="submit" :loading="busy">{{
          t('points.adjust.submit')
        }}</NButton>
      </div>
    </form>
  </NCard>
</template>
