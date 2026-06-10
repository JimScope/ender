import { useQueryClient } from "@tanstack/react-query"
import { useEffect, useRef } from "react"
import pb from "@/lib/pocketbase"

export function useRealtimeQuery(collection: string, queryKeys: string[][]) {
  const queryClient = useQueryClient()
  const queryKeysRef = useRef(queryKeys)
  queryKeysRef.current = queryKeys

  useEffect(() => {
    if (!pb.authStore.isValid) return

    let disposed = false
    let unsubFn: (() => Promise<void>) | null = null

    pb.collection(collection)
      .subscribe("*", () => {
        for (const key of queryKeysRef.current) {
          queryClient.invalidateQueries({ queryKey: key })
        }
      })
      .then((fn) => {
        // The component may unmount before subscribe() resolves (guaranteed
        // in dev with StrictMode) — drop the subscription right away instead
        // of leaking it.
        if (disposed) {
          fn().catch(() => {})
        } else {
          unsubFn = fn
        }
      })
      .catch(() => {
        // Subscription failed (e.g. auth expired mid-flight) — nothing to
        // clean up; queries still work via regular fetching.
      })

    return () => {
      disposed = true
      unsubFn?.().catch(() => {})
    }
  }, [collection, queryClient])
}
