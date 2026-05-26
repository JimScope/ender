import { zodResolver } from "@hookform/resolvers/zod"
import { Plus } from "lucide-react"
import { useEffect, useState } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { z } from "zod"
import { MultiSelect } from "@/components/Common/MultiSelect"
import { TemplateSelect } from "@/components/Templates/TemplateSelect"
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { TagInput } from "@/components/ui/tag-input"
import { Textarea } from "@/components/ui/textarea"
import { useDeviceList } from "@/hooks/useDeviceList"
import { useCreateScheduledSMS } from "@/hooks/useScheduledSMSMutations"
import { COMMON_TIMEZONES } from "@/lib/constants"
import {
  getBrowserTimezone,
  naiveDatetimeToUTC,
  nowLocalDatetime,
} from "@/lib/datetime"

const formSchema = z
  .object({
    name: z.string().min(1, "Name is required").max(100),
    recipients: z
      .array(z.e164().min(1))
      .min(1, "At least one recipient is required"),
    body: z.string().min(1, "Message body is required").max(1600),
    device_id: z.array(z.string()).optional(),
    schedule_type: z.enum(["one_time", "recurring"]),
    scheduled_at: z.string().optional(),
    cron_expression: z.string().optional(),
    timezone: z.string().min(1),
  })
  .superRefine((data, ctx) => {
    if (data.schedule_type !== "one_time" || !data.scheduled_at) return
    const whenUtc = new Date(
      naiveDatetimeToUTC(data.scheduled_at, data.timezone),
    ).getTime()
    if (Number.isNaN(whenUtc) || whenUtc <= Date.now()) {
      ctx.addIssue({
        code: "custom",
        path: ["scheduled_at"],
        message: "scheduled_at must be in the future",
      })
    }
  })

type FormData = z.infer<typeof formSchema>

const AddScheduledSMS = () => {
  const { t } = useTranslation()
  const [isOpen, setIsOpen] = useState(false)
  const { data: devices } = useDeviceList()

  const form = useForm<FormData>({
    resolver: zodResolver(formSchema),
    mode: "onBlur",
    criteriaMode: "all",
    defaultValues: {
      name: "",
      recipients: [],
      body: "",
      device_id: [],
      schedule_type: "one_time",
      scheduled_at: "",
      cron_expression: "",
      timezone: getBrowserTimezone(),
    },
  })

  useEffect(() => {
    if (devices?.data?.length === 1 && !form.getValues("device_id")?.length) {
      form.setValue("device_id", [devices.data[0].id])
    }
  }, [devices, form])

  const scheduleType = form.watch("schedule_type")
  const createScheduledSMSMutation = useCreateScheduledSMS()

  const onSubmit = (data: FormData) => {
    createScheduledSMSMutation.mutate(
      {
        name: data.name,
        recipients: data.recipients,
        body: data.body,
        device_id: data.device_id?.[0] || undefined,
        schedule_type: data.schedule_type,
        scheduled_at:
          data.schedule_type === "one_time"
            ? naiveDatetimeToUTC(data.scheduled_at!, data.timezone)
            : undefined,
        cron_expression:
          data.schedule_type === "recurring" ? data.cron_expression : undefined,
        timezone: data.timezone,
      },
      {
        onSuccess: () => {
          form.reset()
          setIsOpen(false)
        },
      },
    )
  }

  return (
    <Dialog open={isOpen} onOpenChange={setIsOpen}>
      <DialogTrigger asChild>
        <Button className="my-4">
          <Plus />
          {t("scheduled.addScheduled")}
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("scheduled.createTitle")}</DialogTitle>
          <DialogDescription>{t("scheduled.createDesc")}</DialogDescription>
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
                      {t("common.name")}{" "}
                      <span className="text-destructive">*</span>
                    </FormLabel>
                    <FormControl>
                      <Input
                        placeholder={t("scheduled.scheduleName")}
                        {...field}
                        required
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <Controller
                name="recipients"
                control={form.control}
                render={({ field, fieldState }) => (
                  <FormItem>
                    <FormLabel>
                      {t("scheduled.recipients")}{" "}
                      <span className="text-destructive">*</span>
                    </FormLabel>
                    <TagInput
                      {...field}
                      id={field.name}
                      placeholder={t("sms.recipientPlaceholder")}
                      aria-invalid={fieldState.invalid}
                    />
                    {fieldState.error && (
                      <FormMessage>{fieldState.error.message}</FormMessage>
                    )}
                  </FormItem>
                )}
              />

              <div className="space-y-2">
                <TemplateSelect
                  onSelect={(tmpl) => {
                    if (tmpl) form.setValue("body", tmpl.body)
                  }}
                />
              </div>

              <FormField
                control={form.control}
                name="body"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t("scheduled.bodyLabel")}{" "}
                      <span className="text-destructive">*</span>
                    </FormLabel>
                    <FormControl>
                      <Textarea
                        placeholder={t("scheduled.bodyPlaceholder")}
                        rows={3}
                        {...field}
                        required
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <Controller
                name="device_id"
                control={form.control}
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t("sms.device")}</FormLabel>
                    <MultiSelect
                      options={(devices?.data || []).map((device) => ({
                        label: device.name || device.id,
                        value: device.id,
                      }))}
                      onValueChange={field.onChange}
                      defaultValue={field.value}
                    />
                    <FormDescription>
                      {t("scheduled.leaveEmpty")}
                    </FormDescription>
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="schedule_type"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t("scheduled.scheduleType")}{" "}
                      <span className="text-destructive">*</span>
                    </FormLabel>
                    <Select
                      onValueChange={field.onChange}
                      defaultValue={field.value}
                    >
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        <SelectItem value="one_time">
                          {t("scheduled.oneTime")}
                        </SelectItem>
                        <SelectItem value="recurring">
                          {t("scheduled.recurring")}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {scheduleType === "one_time" && (
                <FormField
                  control={form.control}
                  name="scheduled_at"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t("scheduled.sendAt")}{" "}
                        <span className="text-destructive">*</span>
                      </FormLabel>
                      <FormControl>
                        <Input
                          type="datetime-local"
                          min={nowLocalDatetime()}
                          {...field}
                          required
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}

              {scheduleType === "recurring" && (
                <FormField
                  control={form.control}
                  name="cron_expression"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t("scheduled.cronExpression")}{" "}
                        <span className="text-destructive">*</span>
                      </FormLabel>
                      <FormControl>
                        <Input placeholder="*/5 * * * *" {...field} required />
                      </FormControl>
                      <FormDescription>
                        {t("scheduled.cronFormat")}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}

              <FormField
                control={form.control}
                name="timezone"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t("scheduled.timezone")}</FormLabel>
                    <Select
                      onValueChange={field.onChange}
                      defaultValue={field.value}
                    >
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        {COMMON_TIMEZONES.map((tz) => (
                          <SelectItem key={tz} value={tz}>
                            {tz}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>

            <DialogFooter>
              <DialogClose asChild>
                <Button
                  variant="outline"
                  disabled={createScheduledSMSMutation.isPending}
                >
                  {t("common.cancel")}
                </Button>
              </DialogClose>
              <LoadingButton
                type="submit"
                loading={createScheduledSMSMutation.isPending}
              >
                {t("scheduled.createSchedule")}
              </LoadingButton>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  )
}

export default AddScheduledSMS
