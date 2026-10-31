<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCard,
  NCheckbox,
  NCheckboxGroup,
  NDatePicker,
  NFormItem,
  NInput,
  NSpin,
  NSwitch,
  NTimePicker,
  useDialog,
  useMessage,
} from 'naive-ui'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import { avatarIds } from '@/avatars'
import KidAvatar from '@/components/KidAvatar.vue'
import {
  emptyForm,
  formFromKid,
  inputFromForm,
  type Kid,
  type KidForm,
  weekdayNames,
} from '@/kids/form'
import { kidColors } from '@/setup/wizard'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const message = useMessage()
const dialog = useDialog()

const isNew = computed(() => route.name === 'kid-new')
const kid = ref<Kid>()
const form = ref<KidForm>(emptyForm(avatarIds[Math.floor(Math.random() * avatarIds.length)]))
const loading = ref(!isNew.value)
const saving = ref(false)
const error = ref<ApiError>()
const pin = ref('')
const pinValid = computed(() => /^\d{4,8}$/.test(pin.value))

const isFuture = (ts: number) => ts > Date.now()

onMounted(async () => {
  if (isNew.value) return
  const {
    data,
    error: e,
    response,
  } = await api.GET('/kids/{kid}', {
    params: { path: { kid: String(route.params.kid) } },
  })
  loading.value = false
  if (!data) {
    error.value = ApiError.fromProblem(response.status, e)
    return
  }
  kid.value = data
  form.value = formFromKid(data)
})

async function save() {
  saving.value = true
  error.value = undefined
  try {
    const body = inputFromForm(form.value)
    const res = isNew.value
      ? await api.POST('/kids', { body })
      : await api.PUT('/kids/{kid}', { params: { path: { kid: String(kid.value?.id) } }, body })
    if (!res.data) {
      error.value = ApiError.fromProblem(res.response.status, res.error)
      return
    }
    message.success(t('kids.saved', { name: res.data.displayName }))
    await router.push({ name: 'kids' })
  } catch (e) {
    error.value = ApiError.network(e)
  } finally {
    saving.value = false
  }
}

async function setPin(clear: boolean) {
  if (!kid.value) return
  const { error: e, response } = await api.PUT('/kids/{kid}/pin', {
    params: { path: { kid: String(kid.value.id) } },
    body: { pin: clear ? '' : pin.value },
  })
  if (e) {
    error.value = ApiError.fromProblem(response.status, e)
    return
  }
  pin.value = ''
  kid.value = { ...kid.value, pinSet: !clear }
  message.success(clear ? t('kids.pinCleared') : t('kids.pinSaved'))
}

function toggleArchive() {
  const k = kid.value
  if (!k) return
  const archiving = !k.archivedAt
  const run = async () => {
    const path = { params: { path: { kid: String(k.id) } } }
    const res = archiving
      ? await api.POST('/kids/{kid}/archive', path)
      : await api.POST('/kids/{kid}/unarchive', path)
    if (!res.data) {
      error.value = ApiError.fromProblem(res.response.status, res.error)
      return
    }
    kid.value = res.data
    message.success(
      archiving
        ? t('kids.archivedMsg', { name: k.displayName })
        : t('kids.unarchivedMsg', { name: k.displayName }),
    )
  }
  if (!archiving) return void run()
  dialog.warning({
    title: t('kids.archive'),
    content: t('kids.archiveConfirm', { name: k.displayName }),
    positiveText: t('kids.archive'),
    negativeText: t('account.cancel'),
    onPositiveClick: run,
  })
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex items-center gap-4">
      <KidAvatar
        :name="form.displayName || '?'"
        :color="form.color"
        :avatar="form.avatar"
        :size="56"
      />
      <h1 class="text-2xl font-semibold">
        {{ isNew ? t('kids.newTitle') : kid?.displayName }}
      </h1>
    </div>
    <NSpin v-if="loading" />
    <form v-else class="flex flex-col gap-6" @submit.prevent="save">
      <NAlert v-if="error" type="error" :show-icon="false">
        {{ error.detail || error.code }}
        <ul v-if="error.fieldErrors?.length" class="mt-1 list-disc pl-5">
          <li v-for="(fe, i) in error.fieldErrors" :key="i">{{ fe.location }}: {{ fe.message }}</li>
        </ul>
      </NAlert>

      <NCard :title="t('kids.profile')">
        <div class="grid gap-4 sm:grid-cols-2">
          <NFormItem
            :label="t('kids.name')"
            :label-props="{ for: 'kid-name' }"
            :show-feedback="false"
          >
            <NInput
              v-model:value="form.displayName"
              :maxlength="40"
              :input-props="{ id: 'kid-name' }"
            />
          </NFormItem>
          <NFormItem
            v-if="!isNew"
            :label="t('kids.slug')"
            :label-props="{ for: 'kid-slug' }"
            :show-feedback="false"
          >
            <NInput
              v-model:value="form.slug"
              :maxlength="32"
              :input-props="{ id: 'kid-slug', autocapitalize: 'none' }"
            />
          </NFormItem>
          <NFormItem :label="t('kids.birthDate')" :show-feedback="false">
            <NDatePicker
              :formatted-value="form.birthDate || null"
              type="date"
              value-format="yyyy-MM-dd"
              clearable
              class="w-full"
              :is-date-disabled="isFuture"
              @update:formatted-value="form.birthDate = $event ?? ''"
            />
          </NFormItem>
        </div>
        <p v-if="!isNew" class="hint mt-1">{{ t('kids.slugHint') }}</p>

        <fieldset class="mt-4">
          <legend class="mb-2 font-medium">{{ t('setup.kids.color') }}</legend>
          <div class="flex flex-wrap gap-1.5">
            <label
              v-for="c in kidColors"
              :key="c"
              :for="`color-${c.slice(1)}`"
              class="size-7 cursor-pointer rounded-full outline-offset-2 focus-within:outline-2"
              :class="form.color === c ? 'outline-2 outline-gray-900 dark:outline-white' : ''"
              :style="{ background: c }"
            >
              <input
                :id="`color-${c.slice(1)}`"
                v-model="form.color"
                class="sr-only"
                type="radio"
                name="color"
                :value="c"
              >
              <span class="sr-only">{{ c }}</span>
            </label>
          </div>
        </fieldset>

        <fieldset class="mt-4">
          <legend class="mb-2 font-medium">{{ t('kids.avatar') }}</legend>
          <div class="flex flex-wrap gap-2" data-testid="avatar-picker">
            <label
              v-for="a in avatarIds"
              :key="a"
              :for="`avatar-${a}`"
              class="cursor-pointer rounded-full outline-offset-2 focus-within:outline-2"
              :class="form.avatar === a ? 'outline-3 outline-emerald-500' : ''"
            >
              <input
                :id="`avatar-${a}`"
                v-model="form.avatar"
                class="sr-only"
                type="radio"
                name="avatar"
                :value="a"
              >
              <img :src="`/avatars/${a}.svg`" alt="" class="size-12 rounded-full">
              <span class="sr-only">{{ t('kids.avatarN', { n: Number(a.slice(-2)) }) }}</span>
            </label>
            <label
              for="avatar-none"
              class="grid size-12 cursor-pointer place-items-center rounded-full border border-dashed border-gray-400 text-xs outline-offset-2 focus-within:outline-2"
              :class="form.avatar === '' ? 'outline-3 outline-emerald-500' : ''"
            >
              <input
                id="avatar-none"
                v-model="form.avatar"
                class="sr-only"
                type="radio"
                name="avatar"
                value=""
              >
              {{ t('kids.noAvatar') }}
            </label>
          </div>
        </fieldset>
      </NCard>

      <NCard :title="t('kids.pinTitle')">
        <div class="flex flex-col gap-3">
          <div class="flex items-center gap-2">
            <NSwitch v-model:value="form.pinRequired" aria-labelledby="lbl-pinRequired" />
            <span id="lbl-pinRequired">{{ t('setup.kids.pinRequired') }}</span>
          </div>
          <template v-if="!isNew">
            <p class="hint">{{ kid?.pinSet ? t('kids.pinIsSet') : t('kids.pinNotSet') }}</p>
            <div class="flex flex-wrap items-end gap-2">
              <NFormItem
                :label="t('account.pin.pin')"
                :label-props="{ for: 'kid-pin' }"
                :show-feedback="false"
              >
                <NInput
                  v-model:value="pin"
                  type="password"
                  :maxlength="8"
                  :input-props="{ id: 'kid-pin', inputmode: 'numeric', autocomplete: 'off' }"
                />
              </NFormItem>
              <NButton :disabled="!pinValid" @click="setPin(false)">{{
                t('kids.savePin')
              }}</NButton>
              <NButton v-if="kid?.pinSet" secondary type="error" @click="setPin(true)">{{
                t('kids.clearPin')
              }}</NButton>
            </div>
          </template>
          <p v-else class="hint">{{ t('kids.pinAfterCreate') }}</p>
        </div>
      </NCard>

      <NCard :title="t('kids.schedule')">
        <div class="flex flex-col gap-5">
          <div class="flex flex-col gap-2">
            <div class="flex items-center gap-2">
              <NSwitch v-model:value="form.dayEndOn" aria-labelledby="lbl-dayEndOn" />
              <span id="lbl-dayEndOn">{{ t('kids.dayEnd') }}</span>
            </div>
            <p class="hint">{{ t('kids.dayEndHint') }}</p>
            <NTimePicker
              v-if="form.dayEndOn"
              :formatted-value="form.dayEnd"
              value-format="HH:mm"
              format="HH:mm"
              :clearable="false"
              class="max-w-40"
              @update:formatted-value="form.dayEnd = $event ?? form.dayEnd"
            />
          </div>
          <div class="flex flex-col gap-2">
            <div class="flex items-center gap-2">
              <NSwitch v-model:value="form.quietOn" aria-labelledby="lbl-quietOn" />
              <span id="lbl-quietOn">{{ t('kids.quietHours') }}</span>
            </div>
            <p class="hint">{{ t('kids.quietHint') }}</p>
            <div v-if="form.quietOn" class="flex items-center gap-2">
              <NTimePicker
                :formatted-value="form.quietStart"
                value-format="HH:mm"
                format="HH:mm"
                :clearable="false"
                class="max-w-32"
                @update:formatted-value="form.quietStart = $event ?? form.quietStart"
              />
              <span>–</span>
              <NTimePicker
                :formatted-value="form.quietEnd"
                value-format="HH:mm"
                format="HH:mm"
                :clearable="false"
                class="max-w-32"
                @update:formatted-value="form.quietEnd = $event ?? form.quietEnd"
              />
            </div>
          </div>
          <div class="flex flex-col gap-2">
            <div class="flex items-center gap-2">
              <NSwitch v-model:value="form.schoolOn" aria-labelledby="lbl-schoolOn" />
              <span id="lbl-schoolOn">{{ t('kids.schoolHours') }}</span>
            </div>
            <p class="hint">{{ t('kids.schoolHint') }}</p>
            <template v-if="form.schoolOn">
              <NCheckboxGroup v-model:value="form.schoolDays">
                <div class="flex flex-wrap gap-3">
                  <NCheckbox
                    v-for="d in weekdayNames"
                    :key="d"
                    :value="d"
                    :label="t(`setup.household.weekdays.${d}`)"
                  />
                </div>
              </NCheckboxGroup>
              <div class="flex items-center gap-2">
                <NTimePicker
                  :formatted-value="form.schoolStart"
                  value-format="HH:mm"
                  format="HH:mm"
                  :clearable="false"
                  class="max-w-32"
                  @update:formatted-value="form.schoolStart = $event ?? form.schoolStart"
                />
                <span>–</span>
                <NTimePicker
                  :formatted-value="form.schoolEnd"
                  value-format="HH:mm"
                  format="HH:mm"
                  :clearable="false"
                  class="max-w-32"
                  @update:formatted-value="form.schoolEnd = $event ?? form.schoolEnd"
                />
              </div>
            </template>
          </div>
        </div>
      </NCard>

      <div class="flex flex-wrap justify-between gap-2">
        <NButton
          v-if="!isNew && kid"
          secondary
          :type="kid.archivedAt ? 'default' : 'warning'"
          @click="toggleArchive"
        >
          {{ kid.archivedAt ? t('kids.unarchive') : t('kids.archive') }}
        </NButton>
        <span v-else />
        <div class="flex gap-2">
          <RouterLink :to="{ name: 'kids' }"
            ><NButton quaternary>{{ t('account.cancel') }}</NButton></RouterLink
          >
          <NButton type="primary" attr-type="submit" :loading="saving">
            {{ isNew ? t('kids.create') : t('kids.save') }}
          </NButton>
        </div>
      </div>
    </form>
  </div>
</template>

<style scoped>
@reference "../../style.css";
.hint {
  @apply text-sm text-gray-500 dark:text-neutral-400;
}
</style>
