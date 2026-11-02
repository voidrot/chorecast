<script setup lang="ts">
import { NAlert, NButton, NDatePicker, NSelect, NSpin, useMessage } from 'naive-ui'
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import {
  type ActorFilter,
  AUDIT_ACTIONS,
  type AuditAction,
  type AuditEntry,
  actorLabel,
  auditQuery,
  describe,
  detailLines,
  mergeEntries,
} from '@/audit/log'
import { formatDateTime } from '@/format'
import { useSessionStore } from '@/stores/session'

const { t, te } = useI18n()
const message = useMessage()
const session = useSessionStore()

const entries = ref<AuditEntry[]>([])
const nextCursor = ref<string>()
const loading = ref(false)
const loadError = ref<string>()
let seq = 0

const actorFilter = ref<ActorFilter>(null)
const actionFilter = ref<AuditAction | null>(null)
const range = ref<[string, string] | null>(null)
const parentOptions = ref<{ value: number; label: string }[]>([])
const actorOptions = computed(() => [
  ...parentOptions.value,
  { value: 'system', label: t('audit.actor.system') },
  { value: 'cli', label: t('audit.actor.cli') },
])
const actionOptions = computed(() =>
  AUDIT_ACTIONS.map((a) => ({ value: a, label: t(`audit.actionNames.${a}`) })),
)

function errorText(e: ApiError): string {
  return e.detail || e.code
}

async function fetchPage(cursor?: string) {
  const filters = { actor: actorFilter.value, action: actionFilter.value, range: range.value }
  const { data, error, response } = await api.GET('/audit', {
    params: { query: auditQuery(filters, cursor) },
  })
  if (!data) throw ApiError.fromProblem(response.status, error)
  return data
}

async function load() {
  // Only the newest load may write state: a slower, older response is dropped.
  const mine = ++seq
  loading.value = true
  loadError.value = undefined
  try {
    const page = await fetchPage()
    if (mine !== seq) return
    entries.value = page.items
    nextCursor.value = page.nextCursor
  } catch (e) {
    if (mine !== seq) return
    loadError.value = errorText(e instanceof ApiError ? e : ApiError.network(e))
    entries.value = []
    nextCursor.value = undefined
  } finally {
    if (mine === seq) loading.value = false
  }
}

async function loadMore() {
  if (!nextCursor.value) return
  const mine = seq
  loading.value = true
  try {
    const page = await fetchPage(nextCursor.value)
    if (mine !== seq) return
    entries.value = mergeEntries(entries.value, page.items)
    nextCursor.value = page.nextCursor
  } catch (e) {
    if (mine === seq) message.error(errorText(e instanceof ApiError ? e : ApiError.network(e)))
  } finally {
    if (mine === seq) loading.value = false
  }
}

onMounted(async () => {
  if (!session.isOwner) return
  // The parent filter's options are a convenience: if they fail to load, the log still works.
  try {
    const { data } = await api.GET('/parents')
    parentOptions.value = (data?.items ?? []).map((p) => ({ value: p.id, label: p.displayName }))
  } catch {
    // leave the options empty
  }
  await load()
})

watch([actorFilter, actionFilter, range], () => void load())
</script>

<template>
  <div class="flex flex-col gap-6">
    <div>
      <h1 class="text-2xl font-semibold">{{ t('audit.title') }}</h1>
      <p class="text-gray-600 dark:text-neutral-400">{{ t('audit.intro') }}</p>
    </div>

    <NAlert v-if="!session.isOwner" type="info">{{ t('audit.ownersOnly') }}</NAlert>
    <template v-else>
      <div class="grid gap-3 sm:grid-cols-3">
        <div class="flex flex-col gap-1">
          <span class="text-sm font-medium" aria-hidden="true">{{ t('audit.filterActor') }}</span>
          <NSelect
            v-model:value="actorFilter"
            :options="actorOptions"
            clearable
            :aria-label="t('audit.filterActor')"
          />
        </div>
        <div class="flex flex-col gap-1">
          <label for="af-action" class="text-sm font-medium">{{ t('audit.filterAction') }}</label>
          <NSelect
            v-model:value="actionFilter"
            :options="actionOptions"
            clearable
            filterable
            :input-props="{ id: 'af-action' }"
          />
        </div>
        <fieldset>
          <legend class="mb-1 text-sm font-medium">{{ t('audit.filterDates') }}</legend>
          <NDatePicker
            v-model:formatted-value="range"
            type="daterange"
            value-format="yyyy-MM-dd"
            clearable
          />
        </fieldset>
      </div>

      <NAlert v-if="loadError" type="error">{{ loadError }}</NAlert>
      <NSpin v-if="loading && !entries.length" />
      <p v-else-if="!entries.length && !loadError" class="text-gray-600 dark:text-neutral-400">
        {{ t('audit.empty') }}
      </p>
      <template v-else-if="entries.length">
        <ul class="flex flex-col divide-y divide-gray-200 dark:divide-neutral-800">
          <li
            v-for="e in entries"
            :key="e.id"
            class="flex flex-col gap-1 py-3 sm:flex-row sm:gap-4"
          >
            <time
              :datetime="e.createdAt"
              class="shrink-0 text-sm text-gray-600 sm:w-44 dark:text-neutral-400"
            >
              {{ formatDateTime(e.createdAt) }}
            </time>
            <div class="flex flex-col gap-0.5">
              <p><span class="font-medium">{{ actorLabel(e, t) }}</span> · {{ describe(e, t) }}</p>
              <p
                v-for="(line, i) in detailLines(e, t, te)"
                :key="i"
                class="text-sm text-gray-600 dark:text-neutral-400"
              >
                {{ line }}
              </p>
            </div>
          </li>
        </ul>
        <div v-if="nextCursor" class="flex justify-center">
          <NButton :loading="loading" @click="loadMore">{{ t('audit.loadMore') }}</NButton>
        </div>
      </template>
    </template>
  </div>
</template>
