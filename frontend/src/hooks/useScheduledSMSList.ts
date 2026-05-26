import { queryOptions, useQuery, useSuspenseQuery } from "@tanstack/react-query"
import pb from "@/lib/pocketbase"
import { useRealtimeQuery } from "./useRealtimeQuery"

export const scheduledSMSListQueryOptions = queryOptions({
  queryKey: ["scheduled-sms"],
  queryFn: async () => {
    const result = await pb.collection("scheduled_sms").getList(1, 100, {
      sort: "-created",
      expand: "device_id",
    })
    return { data: result.items, count: result.totalItems }
  },
  staleTime: 60_000,
})

export function useScheduledSMSList() {
  useRealtimeQuery("scheduled_sms", [["scheduled-sms"]])
  return useQuery(scheduledSMSListQueryOptions)
}

export function useScheduledSMSListSuspense() {
  useRealtimeQuery("scheduled_sms", [["scheduled-sms"]])
  return useSuspenseQuery(scheduledSMSListQueryOptions)
}
