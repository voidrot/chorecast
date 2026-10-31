<script setup lang="ts">
import { darkTheme, NConfigProvider, NDialogProvider, NMessageProvider, useOsTheme } from 'naive-ui'
import { computed } from 'vue'
import { RouterView } from 'vue-router'
import { naiveLocale, themeOverrides } from './theme'

const os = useOsTheme()
const theme = computed(() => (os.value === 'dark' ? darkTheme : null))
</script>

<template>
  <NConfigProvider :theme="theme" :theme-overrides="themeOverrides" :locale="naiveLocale">
    <NMessageProvider>
      <NDialogProvider>
        <RouterView />
      </NDialogProvider>
    </NMessageProvider>
  </NConfigProvider>
</template>
