<script setup lang="ts">
import { NAlert, NButton, NInput, NModal, NSelect } from 'naive-ui'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  buildRejection,
  REJECT_REASONS,
  REJECT_TEXT_MAX,
  type RejectBody,
  type RejectReason,
} from '@/review/queue'

const props = defineProps<{ show: boolean; chore: string; busy: boolean; error?: string }>()
const emit = defineEmits<{ close: []; submit: [body: RejectBody] }>()

const { t } = useI18n()
const reason = ref<RejectReason | null>(null)
const text = ref('')
const empty = ref(false)

const options = computed(() =>
  REJECT_REASONS.map((r) => ({ value: r, label: t(`review.reasons.${r}`) })),
)

// Start each rejection with a clean form.
watch(
  () => props.show,
  (shown) => {
    if (!shown) return
    reason.value = null
    text.value = ''
    empty.value = false
  },
)

function submit() {
  const body = buildRejection(reason.value, text.value)
  empty.value = body === undefined
  if (body) emit('submit', body)
}
</script>

<template>
  <NModal
    :show="show"
    preset="card"
    :title="t('review.rejectTitle', { chore: chore })"
    class="max-w-md"
    @update:show="(v: boolean) => !v && emit('close')"
  >
    <form class="flex flex-col gap-4" @submit.prevent="submit">
      <div class="flex flex-col gap-1">
        <span class="text-sm font-medium" aria-hidden="true">{{ t('review.reasonLabel') }}</span>
        <NSelect
          v-model:value="reason"
          :options="options"
          clearable
          :aria-label="t('review.reasonLabel')"
        />
      </div>
      <div class="flex flex-col gap-1">
        <label for="rej-text" class="text-sm font-medium">{{ t('review.textLabel') }}</label>
        <NInput
          v-model:value="text"
          type="textarea"
          :maxlength="REJECT_TEXT_MAX"
          show-count
          :autosize="{ minRows: 2 }"
          :input-props="{ id: 'rej-text' }"
        />
      </div>
      <NAlert v-if="empty" type="warning">{{ t('review.reasonOrText') }}</NAlert>
      <NAlert v-if="error" type="error">{{ error }}</NAlert>
      <div class="flex justify-end gap-2">
        <NButton @click="emit('close')">{{ t('review.cancel') }}</NButton>
        <NButton type="primary" attr-type="submit" :loading="busy">{{
          t('review.reject')
        }}</NButton>
      </div>
    </form>
  </NModal>
</template>
