<script setup lang="ts">
import { NAlert, NSpin, useMessage } from 'naive-ui'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import KidAvatar from '@/components/KidAvatar.vue'
import { earliestExpiry, useLinkRefresh } from '@/composables/useLinkRefresh'
import { householdTimeZone } from '@/format'
import type { RejectBody } from '@/review/queue'
import { useReviewQueueStore } from '@/stores/reviewQueue'
import {
  type Action,
  BUCKETS,
  completers,
  householdToday,
  type Instance,
  isStale,
  planByKid,
  rangeFor,
  reassignTargets,
} from '@/today/plan'
import RejectModal from '@/views/review/RejectModal.vue'
import KidPickModal from './KidPickModal.vue'
import TodayCard from './TodayCard.vue'

const { t, te } = useI18n()
const message = useMessage()
const reviewQueue = useReviewQueueStore()

type KidRow = {
  id: number
  displayName: string
  color: string
  avatar?: string
  avatarPhotoUrl?: string
  avatarPhotoExpiresAt?: string
}

const kids = ref<KidRow[]>([])
const instances = ref<Instance[]>([])
const today = ref('')
const loading = ref(false)
const loadError = ref<string>()
const busyIds = ref<Set<number>>(new Set())
let seq = 0

const plans = computed(() => planByKid(instances.value, kids.value, today.value))
const kidById = computed(() => new Map(kids.value.map((k) => [k.id, k])))

function errorText(e: ApiError): string {
  if (te(`today.errors.${e.code}`)) return t(`today.errors.${e.code}`)
  // A refused completion (missing photos, say) explains itself in the field errors.
  const reasons = e.fieldErrors?.map((f) => f.message).filter(Boolean) ?? []
  if (e.code === 'validation_failed' && reasons.length) return reasons.join(' ')
  return e.detail || e.code
}

function asApiError(e: unknown): ApiError {
  return e instanceof ApiError ? e : ApiError.network(e)
}

async function load() {
  // Only the newest load may write state: a slower, older response is dropped.
  const mine = ++seq
  loading.value = true
  loadError.value = undefined
  try {
    // Today is the household's day, and the layout may not have loaded its zone yet.
    const household = await api.GET('/household')
    if (!household.data) throw ApiError.fromProblem(household.response.status, household.error)
    householdTimeZone.value = household.data.timezone
    const day = householdToday(household.data.timezone, household.data.dayEnd)
    const { from, to } = rangeFor(day)
    const [k, i] = await Promise.all([
      api.GET('/kids'),
      api.GET('/instances', { params: { query: { from, to } } }),
    ])
    if (!k.data) throw ApiError.fromProblem(k.response.status, k.error)
    if (!i.data) throw ApiError.fromProblem(i.response.status, i.error)
    if (mine !== seq) return
    today.value = day
    kids.value = k.data.items
    instances.value = i.data.items
  } catch (e) {
    if (mine === seq) loadError.value = errorText(asApiError(e))
  } finally {
    if (mine === seq) loading.value = false
  }
}

useLinkRefresh(
  computed(() => earliestExpiry(kids.value.map((k) => k.avatarPhotoExpiresAt))),
  () => {
    if (!loading.value) void load()
  },
)

function setBusy(id: number, on: boolean) {
  const next = new Set(busyIds.value)
  if (on) next.add(id)
  else next.delete(id)
  busyIds.value = next
}

interface Outcome {
  ok: boolean
  error?: string
}

/** Runs one action. A stale-state failure (409/404) reloads the list so the card catches up. */
async function run(
  inst: Instance,
  call: () => Promise<{ response: Response; error?: Parameters<typeof ApiError.fromProblem>[1] }>,
  done: string,
): Promise<Outcome> {
  setBusy(inst.id, true)
  try {
    const { response, error } = await call()
    if (response.ok) {
      message.success(t(done, { chore: inst.choreName }))
      void load()
      void reviewQueue.refresh()
      return { ok: true }
    }
    const err = ApiError.fromProblem(response.status, error)
    if (isStale(err.code)) void load()
    return { ok: false, error: errorText(err) }
  } catch (e) {
    return { ok: false, error: errorText(asApiError(e)) }
  } finally {
    setBusy(inst.id, false)
  }
}

const path = (inst: Instance) => ({ params: { path: { instance: inst.id } } })

async function simple(inst: Instance, action: 'approve' | 'reverse' | 'excuse' | 'complete') {
  const call = {
    complete: () =>
      api.POST('/instances/{instance}/completion', { ...path(inst), body: { kidId: inst.kidId } }),
    approve: () => api.POST('/instances/{instance}/approval', path(inst)),
    reverse: () => api.POST('/instances/{instance}/approval/reversal', path(inst)),
    excuse: () => api.POST('/instances/{instance}/excusal', path(inst)),
  }[action]
  const out = await run(inst, call, `today.done.${action}`)
  if (!out.ok && out.error) message.error(out.error)
}

const rejecting = ref<Instance>()
const reassigning = ref<Instance>()
const completing = ref<Instance>()
const modalBusy = ref(false)
const modalError = ref<string>()

function onAct(inst: Instance, action: Action) {
  if (action === 'reject') {
    modalError.value = undefined
    rejecting.value = inst
  } else if (action === 'complete' && inst.kidId === undefined) {
    // A shared chore needs the member who did it.
    modalError.value = undefined
    completing.value = inst
  } else if (action === 'reassign') {
    modalError.value = undefined
    reassigning.value = inst
  } else {
    void simple(inst, action)
  }
}

async function submitModal(
  inst: Instance | undefined,
  call: () => Promise<{ response: Response; error?: Parameters<typeof ApiError.fromProblem>[1] }>,
  done: string,
  close: () => void,
) {
  if (!inst) return
  modalBusy.value = true
  modalError.value = undefined
  const out = await run(inst, call, done)
  modalBusy.value = false
  if (out.ok) close()
  else modalError.value = out.error
}

function submitReject(body: RejectBody) {
  const inst = rejecting.value
  if (!inst) return
  void submitModal(
    inst,
    () => api.POST('/instances/{instance}/rejection', { ...path(inst), body }),
    'today.done.reject',
    () => {
      rejecting.value = undefined
    },
  )
}

function submitReassign(kidId: number) {
  const inst = reassigning.value
  if (!inst) return
  void submitModal(
    inst,
    () => api.POST('/instances/{instance}/reassignment', { ...path(inst), body: { kidId } }),
    'today.done.reassign',
    () => {
      reassigning.value = undefined
    },
  )
}

function submitComplete(kidId: number) {
  const inst = completing.value
  if (!inst) return
  void submitModal(
    inst,
    () => api.POST('/instances/{instance}/completion', { ...path(inst), body: { kidId } }),
    'today.done.complete',
    () => {
      completing.value = undefined
    },
  )
}

const completeOptions = computed(() =>
  completing.value
    ? completers(kids.value, completing.value).map((k) => ({ value: k.id, label: k.displayName }))
    : [],
)

const targetOptions = computed(() =>
  reassigning.value
    ? reassignTargets(kids.value, reassigning.value).map((k) => ({
        value: k.id,
        label: k.displayName,
      }))
    : [],
)

onMounted(load)
</script>

<template>
  <div class="flex flex-col gap-6">
    <h1 class="text-2xl font-semibold">{{ t('today.title') }}</h1>

    <NAlert v-if="loadError" type="error">{{ loadError }}</NAlert>
    <NSpin v-if="loading && !instances.length" />
    <p v-else-if="!plans.length && !loadError" class="text-gray-600 dark:text-neutral-400">
      {{ t('today.empty') }}
    </p>

    <section
      v-for="p in plans"
      :key="p.key"
      :aria-labelledby="`tk-${p.key}`"
      class="flex flex-col gap-4"
    >
      <h2 :id="`tk-${p.key}`" class="flex items-center gap-3 text-xl font-semibold">
        <template v-if="p.kidId !== undefined && kidById.get(p.kidId)">
          <KidAvatar
            :name="kidById.get(p.kidId)?.displayName ?? ''"
            :color="kidById.get(p.kidId)?.color ?? ''"
            :avatar="kidById.get(p.kidId)?.avatar"
            :photo-url="kidById.get(p.kidId)?.avatarPhotoUrl"
            :size="36"
          />
          {{ kidById.get(p.kidId)?.displayName }}
        </template>
        <template v-else-if="p.kidId !== undefined">{{ t('today.unknownKid') }}</template>
        <template v-else>{{ t('today.shared') }}</template>
      </h2>
      <template v-for="b in BUCKETS" :key="b">
        <div v-if="p.buckets[b].length" class="flex flex-col gap-2">
          <h3
            class="text-sm font-semibold uppercase tracking-wide text-gray-600 dark:text-neutral-400"
          >
            {{ t(`today.buckets.${b}`) }}
          </h3>
          <TodayCard
            v-for="i in p.buckets[b]"
            :key="i.id"
            :instance="i"
            :busy="busyIds.has(i.id)"
            @act="(a) => onAct(i, a)"
          />
        </div>
      </template>
    </section>

    <RejectModal
      :show="rejecting !== undefined"
      :chore="rejecting?.choreName ?? ''"
      :busy="modalBusy"
      :error="modalError"
      @close="rejecting = undefined"
      @submit="submitReject"
    />
    <KidPickModal
      :show="reassigning !== undefined"
      :title="t('today.reassignTitle', { chore: reassigning?.choreName ?? '' })"
      :label="t('today.reassignTo')"
      :submit-label="t('today.actions.reassign')"
      :kids="targetOptions"
      :busy="modalBusy"
      :error="modalError"
      @close="reassigning = undefined"
      @submit="submitReassign"
    />
    <KidPickModal
      :show="completing !== undefined"
      :title="t('today.completeTitle', { chore: completing?.choreName ?? '' })"
      :label="t('today.completeFor')"
      :submit-label="t('today.actions.complete')"
      :kids="completeOptions"
      :busy="modalBusy"
      :error="modalError"
      @close="completing = undefined"
      @submit="submitComplete"
    />
  </div>
</template>
