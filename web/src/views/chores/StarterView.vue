<script setup lang="ts">
import { NAlert, NButton, NCheckbox, NSpin, NSwitch, NTag, useMessage } from 'naive-ui'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import {
  buildApplyBody,
  groupState,
  groups,
  initialSelection,
  refsToCreate,
  toggleGroup,
} from '@/chores/starter'
import { useScheduleText } from '@/chores/useScheduleText'
import { useAsync } from '@/composables/useAsync'

const { t } = useI18n()
const router = useRouter()
const message = useMessage()
const scheduleText = useScheduleText()

const pack = useAsync(async () => {
  const { data, error, response } = await api.GET('/chores/starter-pack')
  if (!data) throw ApiError.fromProblem(response.status, error)
  return data
})

const sel = ref(new Set<string>())
const paused = ref(false)
const submitting = ref(false)
const submitError = ref<ApiError>()

onMounted(async () => {
  const loaded = await pack.run()
  if (loaded) sel.value = initialSelection(loaded)
})

const sections = computed(() => (pack.data.value ? groups(pack.data.value) : []))
const selectable = computed(() => pack.data.value?.chores.some((c) => !c.exists) ?? false)
const count = computed(() =>
  pack.data.value ? (buildApplyBody(pack.data.value, sel.value, false).chores?.length ?? 0) : 0,
)
const creates = computed(() => {
  if (!pack.data.value) return []
  const refs = refsToCreate(pack.data.value, sel.value)
  return [...refs.locations, ...refs.equipment].map((r) => r.name)
})

function toggleOne(key: string, on: boolean) {
  const next = new Set(sel.value)
  if (on) next.add(key)
  else next.delete(key)
  sel.value = next
}

async function submit() {
  if (!pack.data.value || submitting.value) return
  submitting.value = true
  submitError.value = undefined
  try {
    const { data, error, response } = await api.POST('/chores/starter-pack/apply', {
      body: buildApplyBody(pack.data.value, sel.value, paused.value),
    })
    if (!data) {
      submitError.value = ApiError.fromProblem(response.status, error)
      return
    }
    message.success(t('starter.added', data.created.chores.length))
    await router.push({ name: 'chores' })
  } catch (e) {
    submitError.value = ApiError.network(e)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-semibold">{{ t('starter.title') }}</h1>
      <RouterLink :to="{ name: 'chores' }" class="text-sm underline">{{
        t('chores.backToChores')
      }}</RouterLink>
    </div>
    <NAlert v-if="pack.error.value" type="error">{{ pack.error.value.detail }}</NAlert>
    <NSpin v-else-if="!pack.data.value" />
    <p v-else-if="!selectable" class="text-gray-600 dark:text-neutral-400">
      {{ t('starter.none') }}
    </p>
    <template v-else>
      <p class="text-gray-600 dark:text-neutral-400">{{ t('starter.intro') }}</p>

      <section
        v-for="g in sections"
        :key="g.key"
        class="flex flex-col gap-2 rounded-lg border border-gray-200 p-4 dark:border-neutral-700"
      >
        <div class="flex items-center justify-between gap-3">
          <NCheckbox
            :checked="groupState(g.chores, sel) === 'all'"
            :indeterminate="groupState(g.chores, sel) === 'some'"
            :disabled="g.chores.every((c) => c.exists)"
            @update:checked="sel = toggleGroup(g.chores, sel)"
          >
            <span class="font-medium">{{ g.name }}</span>
          </NCheckbox>
          <span class="text-sm text-gray-500 dark:text-neutral-400">
            {{
              t('starter.selectedCount', {
                n: g.chores.filter((c) => sel.has(c.key) && !c.exists).length,
                m: g.chores.filter((c) => !c.exists).length,
              })
            }}
          </span>
        </div>
        <div v-for="c in g.chores" :key="c.key" class="flex flex-wrap items-start gap-x-2 pl-6">
          <NCheckbox
            :checked="sel.has(c.key) && !c.exists"
            :disabled="c.exists"
            @update:checked="toggleOne(c.key, $event)"
          >
            <span class="font-medium">{{ c.name }}</span>
          </NCheckbox>
          <NTag v-if="c.exists" size="small" :bordered="false">{{ t('starter.exists') }}</NTag>
          <span class="text-sm text-gray-500 dark:text-neutral-400">
            {{ t('chores.pts', { n: c.points }) }}
            <template v-if="c.estimatedMinutes">
              · {{ t('starter.minutes', { n: c.estimatedMinutes }) }}
            </template>
            · {{ scheduleText(c.schedule) }}
            <template v-if="c.minAge"> · {{ t('starter.ages', { n: c.minAge }) }}</template>
          </span>
        </div>
      </section>

      <p v-if="creates.length" class="text-sm text-gray-500 dark:text-neutral-400">
        {{ t('starter.creates', { names: creates.join(', ') }) }}
      </p>
      <div class="flex items-center gap-2">
        <NSwitch v-model:value="paused" aria-labelledby="starter-paused-label" />
        <span id="starter-paused-label">{{ t('starter.paused') }}</span>
      </div>
      <NAlert v-if="submitError" type="error">{{
        submitError.detail || t('starter.submitFailed')
      }}</NAlert>
      <div>
        <NButton type="primary" :disabled="count === 0" :loading="submitting" @click="submit">{{
          t('starter.add', { n: count }, count)
        }}</NButton>
      </div>
    </template>
  </div>
</template>
