<script setup lang="ts">
import { darkTheme, NConfigProvider, NMessageProvider, useOsTheme } from 'naive-ui'
import { computed } from 'vue'
import { RouterView } from 'vue-router'
import { themeOverrides } from './theme'

const os = useOsTheme()
const theme = computed(() => (os.value === 'dark' ? darkTheme : null))
</script>

<template>
  <NConfigProvider :theme="theme" :theme-overrides="themeOverrides">
    <NMessageProvider>
      <RouterView />
    </NMessageProvider>
  </NConfigProvider>
</template>
