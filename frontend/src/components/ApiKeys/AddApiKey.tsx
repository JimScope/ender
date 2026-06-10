import { zodResolver } from "@hookform/resolvers/zod"
import { Cuer } from "cuer"
import { Check, Copy, Plus } from "lucide-react"
import { useState } from "react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { z } from "zod"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form"
import { Input } from "@/components/ui/input"
import { LoadingButton } from "@/components/ui/loading-button"
import { useCreateApiKey } from "@/hooks/useApiKeyMutations"
import { useCopyToClipboard } from "@/hooks/useCopyToClipboard"

const formSchema = z.object({
  name: z.string().min(1, { message: "Name is required" }),
  expires_at: z.string().optional(),
})

type FormData = z.infer<typeof formSchema>

const QR_PAYLOAD_VERSION = "0.1"

interface AddApiKeyProps {
  open?: boolean
  onOpenChange?: (open: boolean) => void
}

const AddApiKey = ({ open, onOpenChange }: AddApiKeyProps) => {
  const { t } = useTranslation()
  const [internalOpen, setInternalOpen] = useState(false)
  const isOpen = open ?? internalOpen
  const setIsOpen = onOpenChange ?? setInternalOpen
  const [createdKey, setCreatedKey] = useState<string | null>(null)
  const [copiedText, copyToClipboard] = useCopyToClipboard()

  const form = useForm<FormData>({
    resolver: zodResolver(formSchema),
    mode: "onBlur",
    criteriaMode: "all",
    defaultValues: {
      name: "",
      expires_at: "",
    },
  })

  const createApiKeyMutation = useCreateApiKey()

  const onSubmit = (data: FormData) => {
    const payload = {
      name: data.name,
      ...(data.expires_at ? { expires_at: data.expires_at } : {}),
    }
    createApiKeyMutation.mutate(payload, {
      onSuccess: (response) => {
        setCreatedKey(response?.key ?? null)
      },
    })
  }

  const handleClose = (open: boolean) => {
    if (!open) {
      form.reset()
      setCreatedKey(null)
      createApiKeyMutation.reset()
    }
    setIsOpen(open)
  }

  const getQrPayload = (apiKey: string) => {
    const payload = {
      // VITE_API_URL is undefined in production builds (frontend embedded in
      // the backend) — fall back to the origin serving the app.
      server_instance: import.meta.env.VITE_API_URL || window.location.origin,
      api_key: apiKey,
      version: QR_PAYLOAD_VERSION,
    }
    return JSON.stringify(payload)
  }

  return (
    <Dialog open={isOpen} onOpenChange={handleClose}>
      <DialogTrigger asChild>
        <Button className="my-4">
          <Plus />
          {t("apiKeys.createApiKey")}
        </Button>
      </DialogTrigger>
      <DialogContent
        className="sm:max-w-md"
        onInteractOutside={(e) => e.preventDefault()}
      >
        {createdKey ? (
          <>
            <DialogHeader>
              <DialogTitle>{t("apiKeys.apiKeyCreated")}</DialogTitle>
              <DialogDescription>
                {t("apiKeys.apiKeyCreatedMsg")}
              </DialogDescription>
            </DialogHeader>
            <div className="flex flex-col gap-4 py-4">
              <div className="flex flex-col items-center gap-3">
                <div className="rounded-lg border bg-white p-4">
                  <Cuer.Root value={getQrPayload(createdKey)} size={200}>
                    <Cuer.Finder fill="black" />
                    <Cuer.Cells fill="black" />
                  </Cuer.Root>
                </div>
                <p className="text-sm text-muted-foreground text-center">
                  {t("apiKeys.scanQrCode")}
                </p>
              </div>
              <div className="flex items-center gap-2">
                <Input
                  value={createdKey}
                  readOnly
                  className="font-mono text-sm"
                />
                <Button
                  variant="outline"
                  size="icon"
                  aria-label={
                    copiedText ? t("common.copied") : t("devices.copyApiKey")
                  }
                  onClick={() => copyToClipboard(createdKey)}
                >
                  {copiedText ? (
                    <Check className="h-4 w-4" />
                  ) : (
                    <Copy className="h-4 w-4" />
                  )}
                </Button>
              </div>
            </div>
            <DialogFooter>
              <DialogClose asChild>
                <Button>{t("common.done")}</Button>
              </DialogClose>
            </DialogFooter>
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>{t("apiKeys.createTitle")}</DialogTitle>
              <DialogDescription>{t("apiKeys.createDesc")}</DialogDescription>
            </DialogHeader>
            <Form {...form}>
              <form onSubmit={form.handleSubmit(onSubmit)}>
                <div className="grid gap-4 py-4">
                  <FormField
                    control={form.control}
                    name="name"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>
                          {t("apiKeys.nameLabel")}{" "}
                          <span className="text-destructive">*</span>
                        </FormLabel>
                        <FormControl>
                          <Input
                            placeholder={t("apiKeys.namePlaceholder")}
                            type="text"
                            {...field}
                            required
                          />
                        </FormControl>
                        <FormDescription>
                          {t("apiKeys.nameDesc")}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name="expires_at"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t("apiKeys.expiration")}</FormLabel>
                        <FormControl>
                          <Input
                            type="date"
                            min={new Date().toISOString().split("T")[0]}
                            {...field}
                          />
                        </FormControl>
                        <FormDescription>
                          {t("apiKeys.expirationDesc")}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>

                <DialogFooter>
                  <DialogClose asChild>
                    <Button
                      variant="outline"
                      disabled={createApiKeyMutation.isPending}
                    >
                      {t("common.cancel")}
                    </Button>
                  </DialogClose>
                  <LoadingButton
                    type="submit"
                    loading={createApiKeyMutation.isPending}
                  >
                    {t("common.create")}
                  </LoadingButton>
                </DialogFooter>
              </form>
            </Form>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

export default AddApiKey
