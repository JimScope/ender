import { createFileRoute } from "@tanstack/react-router"
import { CreditCard } from "lucide-react"
import { Suspense, useMemo } from "react"
import { useTranslation } from "react-i18next"

import { getColumns } from "@/components/Billing/columns"
import { DataTable } from "@/components/Common/DataTable"
import PendingBilling from "@/components/Pending/PendingBilling"
import useAppConfig from "@/hooks/useAppConfig"
import { usePaymentListSuspense } from "@/hooks/usePaymentList"
import type { Payment } from "@/types/collections"

export const Route = createFileRoute("/_layout/billing")({
  component: Billing,
})

function BillingTableContent() {
  const { t } = useTranslation()
  const { data: payments } = usePaymentListSuspense()
  const columns = useMemo(() => getColumns(t), [t])

  if (!payments?.data || payments.data.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center text-center py-12">
        <div className="rounded-full bg-muted p-4 mb-4">
          <CreditCard className="h-8 w-8 text-muted-foreground" />
        </div>
        <h2 className="text-lg font-semibold">{t("billing.noHistory")}</h2>
        <p className="text-muted-foreground">{t("billing.noHistoryDesc")}</p>
      </div>
    )
  }

  return (
    <DataTable
      columns={columns}
      data={(payments?.data ?? []) as unknown as Payment[]}
      caption={t("billing.paymentHistory")}
      totalCount={payments?.count}
    />
  )
}

function BillingTable() {
  return (
    <Suspense fallback={<PendingBilling />}>
      <BillingTableContent />
    </Suspense>
  )
}

function Billing() {
  const { t } = useTranslation()
  const { config } = useAppConfig()

  return (
    <div className="flex flex-col gap-6">
      <title>{`${t("billing.title")} - ${config.appName}`}</title>
      <div>
        <h1 className="text-2xl">{t("billing.title")}</h1>
        <p className="text-muted-foreground">{t("billing.description")}</p>
      </div>
      <BillingTable />
    </div>
  )
}
