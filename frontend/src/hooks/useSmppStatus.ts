import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useEffect, useRef } from "react"
import pb from "@/lib/pocketbase"

export function useSmppStatus() {
  const queryClient = useQueryClient()
  const unsubRef = useRef<(() => Promise<void>) | null>(null)

  useEffect(() => {
    if (!pb.authStore.isValid) return

    pb.realtime
      .subscribe("smpp-status", (data) => {
        queryClient.setQueryData(["smpp-status"], data)
      })
      .then((unsub) => {
        unsubRef.current = unsub
      })

    return () => {
      unsubRef.current?.()
    }
  }, [queryClient])

  return useQuery<Record<string, boolean>>({
    queryKey: ["smpp-status"],
    queryFn: () => ({}),
    staleTime: Infinity,
  })
}
