<script setup lang="ts">
import { NAlert, NButton, NCard, NInput } from 'naive-ui'
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import type { components } from '@/api/schema'

type Item = components['schemas']['CatalogItem']
const props = defineProps<{ kind: 'locations' | 'equipment' }>()

const { t } = useI18n()
const items = ref<Item[]>([])
const name = ref('')
const description = ref('')
const editing = ref<number>()
const error = ref<ApiError>()
const path = `/${props.kind}` as const

async function load() {
  const { data } = await api.GET(path)
  items.value = data?.items ?? []
}
onMounted(load)

async function run(p: Promise<{ error?: unknown; response: Response }>) {
  const { error: e, response } = await p
  if (e) {
    error.value = ApiError.fromProblem(response.status, e as never)
    return false
  }
  error.value = undefined
  await load()
  return true
}

async function submit() {
  if (!name.value.trim()) return
  const body = { name: name.value, description: description.value }
  const ok = editing.value
    ? await run(api.PUT(`${path}/{id}`, { params: { path: { id: editing.value } }, body }))
    : await run(api.POST(path, { body }))
  if (ok) {
    name.value = description.value = ''
    editing.value = undefined
  }
}

function edit(item: Item) {
  editing.value = item.id
  name.value = item.name
  description.value = item.description ?? ''
}
</script>

<template>
  <NCard :title="t(`chores.${kind}`)">
    <div class="flex flex-col gap-3">
      <NAlert v-if="error" type="error" :show-icon="false">{{ error.detail }}</NAlert>
      <ul class="divide-y divide-gray-200 dark:divide-neutral-800">
        <li
          v-for="item in items"
          :key="item.id"
          class="flex items-center justify-between gap-2 py-2"
        >
          <div class="min-w-0">
            <div class="font-medium">{{ item.name }}</div>
            <div v-if="item.description" class="text-sm text-gray-500 dark:text-neutral-400">
              {{ item.description }}
            </div>
          </div>
          <div class="flex gap-1">
            <NButton size="tiny" quaternary @click="edit(item)">{{ t('chores.edit') }}</NButton>
            <NButton
              size="tiny"
              quaternary
              type="error"
              @click="run(api.DELETE(`${path}/{id}`, { params: { path: { id: item.id } } }))"
              >{{
                t('chores.remove')
              }}</NButton
            >
          </div>
        </li>
      </ul>
      <form class="flex flex-col gap-2" @submit.prevent="submit">
        <NInput
          v-model:value="name"
          :maxlength="60"
          :placeholder="t('chores.catalogName')"
          :input-props="{ 'aria-label': t('chores.catalogName') }"
        />
        <NInput
          v-model:value="description"
          :maxlength="500"
          :placeholder="t('chores.catalogDescription')"
          :input-props="{ 'aria-label': t('chores.catalogDescription') }"
        />
        <div class="flex justify-end gap-2">
          <NButton
            v-if="editing"
            quaternary
            @click="
              editing = undefined;
              name = '';
              description = '';
            "
            >{{
              t('account.cancel')
            }}</NButton
          >
          <NButton type="primary" attr-type="submit" :disabled="!name.trim()">{{
            editing ? t('kids.save') : t('chores.addItem')
          }}</NButton>
        </div>
      </form>
    </div>
  </NCard>
</template>
