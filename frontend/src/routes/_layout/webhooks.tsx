import { createFileRoute } from "@tanstack/react-router"
import { Webhook } from "lucide-react"
import { Suspense, useMemo } from "react"
import { useTranslation } from "react-i18next"

import { DataTable } from "@/components/Common/DataTable"
import PendingWebhooks from "@/components/Pending/PendingWebhooks"
import AddWebhook from "@/components/Webhooks/AddWebhook"
import { getColumns } from "@/components/Webhooks/columns"
import useAppConfig from "@/hooks/useAppConfig"
import { useWebhookListSuspense } from "@/hooks/useWebhookList"
import type { WebhookConfig } from "@/types/collections"

export const Route = createFileRoute("/_layout/webhooks")({
  component: Webhooks,
})

function WebhooksTableContent() {
  const { t } = useTranslation()
  const { data: webhooks } = useWebhookListSuspense()
  const columns = useMemo(() => getColumns(t), [t])

  if (!webhooks?.data || webhooks.data.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center text-center py-12">
        <div className="rounded-full bg-muted p-4 mb-4">
          <Webhook className="h-8 w-8 text-muted-foreground" />
        </div>
        <h2 className="text-lg font-semibold">{t("webhooks.noWebhooks")}</h2>
        <p className="text-muted-foreground">{t("webhooks.noWebhooksDesc")}</p>
        <AddWebhook />
      </div>
    )
  }

  return (
    <DataTable
      columns={columns}
      data={(webhooks?.data ?? []) as unknown as WebhookConfig[]}
      caption={t("webhooks.title")}
      totalCount={webhooks?.count}
    />
  )
}

function WebhooksTable() {
  return (
    <Suspense fallback={<PendingWebhooks />}>
      <WebhooksTableContent />
    </Suspense>
  )
}

function Webhooks() {
  const { t } = useTranslation()
  const { config } = useAppConfig()

  return (
    <div className="flex flex-col gap-6">
      <title>{`${t("webhooks.title")} - ${config.appName}`}</title>
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl">{t("webhooks.title")}</h1>
          <p className="text-muted-foreground">{t("webhooks.description")}</p>
        </div>
        <AddWebhook />
      </div>
      <WebhooksTable />
    </div>
  )
}
