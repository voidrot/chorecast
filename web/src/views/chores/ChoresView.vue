<script setup lang="ts">
import { NAlert, NButton, NCard, NSpin, NTag } from 'naive-ui'
import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import { useAsync } from '@/composables/useAsync'

const { t } = useI18n()
const chores = useAsync(async () => {
  const { data, error, response } = await api.GET('/chores')
  if (!data) throw ApiError.fromProblem(response.status, error)
  return data.items
})
onMounted(() => chores.run())
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-2xl font-semibold">{{ t('chores.title') }}</h1>
      <div class="flex gap-2">
        <RouterLink :to="{ name: 'chore-catalog' }"
          ><NButton secondary>{{ t('chores.catalog') }}</NButton></RouterLink
        >
        <RouterLink :to="{ name: 'chore-new' }"
          ><NButton type="primary">{{ t('chores.add') }}</NButton></RouterLink
        >
      </div>
    </div>
    <NAlert v-if="chores.error.value" type="error">{{ chores.error.value.detail }}</NAlert>
    <NSpin v-else-if="chores.loading.value && !chores.data.value" />
    <p v-else-if="!chores.data.value?.length" class="text-gray-600 dark:text-neutral-400">
      {{ t('chores.empty') }}
    </p>
    <div v-else class="grid gap-3 sm:grid-cols-2">
      <RouterLink
        v-for="c in chores.data.value"
        :key="c.id"
        :to="{ name: 'chore-edit', params: { id: c.id } }"
        data-testid="chore-card"
      >
        <NCard hoverable size="small" :class="{ 'opacity-60': c.disabled }">
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <div class="font-medium">{{ c.name }}</div>
              <div class="text-sm text-gray-500 dark:text-neutral-400">
                {{
                  c.slots?.length
                    ? t('chores.dueAt', { times: c.slots.join(', ') })
                    : t('chores.dueAnytime')
                }}
              </div>
            </div>
            <div class="text-right">
              <div class="font-semibold text-emerald-600 dark:text-emerald-400">
                {{ t('chores.pts', { n: c.points }) }}
              </div>
              <div class="mt-1 flex flex-wrap justify-end gap-1">
                <NTag v-if="c.requiresApproval" size="small" :bordered="false">{{
                  t('chores.approval')
                }}</NTag>
                <NTag v-if="c.evidence.photos" size="small" :bordered="false">
                  {{ t('chores.photosTag', { n: c.evidence.photos }) }}
                </NTag>
                <NTag v-if="c.disabled" size="small" type="warning" :bordered="false">{{
                  t('chores.disabled')
                }}</NTag>
              </div>
            </div>
          </div>
        </NCard>
      </RouterLink>
    </div>
  </div>
</template>
