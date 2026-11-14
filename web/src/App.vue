<script setup lang="ts">
import { darkTheme, NConfigProvider, NDialogProvider, NMessageProvider, useOsTheme } from 'naive-ui'
import { computed } from 'vue'
import { RouterView } from 'vue-router'
import { darkOverrides, lightOverrides, naiveLocale } from './theme'

const os = useOsTheme()
const theme = computed(() => (os.value === 'dark' ? darkTheme : null))
const overrides = computed(() => (os.value === 'dark' ? darkOverrides : lightOverrides))
</script>

<template>
  <NConfigProvider :theme="theme" :theme-overrides="overrides" :locale="naiveLocale">
    <NMessageProvider>
      <NDialogProvider>
        <RouterView />
      </NDialogProvider>
    </NMessageProvider>
  </NConfigProvider>
</template>
