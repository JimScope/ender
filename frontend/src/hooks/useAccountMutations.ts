import { useMutation, useQueryClient } from "@tanstack/react-query"

import pb from "@/lib/pocketbase"
import useCustomToast from "./useCustomToast"

interface ProfileUpdate {
  full_name?: string
  email?: string
}

interface PasswordChange {
  current_password: string
  new_password: string
  confirm_password: string
}

export function useUpdateProfile() {
  const queryClient = useQueryClient()
  const { showSuccessToast, showErrorToast } = useCustomToast()

  return useMutation({
    mutationFn: async (data: ProfileUpdate) => {
      const userId = pb.authStore.record?.id
      if (!userId) throw new Error("Not authenticated")

      const { email, ...fields } = data
      if (Object.keys(fields).length > 0) {
        await pb.collection("users").update(userId, fields)
      }
      // An auth record can't change its own email through a direct update —
      // PocketBase requires the requestEmailChange confirmation flow.
      if (email && email !== pb.authStore.record?.email) {
        await pb.collection("users").requestEmailChange(email)
        return { emailChangeRequested: true }
      }
      return { emailChangeRequested: false }
    },
    onSuccess: ({ emailChangeRequested }) => {
      if (emailChangeRequested) {
        showSuccessToast("Confirmation link sent to the new email address")
      } else {
        showSuccessToast("User updated successfully")
      }
    },
    onError: (error: Error) => {
      showErrorToast(error.message || "Failed to update profile")
    },
    onSettled: () => {
      queryClient.invalidateQueries()
    },
  })
}

export function useChangePassword() {
  const { showSuccessToast, showErrorToast } = useCustomToast()

  return useMutation({
    mutationFn: async (data: PasswordChange) => {
      const userId = pb.authStore.record?.id
      if (!userId) throw new Error("Not authenticated")
      return await pb.collection("users").update(userId, {
        oldPassword: data.current_password,
        password: data.new_password,
        passwordConfirm: data.confirm_password,
      })
    },
    onSuccess: () => {
      showSuccessToast("Password updated successfully")
    },
    onError: (error: Error) => {
      showErrorToast(error.message || "Failed to change password")
    },
  })
}

export function useDeleteAccount() {
  const queryClient = useQueryClient()
  const { showSuccessToast, showErrorToast } = useCustomToast()

  return useMutation({
    mutationFn: async () => {
      const userId = pb.authStore.record?.id
      if (!userId) throw new Error("Not authenticated")
      return await pb.collection("users").delete(userId)
    },
    onSuccess: () => {
      showSuccessToast("Your account has been successfully deleted")
    },
    onError: (error: Error) => {
      showErrorToast(error.message || "Failed to delete account")
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ["currentUser"] })
    },
  })
}

export function useExportData() {
  const { showErrorToast } = useCustomToast()

  return useMutation({
    mutationFn: async () => {
      return await pb.send("/api/user/export", { method: "GET" })
    },
    onError: () => {
      showErrorToast("Failed to export data")
    },
  })
}
