<script setup lang="ts">
import { NAlert, NButton, NPopconfirm, NTag } from 'naive-ui'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import { formatDate, formatDateTime } from '@/format'
import { type Action, actionsFor, type Evidence, hasEvidence, type Instance } from '@/today/plan'

const props = defineProps<{ instance: Instance; busy: boolean }>()
const emit = defineEmits<{ act: [action: Action] }>()

const { t, te } = useI18n()

const actions = computed(() => actionsFor(props.instance))
const statusType = computed(() => {
  switch (props.instance.status) {
    case 'approved':
      return 'success'
    case 'submitted':
      return 'info'
    case 'missed':
      return 'error'
    default:
      return 'default'
  }
})

const evidence = ref<Evidence[]>()
const evidenceError = ref<string>()
const evidenceOpen = ref(false)
const evidenceLoading = ref(false)

async function toggleEvidence() {
  evidenceOpen.value = !evidenceOpen.value
  if (!evidenceOpen.value || evidenceLoading.value) return
  // Reload each time it opens: signed photo links expire.
  evidenceLoading.value = true
  evidenceError.value = undefined
  try {
    const { data, error, response } = await api.GET('/instances/{instance}/evidence', {
      params: { path: { instance: props.instance.id } },
    })
    if (!data) throw ApiError.fromProblem(response.status, error)
    evidence.value = data.items
  } catch (e) {
    const err = e instanceof ApiError ? e : ApiError.network(e)
    evidenceError.value = te(`today.errors.${err.code}`)
      ? t(`today.errors.${err.code}`)
      : err.detail || err.code
  } finally {
    evidenceLoading.value = false
  }
}
</script>

<template>
  <article
    class="flex flex-col gap-2 rounded border border-gray-200 bg-white p-3 dark:border-neutral-800 dark:bg-neutral-900"
    :aria-label="instance.choreName"
  >
    <div class="flex flex-wrap items-center justify-between gap-2">
      <div class="flex flex-col">
        <span class="font-medium">{{ instance.choreName }}</span>
        <span class="text-sm text-gray-600 dark:text-neutral-400">
          {{ formatDate(instance.date, { dateStyle: 'full' }) }}
          <template v-if="instance.slot"> · {{ instance.slot }}</template>
          <template v-if="instance.planned"> · {{ t('today.planned') }}</template>
        </span>
        <span v-if="instance.rejectionCount" class="text-sm text-gray-600 dark:text-neutral-400">
          {{ t('today.rejectedBefore', { n: instance.rejectionCount }) }}
        </span>
      </div>
      <NTag :type="statusType" :bordered="false" size="small">
        {{ t(`today.status.${instance.status}`) }}
      </NTag>
    </div>

    <div v-if="actions.length || hasEvidence(instance)" class="flex flex-wrap gap-2">
      <template v-for="a in actions" :key="a">
        <NButton
          v-if="a === 'approve'"
          type="primary"
          size="small"
          :loading="busy"
          :disabled="busy"
          @click="emit('act', 'approve')"
          >{{
            t('today.actions.approve')
          }}</NButton
        >
        <NButton
          v-else-if="a === 'reject'"
          size="small"
          :disabled="busy"
          @click="emit('act', 'reject')"
          >{{
            t('today.actions.reject')
          }}</NButton
        >
        <NButton
          v-else-if="a === 'reassign'"
          size="small"
          :disabled="busy"
          @click="emit('act', 'reassign')"
          >{{
            t('today.actions.reassign')
          }}</NButton
        >
        <NButton
          v-else-if="a === 'complete' && instance.kidId === undefined"
          size="small"
          :disabled="busy"
          @click="emit('act', 'complete')"
          >{{
            t('today.actions.complete')
          }}</NButton
        >
        <NPopconfirm
          v-else
          :positive-text="t(`today.actions.${a}`)"
          :negative-text="t('today.cancel')"
          @positive-click="emit('act', a)"
        >
          <template #trigger>
            <NButton size="small" :disabled="busy">{{ t(`today.actions.${a}`) }}</NButton>
          </template>
          {{ t(`today.confirm.${a}`, { chore: instance.choreName }) }}
        </NPopconfirm>
      </template>
      <NButton
        v-if="hasEvidence(instance)"
        size="small"
        quaternary
        :aria-expanded="evidenceOpen"
        @click="toggleEvidence"
        >{{
          evidenceOpen ? t('today.hideEvidence') : t('today.showEvidence')
        }}</NButton
      >
    </div>

    <div
      v-if="evidenceOpen"
      class="flex flex-col gap-3 border-t border-gray-200 pt-2 dark:border-neutral-800"
    >
      <NAlert v-if="evidenceError" type="error">{{ evidenceError }}</NAlert>
      <p v-else-if="evidenceLoading" class="text-sm">{{ t('today.loading') }}</p>
      <p v-else-if="!evidence?.length" class="text-sm">{{ t('today.noEvidence') }}</p>
      <section v-for="e in evidence" :key="e.attempt" class="flex flex-col gap-1">
        <h4 class="text-sm font-medium">
          {{ t('today.attempt', { n: e.attempt, at: formatDateTime(e.submittedAt) }) }}
        </h4>
        <p v-if="e.note" class="text-sm">{{ t('today.note') }} {{ e.note }}</p>
        <p v-if="e.checklist.length" class="text-sm">
          {{ t('today.stepsDone', { n: e.checklist.length }) }}
        </p>
        <div v-if="e.photos.length" class="flex flex-wrap gap-2">
          <template v-for="(p, i) in e.photos" :key="p.mediaId">
            <span v-if="p.expired || !p.url" class="text-sm">{{ t('today.mediaExpired') }}</span>
            <a
              v-else
              :href="p.url"
              target="_blank"
              rel="noopener noreferrer"
              :aria-label="t('today.openPhoto', { n: i + 1, total: e.photos.length })"
            >
              <img
                :src="p.thumbUrl || p.url"
                :alt="t('today.photoAlt', { chore: instance.choreName, n: i + 1 })"
                class="h-20 w-20 rounded object-cover"
              >
            </a>
          </template>
        </div>
        <p v-else class="text-sm">{{ t('today.noPhotos') }}</p>
      </section>
    </div>
  </article>
</template>
