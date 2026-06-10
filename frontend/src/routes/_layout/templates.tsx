import { createFileRoute } from "@tanstack/react-router"
import { FileText } from "lucide-react"
import { Suspense, useMemo } from "react"
import { useTranslation } from "react-i18next"

import { DataTable } from "@/components/Common/DataTable"
import PendingTemplates from "@/components/Pending/PendingTemplates"
import AddTemplate from "@/components/Templates/AddTemplate"
import { getColumns } from "@/components/Templates/columns"
import useAppConfig from "@/hooks/useAppConfig"
import { useTemplateListSuspense } from "@/hooks/useTemplateList"
import type { SMSTemplate } from "@/types/collections"

export const Route = createFileRoute("/_layout/templates")({
  component: Templates,
})

function TemplatesTableContent() {
  const { t } = useTranslation()
  const columns = useMemo(() => getColumns(t), [t])
  const { data: templates } = useTemplateListSuspense()

  if (!templates?.data || templates.data.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center text-center py-12">
        <div className="rounded-full bg-muted p-4 mb-4">
          <FileText className="h-8 w-8 text-muted-foreground" />
        </div>
        <h2 className="text-lg font-semibold">{t("templates.noTemplates")}</h2>
        <p className="text-muted-foreground">
          {t("templates.noTemplatesDesc")}
        </p>
        <AddTemplate />
      </div>
    )
  }

  return (
    <DataTable
      columns={columns}
      data={(templates?.data ?? []) as unknown as SMSTemplate[]}
      caption={t("templates.title")}
      totalCount={templates?.count}
    />
  )
}

function TemplatesTable() {
  return (
    <Suspense fallback={<PendingTemplates />}>
      <TemplatesTableContent />
    </Suspense>
  )
}

function Templates() {
  const { t } = useTranslation()
  const { config } = useAppConfig()

  return (
    <div className="flex flex-col gap-6">
      <title>{`${t("templates.title")} - ${config.appName}`}</title>
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl">{t("templates.title")}</h1>
          <p className="text-muted-foreground">{t("templates.description")}</p>
        </div>
        <AddTemplate />
      </div>
      <TemplatesTable />
    </div>
  )
}
