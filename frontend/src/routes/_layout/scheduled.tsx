import { createFileRoute } from "@tanstack/react-router"
import { Clock } from "lucide-react"
import { Suspense, useMemo } from "react"
import { useTranslation } from "react-i18next"

import { DataTable } from "@/components/Common/DataTable"
import PendingScheduledSMS from "@/components/Pending/PendingScheduledSMS"
import AddScheduledSMS from "@/components/ScheduledSMS/AddScheduledSMS"
import { getColumns } from "@/components/ScheduledSMS/columns"
import useAppConfig from "@/hooks/useAppConfig"
import { useScheduledSMSListSuspense } from "@/hooks/useScheduledSMSList"
import type { ScheduledSMS } from "@/types/collections"

export const Route = createFileRoute("/_layout/scheduled")({
  component: Scheduled,
})

function ScheduledTableContent() {
  const { t } = useTranslation()
  const columns = useMemo(() => getColumns(t), [t])
  const { data: scheduled } = useScheduledSMSListSuspense()

  if (!scheduled?.data || scheduled.data.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center text-center py-12">
        <div className="rounded-full bg-muted p-4 mb-4">
          <Clock className="h-8 w-8 text-muted-foreground" />
        </div>
        <h2 className="text-lg font-semibold">{t("scheduled.noScheduled")}</h2>
        <p className="text-muted-foreground">
          {t("scheduled.noScheduledDesc")}
        </p>
        <AddScheduledSMS />
      </div>
    )
  }

  return (
    <DataTable
      columns={columns}
      data={(scheduled?.data ?? []) as unknown as ScheduledSMS[]}
      caption={t("scheduled.title")}
      totalCount={scheduled?.count}
    />
  )
}

function ScheduledTable() {
  return (
    <Suspense fallback={<PendingScheduledSMS />}>
      <ScheduledTableContent />
    </Suspense>
  )
}

function Scheduled() {
  const { t } = useTranslation()
  const { config } = useAppConfig()

  return (
    <div className="flex flex-col gap-6">
      <title>{`${t("scheduled.title")} - ${config.appName}`}</title>
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl">{t("scheduled.title")}</h1>
          <p className="text-muted-foreground">{t("scheduled.description")}</p>
        </div>
        <AddScheduledSMS />
      </div>
      <ScheduledTable />
    </div>
  )
}
