<script setup lang="ts">
import { NAlert, NButton, NCheckbox, NSelect, NSpin, useMessage } from 'naive-ui'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import {
  groupByKid,
  isGone,
  mergePage,
  PHOTO_REFRESH_MS,
  pruneSelection,
  type RejectBody,
  type ReviewItem,
  removableIds,
  removeItems,
  selectionState,
  summarizeApprovals,
  toggleAll,
  toggleSelected,
} from '@/review/queue'
import { useReviewQueueStore } from '@/stores/reviewQueue'
import RejectModal from './RejectModal.vue'
import ReviewItemCard from './ReviewItemCard.vue'

const { t, te } = useI18n()
const message = useMessage()
const badge = useReviewQueueStore()

const items = ref<ReviewItem[]>([])
const nextCursor = ref<string>()
const loading = ref(false)
const loadError = ref<string>()
const selected = ref<Set<number>>(new Set())
const busyIds = ref<Set<number>>(new Set())
const bulkBusy = ref(false)
const failures = ref<{ instanceId: number; chore: string; text: string }[]>([])
let loadedAt = Date.now()
let lastPhotoReload = 0

const kidFilter = ref<number | null>(null)
const choreFilter = ref<number | null>(null)
const mediaFilter = ref<'with' | 'without' | null>(null)
const kidOptions = ref<{ value: number; label: string }[]>([])
const choreOptions = ref<{ value: number; label: string }[]>([])
const mediaOptions = computed(() => [
  { value: 'with', label: t('review.media.with') },
  { value: 'without', label: t('review.media.without') },
])

const groups = computed(() => groupByKid(items.value))
const state = computed(() => selectionState(selected.value, items.value))

/** Maps an API error to text: a translation for known codes, else the server's detail. */
function errorText(e: ApiError): string {
  return te(`review.errors.${e.code}`) ? t(`review.errors.${e.code}`) : e.detail || e.code
}

async function fetchPage(cursor?: string) {
  const { data, error, response } = await api.GET('/review-queue', {
    params: {
      query: {
        cursor,
        kidId: kidFilter.value ?? undefined,
        choreId: choreFilter.value ?? undefined,
        media: mediaFilter.value ?? undefined,
      },
    },
  })
  if (!data) throw ApiError.fromProblem(response.status, error)
  return data
}

/**
 * Loads the first page, or, with keep, as many pages as are already listed so a reload (for fresh
 * photo links) doesn't lose the parent's place.
 */
async function load(keep = 0) {
  loading.value = true
  loadError.value = undefined
  try {
    let list: ReviewItem[] = []
    let cursor: string | undefined
    do {
      const page = await fetchPage(cursor)
      list = mergePage(list, page.items)
      cursor = page.nextCursor
    } while (cursor && list.length < keep)
    items.value = list
    nextCursor.value = cursor
    selected.value = pruneSelection(selected.value, list)
    loadedAt = Date.now()
  } catch (e) {
    loadError.value = errorText(e instanceof ApiError ? e : ApiError.network(e))
  } finally {
    loading.value = false
  }
}

async function loadMore() {
  if (!nextCursor.value) return
  loading.value = true
  try {
    const page = await fetchPage(nextCursor.value)
    items.value = mergePage(items.value, page.items)
    nextCursor.value = page.nextCursor
  } catch (e) {
    message.error(errorText(e instanceof ApiError ? e : ApiError.network(e)))
  } finally {
    loading.value = false
  }
}

function drop(ids: ReadonlySet<number>) {
  items.value = removeItems(items.value, ids)
  selected.value = pruneSelection(selected.value, items.value)
  void badge.refresh()
}

async function approveOne(item: ReviewItem) {
  const id = item.instance.id
  busyIds.value = new Set(busyIds.value).add(id)
  try {
    const { error, response } = await api.POST('/instances/{instance}/approval', {
      params: { path: { instance: id } },
    })
    if (response.ok) {
      message.success(t('review.approved', { chore: item.instance.choreName }))
      drop(new Set([id]))
      return
    }
    const err = ApiError.fromProblem(response.status, error)
    message.error(errorText(err))
    // 409: someone else already decided it, so it no longer belongs in the queue.
    if (err.status === 409 && isGone(err.code)) drop(new Set([id]))
  } catch (e) {
    message.error(errorText(ApiError.network(e)))
  } finally {
    const next = new Set(busyIds.value)
    next.delete(id)
    busyIds.value = next
  }
}

async function approveSelected() {
  const ids = [...selected.value]
  if (!ids.length) return
  bulkBusy.value = true
  failures.value = []
  try {
    const { data, error, response } = await api.POST('/review-queue/approvals', {
      body: { instanceIds: ids },
    })
    if (!data) throw ApiError.fromProblem(response.status, error)
    const summary = summarizeApprovals(data.results)
    const names = new Map(items.value.map((i) => [i.instance.id, i.instance.choreName]))
    failures.value = summary.failed.map((f) => ({
      instanceId: f.instanceId,
      chore: names.get(f.instanceId) ?? String(f.instanceId),
      text: te(`review.errors.${f.code}`) ? t(`review.errors.${f.code}`) : f.message || f.code,
    }))
    if (summary.approved.length) {
      message.success(t('review.bulkApproved', { n: summary.approved.length }))
    }
    if (summary.alreadyDecided.length) {
      message.warning(t('review.bulkAlreadyDecided', { n: summary.alreadyDecided.length }))
    }
    drop(removableIds(summary))
  } catch (e) {
    message.error(errorText(e instanceof ApiError ? e : ApiError.network(e)))
  } finally {
    bulkBusy.value = false
  }
}

const rejecting = ref<ReviewItem>()
const rejectBusy = ref(false)
const rejectError = ref<string>()

function startReject(item: ReviewItem) {
  rejectError.value = undefined
  rejecting.value = item
}

async function submitReject(body: RejectBody) {
  const item = rejecting.value
  if (!item) return
  rejectBusy.value = true
  rejectError.value = undefined
  try {
    const { error, response } = await api.POST('/instances/{instance}/rejection', {
      params: { path: { instance: item.instance.id } },
      body,
    })
    if (response.ok) {
      message.success(t('review.rejected', { chore: item.instance.choreName }))
      rejecting.value = undefined
      drop(new Set([item.instance.id]))
      return
    }
    const err = ApiError.fromProblem(response.status, error)
    rejectError.value = errorText(err)
    if (err.status === 409 && isGone(err.code)) {
      rejecting.value = undefined
      message.error(rejectError.value)
      drop(new Set([item.instance.id]))
    }
  } catch (e) {
    rejectError.value = errorText(ApiError.network(e))
  } finally {
    rejectBusy.value = false
  }
}

// Signed photo links last about 10 minutes: reload when one fails, at most every 30 seconds.
function onPhotoError() {
  if (Date.now() - lastPhotoReload < 30_000) return
  lastPhotoReload = Date.now()
  void load(items.value.length)
}

let timer: ReturnType<typeof setInterval> | undefined
onMounted(async () => {
  const [kids, chores] = await Promise.all([api.GET('/kids'), api.GET('/chores')])
  kidOptions.value = (kids.data?.items ?? []).map((k) => ({ value: k.id, label: k.displayName }))
  choreOptions.value = (chores.data?.items ?? []).map((c) => ({ value: c.id, label: c.name }))
  await load()
  timer = setInterval(() => {
    if (Date.now() - loadedAt > PHOTO_REFRESH_MS && !loading.value) void load(items.value.length)
  }, 60_000)
})
onBeforeUnmount(() => clearInterval(timer))

watch([kidFilter, choreFilter, mediaFilter], () => {
  selected.value = new Set()
  void load()
})
</script>

<template>
  <div class="flex flex-col gap-6">
    <h1 class="text-2xl font-semibold">{{ t('review.title') }}</h1>

    <div class="grid gap-3 sm:grid-cols-3">
      <div class="flex flex-col gap-1">
        <label for="rf-kid" class="text-sm font-medium">{{ t('review.filterKid') }}</label>
        <NSelect
          v-model:value="kidFilter"
          :options="kidOptions"
          clearable
          :input-props="{ id: 'rf-kid' }"
        />
      </div>
      <div class="flex flex-col gap-1">
        <label for="rf-chore" class="text-sm font-medium">{{ t('review.filterChore') }}</label>
        <NSelect
          v-model:value="choreFilter"
          :options="choreOptions"
          clearable
          filterable
          :input-props="{ id: 'rf-chore' }"
        />
      </div>
      <div class="flex flex-col gap-1">
        <label for="rf-media" class="text-sm font-medium">{{ t('review.filterMedia') }}</label>
        <NSelect
          v-model:value="mediaFilter"
          :options="mediaOptions"
          clearable
          :input-props="{ id: 'rf-media' }"
        />
      </div>
    </div>

    <NAlert v-if="loadError" type="error">{{ loadError }}</NAlert>
    <NSpin v-else-if="loading && !items.length" />
    <p v-else-if="!items.length" class="text-gray-600 dark:text-neutral-400">
      {{ t('review.empty') }}
    </p>
    <template v-else>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <NCheckbox
          :checked="state === 'all'"
          :indeterminate="state === 'some'"
          @update:checked="selected = toggleAll(selected, items)"
        >
          {{ t('review.selectAll') }}
        </NCheckbox>
        <NButton
          type="primary"
          :disabled="state === 'none'"
          :loading="bulkBusy"
          @click="approveSelected"
        >
          {{ t('review.approveSelected', { n: selected.size }) }}
        </NButton>
      </div>

      <NAlert v-if="failures.length" type="warning" closable @close="failures = []">
        <p class="font-medium">{{ t('review.bulkFailed') }}</p>
        <ul class="list-disc pl-5">
          <li v-for="f in failures" :key="f.instanceId">{{ f.chore }}: {{ f.text }}</li>
        </ul>
      </NAlert>

      <section
        v-for="g in groups"
        :key="g.key"
        :aria-labelledby="`rg-${g.key}`"
        class="flex flex-col gap-3"
      >
        <h2 :id="`rg-${g.key}`" class="text-lg font-semibold">
          {{ g.kidName }}
          <span class="text-sm font-normal text-gray-600 dark:text-neutral-400"
            >({{ g.items.length }})</span
          >
        </h2>
        <ReviewItemCard
          v-for="item in g.items"
          :key="item.instance.id"
          :item="item"
          :selected="selected.has(item.instance.id)"
          :busy="busyIds.has(item.instance.id)"
          @toggle="selected = toggleSelected(selected, item.instance.id)"
          @approve="approveOne(item)"
          @reject="startReject(item)"
          @photo-error="onPhotoError"
        />
      </section>

      <div v-if="nextCursor" class="flex justify-center">
        <NButton :loading="loading" @click="loadMore">{{ t('review.loadMore') }}</NButton>
      </div>
    </template>

    <RejectModal
      :show="rejecting !== undefined"
      :chore="rejecting?.instance.choreName ?? ''"
      :busy="rejectBusy"
      :error="rejectError"
      @close="rejecting = undefined"
      @submit="submitReject"
    />
  </div>
</template>
