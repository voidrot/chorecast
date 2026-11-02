import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import { badgeText } from '@/review/queue'

/** The pending count behind the nav badge: the first page's size, plus "+" when more pages exist. */
export const useReviewQueueStore = defineStore('reviewQueue', () => {
  const count = ref(0)
  const hasMore = ref(false)
  const badge = computed(() => badgeText(count.value, hasMore.value))

  async function refresh() {
    const { data } = await api.GET('/review-queue', { params: { query: { limit: 100 } } })
    // A failed refresh keeps the last count; the badge is only a hint.
    if (!data) return
    count.value = data.items.length
    hasMore.value = data.nextCursor !== undefined
  }

  return { count, hasMore, badge, refresh }
})
