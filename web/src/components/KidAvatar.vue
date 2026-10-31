<script setup lang="ts">
import { computed } from 'vue'
import { initials } from '@/kids/form'

const props = withDefaults(
  defineProps<{ name: string; color: string; avatar?: string; size?: number }>(),
  { avatar: '', size: 48 },
)
const style = computed(() => ({
  width: `${props.size}px`,
  height: `${props.size}px`,
  boxShadow: `0 0 0 3px ${props.color}`,
}))
</script>

<template>
  <img
    v-if="avatar"
    :src="`/avatars/${avatar}.svg`"
    :alt="name"
    class="shrink-0 rounded-full"
    :style="style"
  >
  <span
    v-else
    class="grid shrink-0 place-items-center rounded-full font-semibold text-white"
    :style="{ ...style, background: color, fontSize: `${size / 2.6}px` }"
    :aria-label="name"
    role="img"
    >{{
      initials(name)
    }}</span
  >
</template>
