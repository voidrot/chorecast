<script setup lang="ts">
import { NButton, NCard, NCheckbox, NModal, NTag } from 'naive-ui'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatDateTime } from '@/format'
import type { ReviewItem } from '@/review/queue'

const props = defineProps<{ item: ReviewItem; selected: boolean; busy: boolean }>()
const emit = defineEmits<{
  toggle: []
  approve: []
  reject: []
  photoError: []
}>()

const { t } = useI18n()
const photos = computed(() => props.item.submission?.photos ?? [])
const openIndex = ref<number>()
const open = computed({
  get: () => openIndex.value !== undefined,
  set: (v) => {
    if (!v) openIndex.value = undefined
  },
})
const current = computed(() =>
  openIndex.value === undefined ? undefined : photos.value[openIndex.value],
)

function step(delta: number) {
  if (openIndex.value === undefined) return
  const n = photos.value.length
  openIndex.value = (openIndex.value + delta + n) % n
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'ArrowRight') step(1)
  else if (e.key === 'ArrowLeft') step(-1)
}
</script>

<template>
  <NCard size="small" data-testid="review-item">
    <div class="flex flex-col gap-3">
      <div class="flex items-start justify-between gap-3">
        <NCheckbox
          :checked="selected"
          :aria-label="t('review.select', { chore: item.instance.choreName })"
          @update:checked="emit('toggle')"
        >
          <span class="font-medium">{{ item.instance.choreName }}</span>
        </NCheckbox>
        <div
          class="flex flex-wrap items-center justify-end gap-2 text-sm text-gray-600 dark:text-neutral-400"
        >
          <NTag v-if="item.instance.rejectionCount" size="small" type="warning" :bordered="false">
            {{ t('review.rejectedBefore', { n: item.instance.rejectionCount }) }}
          </NTag>
          <span v-if="item.submission">{{
            t('review.submittedAt', { at: formatDateTime(item.submission.submittedAt) })
          }}</span>
        </div>
      </div>

      <p
        v-if="item.instance.lastRejection?.text || item.instance.lastRejection?.reason"
        class="text-sm"
      >
        <span class="font-medium">{{ t('review.lastRejection') }}</span>
        {{
          [
            item.instance.lastRejection?.reason
              ? t(`review.reasons.${item.instance.lastRejection.reason}`)
              : '',
            item.instance.lastRejection?.text ?? '',
          ]
            .filter(Boolean)
            .join(': ')
        }}
      </p>

      <ul
        v-if="item.steps.length"
        class="flex flex-col gap-1 text-sm"
        :aria-label="t('review.checklist')"
      >
        <li v-for="(s, i) in item.steps" :key="i" class="flex items-center gap-2">
          <span aria-hidden="true">{{ s.done ? '☑' : '☐' }}</span>
          <span :class="{ 'text-gray-500 line-through': !s.done }">{{ s.text }}</span>
          <span class="sr-only">{{ s.done ? t('review.stepDone') : t('review.stepNotDone') }}</span>
        </li>
      </ul>

      <p v-if="item.submission?.note" class="whitespace-pre-wrap text-sm">
        <span class="font-medium">{{ t('review.note') }}</span> {{ item.submission.note }}
      </p>

      <div v-if="photos.length" class="grid grid-cols-3 gap-2 sm:grid-cols-5">
        <template v-for="(p, i) in photos" :key="p.mediaId">
          <div
            v-if="p.expired || !p.thumbUrl"
            class="flex aspect-square items-center justify-center rounded bg-gray-100 p-1 text-center text-xs text-gray-600 dark:bg-neutral-800 dark:text-neutral-400"
            data-testid="photo-expired"
          >
            {{ t('review.mediaExpired') }}
          </div>
          <button
            v-else
            type="button"
            class="aspect-square overflow-hidden rounded focus-visible:outline-2 focus-visible:outline-offset-2"
            :aria-label="t('review.openPhoto', { n: i + 1, total: photos.length })"
            @click="openIndex = i"
          >
            <img
              :src="p.thumbUrl"
              :alt="t('review.photoAlt', { chore: item.instance.choreName, n: i + 1 })"
              class="h-full w-full object-cover"
              loading="lazy"
              @error="emit('photoError')"
            >
          </button>
        </template>
      </div>
      <p v-else-if="item.submission" class="text-sm text-gray-600 dark:text-neutral-400">
        {{ t('review.noPhotos') }}
      </p>

      <div class="flex justify-end gap-2">
        <NButton size="small" :disabled="busy" @click="emit('reject')">{{
          t('review.reject')
        }}</NButton>
        <NButton size="small" type="primary" :disabled="busy" @click="emit('approve')">{{
          t('review.approve')
        }}</NButton>
      </div>
    </div>

    <NModal
      v-model:show="open"
      preset="card"
      :title="
        t('review.photoTitle', {
          chore: item.instance.choreName,
          n: (openIndex ?? 0) + 1,
          total: photos.length,
        })
      "
      class="max-w-3xl"
      :on-after-leave="() => (openIndex = undefined)"
      @keydown="onKey"
    >
      <div class="flex flex-col items-center gap-3">
        <img
          v-if="current?.url"
          :src="current.url"
          :alt="t('review.photoAlt', { chore: item.instance.choreName, n: (openIndex ?? 0) + 1 })"
          class="max-h-[70vh] max-w-full object-contain"
          @error="emit('photoError')"
        >
        <p v-else>{{ t('review.mediaExpired') }}</p>
        <div v-if="photos.length > 1" class="flex gap-2">
          <NButton size="small" @click="step(-1)">{{ t('review.prevPhoto') }}</NButton>
          <NButton size="small" @click="step(1)">{{ t('review.nextPhoto') }}</NButton>
        </div>
      </div>
    </NModal>
  </NCard>
</template>
