import { useMutation, useQueryClient } from "@tanstack/react-query"

import pb from "@/lib/pocketbase"
import useCustomToast from "./useCustomToast"

interface DeviceCreate {
  device_type: "android" | "modem" | "smpp"
  name: string
  phone_number: string
  smpp_config?: {
    host: string
    port: number
    system_id: string
    password: string
    system_type?: string
    bind_mode?: "tx" | "rx" | "trx"
    source_ton?: number
    source_npi?: number
    dest_ton?: number
    dest_npi?: number
    use_tls?: boolean
    enquire_link_seconds?: number
    default_data_coding?: number
    submit_throttle_tps?: number
  }
}

interface DeviceUpdate {
  name?: string
  phone_number?: string
}

export function useCreateDevice() {
  const queryClient = useQueryClient()
  const { showSuccessToast, showErrorToast } = useCustomToast()

  return useMutation({
    mutationFn: async (data: DeviceCreate) => {
      const { smpp_config, ...deviceData } = data
      const device = await pb.collection("sms_devices").create({
        ...deviceData,
        user: pb.authStore.record?.id,
      })
      if (deviceData.device_type === "smpp" && smpp_config) {
        await pb.collection("smpp_configs").create({
          device: device.id,
          ...smpp_config,
        })
      }
      return device
    },
    onSuccess: () => {
      showSuccessToast("Device created successfully")
    },
    onError: (error: Error) => {
      showErrorToast(error.message || "Failed to create device")
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ["devices"] })
      queryClient.invalidateQueries({ queryKey: ["quota"] })
    },
  })
}

export function useUpdateDevice(deviceId: string) {
  const queryClient = useQueryClient()
  const { showSuccessToast, showErrorToast } = useCustomToast()

  return useMutation({
    mutationFn: async (data: DeviceUpdate) => {
      return await pb.collection("sms_devices").update(deviceId, data)
    },
    onSuccess: () => {
      showSuccessToast("Device updated successfully")
    },
    onError: (error: Error) => {
      showErrorToast(error.message || "Failed to update device")
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ["devices"] })
    },
  })
}

export function useDeleteDevice() {
  const queryClient = useQueryClient()
  const { showSuccessToast, showErrorToast } = useCustomToast()

  return useMutation({
    mutationFn: async (deviceId: string) => {
      return await pb.collection("sms_devices").delete(deviceId)
    },
    onSuccess: () => {
      showSuccessToast("The device was deleted successfully")
    },
    onError: (error: Error) => {
      showErrorToast(error.message || "Failed to delete device")
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ["devices"] })
      queryClient.invalidateQueries({ queryKey: ["quota"] })
    },
  })
}
