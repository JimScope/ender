import { zodResolver } from "@hookform/resolvers/zod"
import { Cuer } from "cuer"
import {
  ArrowLeft,
  ArrowRight,
  Check,
  Copy,
  Plus,
  Server,
  Smartphone,
  Usb,
} from "lucide-react"
import { useState } from "react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { z } from "zod"

import AndroidAppDownload from "@/components/Devices/AndroidAppDownload"
import ModemAgentDownload from "@/components/Devices/ModemAgentDownload"
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
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form"
import { Input } from "@/components/ui/input"
import { LoadingButton } from "@/components/ui/loading-button"
import useAppConfig from "@/hooks/useAppConfig"
import { useCopyToClipboard } from "@/hooks/useCopyToClipboard"
import { useCreateDevice } from "@/hooks/useDeviceMutations"
import { cn } from "@/lib/utils"

const formSchema = z.object({
  name: z
    .string()
    .min(1, { message: "Name is required" })
    .max(255, { message: "Name must be at most 255 characters" }),
  phone_number: z
    .e164()
    .min(1, { message: "Phone number is required" })
    .max(20, { message: "Phone number must be at most 20 characters" }),
})

const smppFormSchema = z.object({
  host: z.string().min(1, { message: "Host is required" }),
  port: z.number().int().min(1).max(65535),
  system_id: z.string().min(1, { message: "System ID is required" }),
  password: z.string().min(1, { message: "Password is required" }),
  system_type: z.string().optional(),
  bind_mode: z.enum(["tx", "rx", "trx"]),
})

type FormData = z.infer<typeof formSchema>
type SMPPFormData = z.infer<typeof smppFormSchema>
type DeviceType = "android" | "modem" | "smpp"

const QR_PAYLOAD_VERSION = "0.1"
const TOTAL_STEPS = 4

interface AddDeviceProps {
  open?: boolean
  onOpenChange?: (open: boolean) => void
}

function StepIndicator({ current, total }: { current: number; total: number }) {
  return (
    <div className="flex items-center gap-1.5" aria-hidden="true">
      {Array.from({ length: total }).map((_, i) => (
        <div
          key={i}
          className={cn(
            "h-1 flex-1 rounded-full transition-colors",
            i < current ? "bg-primary" : "bg-muted",
          )}
        />
      ))}
    </div>
  )
}

const AddDevice = ({ open, onOpenChange }: AddDeviceProps) => {
  const { t } = useTranslation()
  const [internalOpen, setInternalOpen] = useState(false)
  const isOpen = open ?? internalOpen
  const setIsOpen = onOpenChange ?? setInternalOpen
  const [step, setStep] = useState(1)
  const [deviceType, setDeviceType] = useState<DeviceType>("android")
  const [apiKey, setApiKey] = useState<string | null>(null)
  const [copiedText, copyToClipboard] = useCopyToClipboard()
  const { config } = useAppConfig()

  const form = useForm<FormData>({
    resolver: zodResolver(formSchema),
    mode: "onBlur",
    criteriaMode: "all",
    defaultValues: {
      name: "",
      phone_number: "",
    },
  })

  const smppForm = useForm<SMPPFormData>({
    resolver: zodResolver(smppFormSchema),
    mode: "onBlur",
    criteriaMode: "all",
    defaultValues: {
      host: "",
      port: 2775,
      system_id: "",
      password: "",
      system_type: "",
      bind_mode: "trx",
    },
  })

  const createDeviceMutation = useCreateDevice()

  const onSubmit = (data: FormData) => {
    const smppData = deviceType === "smpp" ? smppForm.getValues() : undefined
    createDeviceMutation.mutate(
      { ...data, device_type: deviceType, smpp_config: smppData },
      {
        onSuccess: (response) => {
          setApiKey(response?.api_key ?? null)
          setStep(4)
        },
      },
    )
  }

  const handleNextFromDetails = async () => {
    const baseValid = await form.trigger()
    if (!baseValid) return
    if (deviceType === "smpp") {
      const smppValid = await smppForm.trigger()
      if (!smppValid) return
    }
    await form.handleSubmit(onSubmit)()
  }

  const handleClose = (open: boolean) => {
    if (!open) {
      setStep(1)
      setDeviceType("android")
      setApiKey(null)
      form.reset()
      smppForm.reset()
    }
    setIsOpen(open)
  }

  const getQrPayload = (deviceApiKey: string) => {
    const payload = {
      server_instance: import.meta.env.VITE_API_URL || window.location.origin,
      api_key: deviceApiKey,
      version: QR_PAYLOAD_VERSION,
    }
    return JSON.stringify(payload)
  }

  const stepTitle = () => {
    switch (step) {
      case 1:
        return t("devices.stepTypeTitle")
      case 2:
        return t("devices.stepDownloadTitle")
      case 3:
        return t("devices.stepDetailsTitle")
      case 4:
        return t("devices.deviceCreated")
      default:
        return t("devices.addDevice")
    }
  }

  const stepDescription = () => {
    switch (step) {
      case 1:
        return t("devices.stepTypeDesc")
      case 2:
        if (deviceType === "android") return t("devices.stepDownloadAndroidDesc")
        if (deviceType === "smpp") return t("devices.stepDownloadSmppDesc")
        return t("devices.stepDownloadModemDesc")
      case 3:
        return t("devices.stepDetailsDesc")
      case 4:
        return t("devices.deviceCreatedMsg")
      default:
        return ""
    }
  }

  return (
    <Dialog open={isOpen} onOpenChange={handleClose}>
      <DialogTrigger asChild>
        <Button className="my-4">
          <Plus />
          {t("devices.addDevice")}
        </Button>
      </DialogTrigger>
      <DialogContent
        className="sm:max-w-md"
        onInteractOutside={(e) => e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>{stepTitle()}</DialogTitle>
          <DialogDescription>{stepDescription()}</DialogDescription>
        </DialogHeader>

        <StepIndicator current={step} total={TOTAL_STEPS} />

        {step === 1 && (
          <>
            <div className="flex flex-col gap-3 py-2">
              <button
                type="button"
                onClick={() => setDeviceType("android")}
                className={cn(
                  "flex items-start gap-3 rounded-lg border p-4 text-left transition-colors hover:bg-accent",
                  deviceType === "android"
                    ? "border-primary ring-1 ring-primary"
                    : "border-border",
                )}
              >
                <Smartphone className="size-5 text-brand shrink-0 mt-0.5" />
                <div className="flex-1">
                  <div className="font-medium">{t("devices.androidPhone")}</div>
                  <div className="text-sm text-muted-foreground">
                    {t("devices.androidPhoneDesc")}
                  </div>
                </div>
              </button>
              <button
                type="button"
                onClick={() => setDeviceType("modem")}
                className={cn(
                  "flex items-start gap-3 rounded-lg border p-4 text-left transition-colors hover:bg-accent",
                  deviceType === "modem"
                    ? "border-primary ring-1 ring-primary"
                    : "border-border",
                )}
              >
                <Usb className="size-5 text-brand shrink-0 mt-0.5" />
                <div className="flex-1">
                  <div className="font-medium">{t("devices.usbModem")}</div>
                  <div className="text-sm text-muted-foreground">
                    {t("devices.usbModemDesc")}
                  </div>
                </div>
              </button>
              <button
                type="button"
                onClick={() => setDeviceType("smpp")}
                className={cn(
                  "flex items-start gap-3 rounded-lg border p-4 text-left transition-colors hover:bg-accent",
                  deviceType === "smpp"
                    ? "border-primary ring-1 ring-primary"
                    : "border-border",
                )}
              >
                <Server className="size-5 text-brand shrink-0 mt-0.5" />
                <div className="flex-1">
                  <div className="font-medium">{t("devices.smppGateway")}</div>
                  <div className="text-sm text-muted-foreground">
                    {t("devices.smppGatewayDesc")}
                  </div>
                </div>
              </button>
            </div>
            <DialogFooter>
              <DialogClose asChild>
                <Button variant="outline">{t("common.cancel")}</Button>
              </DialogClose>
              <Button onClick={() => setStep(2)}>
                {t("common.next")}
                <ArrowRight />
              </Button>
            </DialogFooter>
          </>
        )}

        {step === 2 && (
          <>
            <div className="rounded-lg border p-4 py-2">
              {deviceType === "android" ? (
                <AndroidAppDownload />
              ) : deviceType === "smpp" ? (
                <div className="text-sm text-muted-foreground space-y-2">
                  <p>{t("devices.smppAgentInfo")}</p>
                  <pre className="bg-muted rounded p-2 overflow-x-auto font-mono text-xs">
                    docker compose --profile smpp up -d smpp-agent
                  </pre>
                </div>
              ) : (
                <ModemAgentDownload />
              )}
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setStep(1)}>
                <ArrowLeft />
                {t("common.back")}
              </Button>
              <Button onClick={() => setStep(3)}>
                {t("devices.iveInstalled")}
                <ArrowRight />
              </Button>
            </DialogFooter>
          </>
        )}

        {step === 3 && (
          <Form {...form}>
            <div>
              <div className="grid gap-4 py-4 max-h-[60vh] overflow-y-auto">
                <FormField
                  control={form.control}
                  name="name"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t("common.name")}{" "}
                        <span className="text-destructive">*</span>
                      </FormLabel>
                      <FormControl>
                        <Input
                          placeholder={t("devices.namePlaceholder")}
                          type="text"
                          {...field}
                          required
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name="phone_number"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t("devices.phoneNumber")}{" "}
                        <span className="text-destructive">*</span>
                      </FormLabel>
                      <FormControl>
                        <Input
                          placeholder={t("devices.phonePlaceholder")}
                          type="tel"
                          {...field}
                          required
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                {deviceType === "smpp" && (
                  <Form {...smppForm}>
                    <div className="border-t pt-4">
                      <h4 className="font-medium mb-2">{t("devices.smppSettings")}</h4>
                    </div>
                    <FormField
                      control={smppForm.control}
                      name="host"
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>
                            {t("devices.smppHost")}{" "}
                            <span className="text-destructive">*</span>
                          </FormLabel>
                          <FormControl>
                            <Input placeholder="smpp.provider.com" {...field} required />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={smppForm.control}
                      name="port"
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>
                            {t("devices.smppPort")}{" "}
                            <span className="text-destructive">*</span>
                          </FormLabel>
                          <FormControl>
                            <Input
                              type="number"
                              value={field.value ?? ""}
                              onChange={(e) =>
                                field.onChange(
                                  e.target.value === ""
                                    ? undefined
                                    : Number(e.target.value),
                                )
                              }
                              onBlur={field.onBlur}
                              name={field.name}
                              ref={field.ref}
                              required
                            />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={smppForm.control}
                      name="system_id"
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>
                            {t("devices.smppSystemId")}{" "}
                            <span className="text-destructive">*</span>
                          </FormLabel>
                          <FormControl>
                            <Input {...field} required />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={smppForm.control}
                      name="password"
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>
                            {t("devices.smppPassword")}{" "}
                            <span className="text-destructive">*</span>
                          </FormLabel>
                          <FormControl>
                            <Input type="password" {...field} required />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={smppForm.control}
                      name="system_type"
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t("devices.smppSystemType")}</FormLabel>
                          <FormControl>
                            <Input {...field} />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={smppForm.control}
                      name="bind_mode"
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t("devices.smppBindMode")}</FormLabel>
                          <FormControl>
                            <select
                              {...field}
                              className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
                            >
                              <option value="trx">trx (transceiver)</option>
                              <option value="tx">tx (transmitter)</option>
                              <option value="rx">rx (receiver)</option>
                            </select>
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </Form>
                )}
              </div>

              <DialogFooter>
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => setStep(2)}
                  disabled={createDeviceMutation.isPending}
                >
                  <ArrowLeft />
                  {t("common.back")}
                </Button>
                <LoadingButton
                  type="button"
                  onClick={handleNextFromDetails}
                  loading={createDeviceMutation.isPending}
                >
                  {t("devices.createDevice")}
                </LoadingButton>
              </DialogFooter>
            </div>
          </Form>
        )}

        {step === 4 && apiKey && (
          <>
            <div className="flex flex-col gap-4 py-4">
              {deviceType === "android" && (
                <div className="flex flex-col items-center gap-3">
                  <div
                    className="rounded-lg border bg-white p-4"
                    role="img"
                    aria-label="QR code containing device connection details"
                  >
                    <Cuer.Root value={getQrPayload(apiKey)} size={200}>
                      <Cuer.Finder fill="black" />
                      <Cuer.Cells fill="black" />
                    </Cuer.Root>
                  </div>
                  <p className="text-sm text-muted-foreground text-center">
                    {t("devices.scanQrCode")} {config.appName}{" "}
                    {t("devices.toConnectAutomatically")}
                  </p>
                </div>
              )}
              <div className="flex items-center gap-2">
                <Input value={apiKey} readOnly className="font-mono text-sm" />
                <Button
                  variant="outline"
                  size="icon"
                  onClick={() => copyToClipboard(apiKey)}
                  aria-label={
                    copiedText ? t("common.copied") : t("devices.copyApiKey")
                  }
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
              <Button onClick={() => handleClose(false)}>
                {t("common.done")}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

export default AddDevice
