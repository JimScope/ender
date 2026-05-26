import { useMutation, useQueryClient } from "@tanstack/react-query"

import pb from "@/lib/pocketbase"
import useCustomToast from "./useCustomToast"

interface DeviceCreate {
  device_type: "android" | "modem"
  name: string
  phone_number: string
}
// Note: "aws_aeum" devices are created by the backend (system-global), not via this hook.

interface DeviceUpdate {
  name?: string
  phone_number?: string
}

export function useCreateDevice() {
  const queryClient = useQueryClient()
  const { showSuccessToast, showErrorToast } = useCustomToast()

  return useMutation({
    mutationFn: async (data: DeviceCreate) => {
      return await pb.collection("sms_devices").create({
        ...data,
        user: pb.authStore.record?.id,
      })
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
