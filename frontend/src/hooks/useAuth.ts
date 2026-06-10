import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"

import pb from "@/lib/pocketbase"
import useCustomToast from "./useCustomToast"

const isLoggedIn = () => {
  return pb.authStore.isValid
}

interface LoginData {
  username: string
  password: string
}

interface SignUpData {
  email: string
  password: string
  passwordConfirm: string
  full_name?: string
}

const useAuth = () => {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { showErrorToast } = useCustomToast()

  const { data: user } = useQuery({
    queryKey: ["currentUser"],
    queryFn: async () => {
      try {
        const result = await pb.collection("users").authRefresh()
        return result.record
      } catch (err: any) {
        // Only clear auth on 401 (invalid token), not on network errors
        if (err?.status === 401) {
          pb.authStore.clear()
        }
        throw err
      }
    },
    enabled: isLoggedIn(),
    // useAuth mounts in many components — without staleTime each navigation
    // fires a redundant authRefresh request.
    staleTime: 5 * 60 * 1000,
    retry: (failureCount, error: any) => {
      // Don't retry auth errors (401), only transient/network failures
      if (error?.status === 401) return false
      return failureCount < 2
    },
    retryDelay: (attempt) => Math.min(1000 * 2 ** attempt, 5000),
  })

  const signUpMutation = useMutation({
    mutationFn: async (data: SignUpData) => {
      const record = await pb.collection("users").create({
        email: data.email,
        password: data.password,
        passwordConfirm: data.passwordConfirm,
        full_name: data.full_name || "",
      })
      await pb.collection("users").requestVerification(data.email)
      return { ...record, email: data.email }
    },
    onSuccess: (data) => {
      navigate({ to: "/check-email", search: { email: data?.email || "" } })
    },
    onError: (error: Error) => {
      showErrorToast(error.message || "Registration failed")
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] })
    },
  })

  const login = async (data: LoginData) => {
    await pb.collection("users").authWithPassword(data.username, data.password)
  }

  const loginMutation = useMutation({
    mutationFn: login,
    onSuccess: () => {
      navigate({ to: "/" })
    },
    onError: (error: Error) => {
      showErrorToast(error.message || "Login failed")
    },
  })

  const logout = () => {
    pb.authStore.clear()
    queryClient.clear()
    navigate({ to: "/login" })
  }

  return {
    signUpMutation,
    loginMutation,
    logout,
    user,
  }
}

export { isLoggedIn }
export default useAuth
