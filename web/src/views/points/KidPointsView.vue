<script setup lang="ts">
import { NAlert, NButton, NCard, NSpin, NTag, useMessage } from 'naive-ui'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import type { components } from '@/api/schema'
import { formatDate, formatDateTime } from '@/format'
import {
  type AdjustmentBody,
  canReverse,
  isClamped,
  mergePage,
  type PointsEntry,
} from '@/points/ledger'
import AdjustCard from './AdjustCard.vue'
import ReverseModal from './ReverseModal.vue'

const { t, te } = useI18n()
const route = useRoute()
const message = useMessage()
const kidKey = String(route.params.kid)

const kidName = ref('')
const balance = ref<{ balance: number; available: number }>()
const streaks = ref<components['schemas']['KidStreaks']>()
const choreNames = ref(new Map<number, string>())
const entries = ref<PointsEntry[]>([])
const nextCursor = ref<string>()
let pagesLoaded = 1
const loading = ref(true)
const loadingMore = ref(false)
const loadError = ref<string>()

const adjustBusy = ref(false)
const adjustError = ref<string>()
const reversing = ref<PointsEntry>()
const reverseBusy = ref(false)
const reverseError = ref<string>()

const pointsFormat = new Intl.NumberFormat(undefined, { signDisplay: 'exceptZero' })
const signed = (n: number) => pointsFormat.format(n)

/** Maps an API error to text: a translation for known codes, else the server's detail. */
function errorText(e: ApiError): string {
  return te(`points.errors.${e.code}`) ? t(`points.errors.${e.code}`) : e.detail || e.code
}

function choreName(id: number) {
  return choreNames.value.get(id) ?? t('points.streaks.unknownChore', { id })
}

const path = computed(() => ({ kid: kidKey }))

async function fetchPage(cursor?: string) {
  const { data, error, response } = await api.GET('/kids/{kid}/points/entries', {
    params: { path: path.value, query: { cursor } },
  })
  if (!data) throw ApiError.fromProblem(response.status, error)
  return data
}

async function fetchBalance() {
  const { data, error, response } = await api.GET('/kids/{kid}/points', {
    params: { path: path.value },
  })
  if (!data) throw ApiError.fromProblem(response.status, error)
  balance.value = data
}

/** Reloads the ledger and balance, keeping as many pages as were listed so the place isn't lost. */
async function reload(keepPages = 1) {
  let list: PointsEntry[] = []
  let cursor: string | undefined
  let pages = 0
  for (let i = 0; i < keepPages; i++) {
    const page = await fetchPage(cursor)
    list = mergePage(list, page.items)
    cursor = page.nextCursor
    pages++
    if (!cursor) break
  }
  pagesLoaded = pages
  entries.value = list
  nextCursor.value = cursor
  await fetchBalance()
}

async function loadAll() {
  loading.value = true
  loadError.value = undefined
  try {
    const [kid, streak, chores] = await Promise.all([
      api.GET('/kids/{kid}', { params: { path: path.value } }),
      api.GET('/kids/{kid}/streaks', { params: { path: path.value } }),
      api.GET('/chores'),
      reload(),
    ])
    if (!kid.data) throw ApiError.fromProblem(kid.response.status, kid.error)
    if (!streak.data) throw ApiError.fromProblem(streak.response.status, streak.error)
    kidName.value = kid.data.displayName
    streaks.value = streak.data
    choreNames.value = new Map((chores.data?.items ?? []).map((c) => [c.id, c.name]))
  } catch (e) {
    loadError.value = errorText(e instanceof ApiError ? e : ApiError.network(e))
  } finally {
    loading.value = false
  }
}

async function loadMore() {
  if (!nextCursor.value) return
  loadingMore.value = true
  try {
    const page = await fetchPage(nextCursor.value)
    entries.value = mergePage(entries.value, page.items)
    nextCursor.value = page.nextCursor
    pagesLoaded++
  } catch (e) {
    message.error(errorText(e instanceof ApiError ? e : ApiError.network(e)))
  } finally {
    loadingMore.value = false
  }
}

/** Re-reads after a write whose outcome the screen can't trust (a conflict or lost response). */
async function refreshQuietly() {
  try {
    await reload(pagesLoaded)
  } catch {
    message.warning(t('points.refreshFailed'))
  }
}

async function adjust(body: AdjustmentBody, key: string, done: () => void) {
  adjustBusy.value = true
  adjustError.value = undefined
  try {
    const { error, response } = await api.POST('/kids/{kid}/points/adjustments', {
      params: { path: path.value, header: { 'Idempotency-Key': key } },
      body,
    })
    if (!response.ok) throw ApiError.fromProblem(response.status, error)
    done()
    message.success(t('points.adjust.done'))
    await refreshQuietly()
  } catch (e) {
    adjustError.value = errorText(e instanceof ApiError ? e : ApiError.network(e))
  } finally {
    adjustBusy.value = false
  }
}

async function reverse(note: string) {
  const target = reversing.value
  if (!target) return
  reverseBusy.value = true
  reverseError.value = undefined
  try {
    const { error, response } = await api.POST('/kids/{kid}/points/entries/{entry}/reversal', {
      params: { path: { ...path.value, entry: target.id } },
      body: { note },
    })
    if (!response.ok) {
      const err = ApiError.fromProblem(response.status, error)
      // Someone else reversed it first: show the current state instead of a stale button.
      if (err.code === 'already_reversed') {
        reversing.value = undefined
        message.warning(errorText(err))
        await refreshQuietly()
        return
      }
      throw err
    }
    reversing.value = undefined
    message.success(t('points.reverse.done'))
    await refreshQuietly()
  } catch (e) {
    reverseError.value = errorText(e instanceof ApiError ? e : ApiError.network(e))
  } finally {
    reverseBusy.value = false
  }
}

function openReverse(e: PointsEntry) {
  reverseError.value = undefined
  reversing.value = e
}

onMounted(loadAll)
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-2xl font-semibold">{{ t('points.title', { name: kidName }) }}</h1>
      <RouterLink :to="{ name: 'kid-edit', params: { kid: kidKey } }">
        <NButton>{{ t('points.profile') }}</NButton>
      </RouterLink>
    </div>

    <NAlert v-if="loadError" type="error">{{ loadError }}</NAlert>
    <NSpin v-else-if="loading" />
    <template v-else>
      <div class="grid gap-4 sm:grid-cols-2">
        <NCard>
          <div class="text-sm text-gray-500 dark:text-neutral-400">{{ t('points.balance') }}</div>
          <div class="text-3xl font-semibold" data-testid="balance">{{ balance?.balance }}</div>
        </NCard>
        <NCard>
          <div class="text-sm text-gray-500 dark:text-neutral-400">{{ t('points.available') }}</div>
          <div class="text-3xl font-semibold" data-testid="available">{{ balance?.available }}</div>
          <div class="text-sm text-gray-500 dark:text-neutral-400">
            {{ t('points.availableHint') }}
          </div>
        </NCard>
      </div>

      <NCard :title="t('points.streaks.title')">
        <div class="flex flex-col gap-3">
          <div>
            <div class="font-medium">{{ t('points.streaks.daily') }}</div>
            <div data-testid="daily-streak">
              {{ t('points.streaks.current', { n: streaks?.daily.current }) }} ·
              {{ t('points.streaks.best', { n: streaks?.daily.best }) }}
              <template v-if="streaks?.daily.lastDay">
                · {{ t('points.streaks.asOf', { date: formatDate(streaks.daily.lastDay) }) }}
              </template>
            </div>
          </div>
          <p v-if="!streaks?.chores?.length" class="text-gray-600 dark:text-neutral-400">
            {{ t('points.streaks.noChores') }}
          </p>
          <ul v-else class="flex flex-col gap-1">
            <li v-for="c in streaks?.chores" :key="c.choreId" class="flex flex-wrap gap-x-3">
              <span class="font-medium">{{ choreName(c.choreId) }}</span>
              <span>{{ t('points.streaks.current', { n: c.current }) }}</span>
              <span>{{ t('points.streaks.best', { n: c.best }) }}</span>
            </li>
          </ul>
        </div>
      </NCard>

      <AdjustCard :busy="adjustBusy" :error="adjustError" @submit="adjust" />

      <NCard :title="t('points.ledger.title')">
        <p v-if="!entries.length" class="text-gray-600 dark:text-neutral-400">
          {{ t('points.ledger.empty') }}
        </p>
        <ul v-else class="flex flex-col divide-y divide-gray-200 dark:divide-neutral-700">
          <li
            v-for="e in entries"
            :key="e.id"
            class="flex flex-wrap items-start justify-between gap-3 py-3"
            data-testid="ledger-entry"
          >
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-medium">{{ t(`points.kinds.${e.kind}`) }}</span>
                <NTag v-if="e.reversedById" size="small" type="warning" :bordered="false">{{
                  t('points.ledger.reversed')
                }}</NTag>
                <NTag v-if="e.reversesId" size="small" :bordered="false">{{
                  t('points.ledger.reversesEntry', { id: e.reversesId })
                }}</NTag>
                <NTag v-if="isClamped(e)" size="small" type="info" :bordered="false">{{
                  t('points.ledger.clamped', { requested: signed(e.requestedAmount ?? 0) })
                }}</NTag>
              </div>
              <div v-if="e.note" class="break-words">{{ e.note }}</div>
              <div class="text-sm text-gray-500 dark:text-neutral-400">
                {{ formatDate(e.localDate) }} · {{ formatDateTime(e.createdAt) }}
              </div>
            </div>
            <div class="text-right">
              <div class="text-lg font-semibold" :class="e.amount < 0 ? 'text-red-600' : ''">
                {{ signed(e.amount) }}
              </div>
              <div class="text-sm text-gray-500 dark:text-neutral-400">
                {{ t('points.ledger.balanceAfter', { n: e.balanceAfter }) }}
              </div>
              <NButton v-if="canReverse(e)" size="small" class="mt-1" @click="openReverse(e)">{{
                e.kind === 'reversal' ? t('points.ledger.undoReversal') : t('points.ledger.reverse')
              }}</NButton>
            </div>
          </li>
        </ul>
        <div v-if="nextCursor" class="mt-3">
          <NButton :loading="loadingMore" @click="loadMore">{{ t('points.ledger.more') }}</NButton>
        </div>
      </NCard>
    </template>

    <ReverseModal
      :show="reversing !== undefined"
      :busy="reverseBusy"
      :error="reverseError"
      @close="reversing = undefined"
      @submit="reverse"
    />
  </div>
</template>
