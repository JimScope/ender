import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useEffect } from "react"
import pb from "@/lib/pocketbase"

export function useModemStatus() {
  const queryClient = useQueryClient()

  useEffect(() => {
    if (!pb.authStore.isValid) return

    let disposed = false
    let unsubFn: (() => Promise<void>) | null = null

    pb.realtime
      .subscribe("modem-status", (data) => {
        queryClient.setQueryData(["modem-status"], data)
      })
      .then((unsub) => {
        // The component may unmount before subscribe() resolves (guaranteed
        // in dev with StrictMode) — drop the subscription right away instead
        // of leaking it.
        if (disposed) {
          unsub().catch(() => {})
        } else {
          unsubFn = unsub
        }
      })
      .catch(() => {
        // Subscription failed (e.g. auth expired mid-flight) — nothing to
        // clean up; the query just keeps its last known data.
      })

    return () => {
      disposed = true
      unsubFn?.().catch(() => {})
    }
  }, [queryClient])

  return useQuery<Record<string, boolean>>({
    queryKey: ["modem-status"],
    queryFn: () => ({}),
    staleTime: Infinity,
  })
}
