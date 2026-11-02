<script setup lang="ts">
import { NAlert, NButton, NInput, NModal } from 'naive-ui'
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { buildReversalNote, NOTE_MAX, noteLength } from '@/points/ledger'

const props = defineProps<{ show: boolean; busy: boolean; error?: string }>()
const emit = defineEmits<{ close: []; submit: [note: string] }>()

const { t } = useI18n()
const note = ref('')
const problem = ref<string>()

// Start each reversal with a clean form.
watch(
  () => props.show,
  (shown) => {
    if (!shown) return
    note.value = ''
    problem.value = undefined
  },
)

function submit() {
  const result = buildReversalNote(note.value)
  if ('problem' in result) {
    problem.value = result.problem
    return
  }
  problem.value = undefined
  emit('submit', result.note)
}
</script>

<template>
  <NModal
    :show="show"
    preset="card"
    :title="t('points.reverse.title')"
    class="max-w-md"
    @update:show="(v: boolean) => !v && emit('close')"
  >
    <form class="flex flex-col gap-4" @submit.prevent="submit">
      <p class="text-sm">{{ t('points.reverse.explain') }}</p>
      <div class="flex flex-col gap-1">
        <label for="rev-note" class="text-sm font-medium">{{ t('points.reverse.note') }}</label>
        <NInput
          v-model:value="note"
          type="textarea"
          :allow-input="(v: string) => noteLength(v) <= NOTE_MAX"
          :count-graphemes="noteLength"
          show-count
          :autosize="{ minRows: 2 }"
          :input-props="{ id: 'rev-note' }"
        />
      </div>
      <NAlert v-if="problem" type="warning" :show-icon="false">{{
        t(`points.reverse.${problem}`)
      }}</NAlert>
      <NAlert v-if="error" type="error" :show-icon="false">{{ error }}</NAlert>
      <div class="flex justify-end gap-2">
        <NButton @click="emit('close')">{{ t('points.reverse.cancel') }}</NButton>
        <NButton type="primary" attr-type="submit" :loading="busy">{{
          t('points.reverse.submit')
        }}</NButton>
      </div>
    </form>
  </NModal>
</template>
