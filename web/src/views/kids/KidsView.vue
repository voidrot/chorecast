<script setup lang="ts">
import { NAlert, NButton, NCard, NSpin, NSwitch, NTag } from 'naive-ui'
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import KidAvatar from '@/components/KidAvatar.vue'
import { useAsync } from '@/composables/useAsync'
import { ageOn } from '@/kids/form'
import { todayISO } from '@/setup/wizard'

const { t } = useI18n()
const showArchived = ref(false)
const today = todayISO()

const kids = useAsync(async () => {
  const { data, error, response } = await api.GET('/kids', {
    params: { query: { archived: showArchived.value } },
  })
  if (!data) throw ApiError.fromProblem(response.status, error)
  return data.items
})
onMounted(() => kids.run())
watch(showArchived, () => kids.run())
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-2xl font-semibold">{{ t('kids.title') }}</h1>
      <div class="flex items-center gap-4">
        <div class="flex items-center gap-2 text-sm">
          <NSwitch v-model:value="showArchived" aria-labelledby="lbl-showArchived" />
          <span id="lbl-showArchived">{{ t('kids.showArchived') }}</span>
        </div>
        <RouterLink :to="{ name: 'kid-new' }">
          <NButton type="primary">{{ t('kids.add') }}</NButton>
        </RouterLink>
      </div>
    </div>
    <NAlert v-if="kids.error.value" type="error">{{ kids.error.value.detail }}</NAlert>
    <NSpin v-else-if="kids.loading.value && !kids.data.value" />
    <p v-else-if="!kids.data.value?.length" class="text-gray-600 dark:text-neutral-400">
      {{ t('kids.empty') }}
    </p>
    <div v-else class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      <RouterLink
        v-for="k in kids.data.value"
        :key="k.id"
        :to="{ name: 'kid-edit', params: { kid: k.slug } }"
        data-testid="kid-card"
      >
        <NCard hoverable :class="{ 'opacity-60': k.archivedAt }">
          <div class="flex items-center gap-4">
            <KidAvatar :name="k.displayName" :color="k.color" :avatar="k.avatar" :size="56" />
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2 text-lg font-medium">
                {{ k.displayName }}
                <NTag v-if="k.archivedAt" size="small" :bordered="false">{{
                  t('kids.archived')
                }}</NTag>
              </div>
              <div class="text-sm text-gray-500 dark:text-neutral-400">
                @{{ k.slug }}
                <template v-if="ageOn(k.birthDate ?? '', today) !== undefined">
                  · {{ t('kids.age', { age: ageOn(k.birthDate ?? '', today) }) }}
                </template>
              </div>
            </div>
          </div>
        </NCard>
      </RouterLink>
    </div>
  </div>
</template>
