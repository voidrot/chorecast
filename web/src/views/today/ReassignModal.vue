<script setup lang="ts">
import { NAlert, NButton, NModal, NSelect } from 'naive-ui'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{
  show: boolean
  chore: string
  kids: { value: number; label: string }[]
  busy: boolean
  error?: string
}>()
const emit = defineEmits<{ close: []; submit: [kidId: number] }>()

const { t } = useI18n()
const kid = ref<number | null>(null)
const missing = ref(false)
const canSubmit = computed(() => props.kids.length > 0)

watch(
  () => props.show,
  (shown) => {
    if (!shown) return
    kid.value = null
    missing.value = false
  },
)

function submit() {
  missing.value = kid.value === null
  if (kid.value !== null) emit('submit', kid.value)
}
</script>

<template>
  <NModal
    :show="show"
    preset="card"
    :title="t('today.reassignTitle', { chore })"
    class="max-w-md"
    @update:show="(v: boolean) => !v && emit('close')"
  >
    <form class="flex flex-col gap-4" @submit.prevent="submit">
      <div class="flex flex-col gap-1">
        <span class="text-sm font-medium" aria-hidden="true">{{ t('today.reassignTo') }}</span>
        <NSelect v-model:value="kid" :options="kids" :aria-label="t('today.reassignTo')" />
      </div>
      <NAlert v-if="missing" type="warning">{{ t('today.chooseKid') }}</NAlert>
      <NAlert v-if="error" type="error">{{ error }}</NAlert>
      <div class="flex justify-end gap-2">
        <NButton @click="emit('close')">{{ t('today.cancel') }}</NButton>
        <NButton type="primary" attr-type="submit" :loading="busy" :disabled="!canSubmit">{{
          t('today.actions.reassign')
        }}</NButton>
      </div>
    </form>
  </NModal>
</template>
