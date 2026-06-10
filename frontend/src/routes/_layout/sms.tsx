import { createFileRoute } from "@tanstack/react-router"
import { Search } from "lucide-react"
import { Suspense, useState } from "react"
import { useTranslation } from "react-i18next"

import PendingItems from "@/components/Pending/PendingItems"
import SendSMS from "@/components/Sms/SendSMS"
import { SMSTable } from "@/components/Sms/SMSTable"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import useAppConfig from "@/hooks/useAppConfig"
import { type SMSMessageType, useSMSListSuspense } from "@/hooks/useSMSList"
import type { SMSMessage } from "@/types/collections"

export const Route = createFileRoute("/_layout/sms")({
  component: Sms,
})

function SMSTableContent({ messageType }: { messageType: SMSMessageType }) {
  const { t } = useTranslation()
  const { data: sms } = useSMSListSuspense(messageType)

  if (!sms || sms.data.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center text-center py-12">
        <div className="rounded-full bg-muted p-4 mb-4">
          <Search className="h-8 w-8 text-muted-foreground" />
        </div>
        <h2 className="text-lg font-semibold">{t("sms.noMessages")}</h2>
        <p className="text-muted-foreground">
          {messageType === "incoming"
            ? t("sms.noIncoming")
            : messageType === "outgoing"
              ? t("sms.noOutgoing")
              : t("sms.noAll")}
        </p>
        {messageType !== "incoming" && <SendSMS />}
      </div>
    )
  }

  return (
    <SMSTable
      data={sms.data as unknown as SMSMessage[]}
      totalCount={sms.count}
    />
  )
}

function Sms() {
  const { t } = useTranslation()
  const [messageType, setMessageType] = useState<SMSMessageType>("all")
  const { config } = useAppConfig()

  return (
    <div className="flex flex-col gap-6">
      <title>{`${t("sms.messageLogs")} - ${config.appName}`}</title>
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl">{t("sms.title")}</h1>
          <p className="text-muted-foreground">{t("sms.description")}</p>
        </div>
        <SendSMS />
      </div>

      <Tabs
        value={messageType}
        onValueChange={(value) => setMessageType(value as SMSMessageType)}
      >
        <TabsList>
          <TabsTrigger value="all">{t("sms.tabAll")}</TabsTrigger>
          <TabsTrigger value="outgoing">{t("sms.tabSent")}</TabsTrigger>
          <TabsTrigger value="incoming">{t("sms.tabReceived")}</TabsTrigger>
        </TabsList>
      </Tabs>

      <Suspense fallback={<PendingItems />}>
        <SMSTableContent messageType={messageType} />
      </Suspense>
    </div>
  )
}
