import { createFileRoute } from "@tanstack/react-router"
import { BookOpen, Copy, ExternalLink, Key, Plus } from "lucide-react"
import { Suspense, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"

import AddApiKey from "@/components/ApiKeys/AddApiKey"
import { getColumns } from "@/components/ApiKeys/columns"
import { DataTable } from "@/components/Common/DataTable"
import PendingApiKeys from "@/components/Pending/PendingApiKeys"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { useApiKeyListSuspense } from "@/hooks/useApiKeyList"
import useAppConfig from "@/hooks/useAppConfig"
import type { ApiKey } from "@/types/collections"

export const Route = createFileRoute("/_layout/integrations")({
  component: Integrations,
})

function ApiKeysEmptyState({ onAddApiKey }: { onAddApiKey: () => void }) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col items-center justify-center text-center py-12">
      <div className="rounded-full bg-muted p-4 mb-4">
        <Key className="h-8 w-8 text-muted-foreground" />
      </div>
      <h2 className="text-lg font-semibold">{t("apiKeys.noApiKeys")}</h2>
      <p className="text-muted-foreground">{t("apiKeys.noApiKeysDesc")}</p>
      <Button className="my-4" onClick={onAddApiKey}>
        <Plus />
        {t("apiKeys.createApiKey")}
      </Button>
    </div>
  )
}

function ApiKeysTableContent({ onAddApiKey }: { onAddApiKey: () => void }) {
  const { t } = useTranslation()
  const columns = useMemo(() => getColumns(t), [t])
  const { data: apiKeys } = useApiKeyListSuspense()

  if (!apiKeys?.data || apiKeys.data.length === 0) {
    return <ApiKeysEmptyState onAddApiKey={onAddApiKey} />
  }

  return (
    <DataTable
      columns={columns}
      data={(apiKeys?.data ?? []) as unknown as ApiKey[]}
      caption={t("apiKeys.title")}
      totalCount={apiKeys?.count}
    />
  )
}

function ApiKeysTable({ onAddApiKey }: { onAddApiKey: () => void }) {
  return (
    <Suspense fallback={<PendingApiKeys />}>
      <ApiKeysTableContent onAddApiKey={onAddApiKey} />
    </Suspense>
  )
}

const SDK_DOCS_URL = "https://vendel.cc/docs/api/sdks/"

const sdks = [
  {
    titleKey: "apiKeys.sdkJsTitle",
    descKey: "apiKeys.sdkJsDesc",
    install: "npm install vendel-sdk",
    icon: "JS",
    color: "bg-yellow-500/15 text-yellow-600 dark:text-yellow-400",
  },
  {
    titleKey: "apiKeys.sdkPythonTitle",
    descKey: "apiKeys.sdkPythonDesc",
    install: "pip install vendel-sdk",
    icon: "PY",
    color: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
  },
  {
    titleKey: "apiKeys.sdkGoTitle",
    descKey: "apiKeys.sdkGoDesc",
    install: "go get github.com/JimScope/vendel-sdk-go",
    icon: "GO",
    color: "bg-cyan-500/15 text-cyan-600 dark:text-cyan-400",
  },
] as const

function SdkCard({
  titleKey,
  descKey,
  install,
  icon,
  color,
}: (typeof sdks)[number]) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)

  const handleCopy = () => {
    navigator.clipboard.writeText(install)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center gap-3 space-y-0 pb-2">
        <div
          className={`flex h-9 w-9 items-center justify-center rounded-md text-xs font-bold ${color}`}
        >
          {icon}
        </div>
        <div className="flex-1">
          <CardTitle className="text-sm font-medium">{t(titleKey)}</CardTitle>
          <CardDescription className="text-xs">{t(descKey)}</CardDescription>
        </div>
      </CardHeader>
      <CardContent>
        <div className="flex items-center gap-2">
          <code className="flex-1 rounded-md bg-muted px-3 py-2 text-xs font-mono truncate">
            {install}
          </code>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={handleCopy}
            className="shrink-0"
          >
            <Copy className={`size-3.5 ${copied ? "text-brand" : ""}`} />
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

function Integrations() {
  const { t } = useTranslation()
  const { config } = useAppConfig()
  const [addApiKeyOpen, setAddApiKeyOpen] = useState(false)

  return (
    <div className="flex flex-col gap-6">
      <title>{`${t("apiKeys.title")} - ${config.appName}`}</title>
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl">{t("apiKeys.title")}</h1>
          <p className="text-muted-foreground">{t("apiKeys.description")}</p>
        </div>
        <AddApiKey open={addApiKeyOpen} onOpenChange={setAddApiKeyOpen} />
      </div>
      <ApiKeysTable onAddApiKey={() => setAddApiKeyOpen(true)} />

      <div className="space-y-4">
        <div>
          <h2 className="text-lg font-semibold">{t("apiKeys.sdksTitle")}</h2>
          <p className="text-sm text-muted-foreground">
            {t("apiKeys.sdksDesc")}
          </p>
        </div>
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {sdks.map((sdk) => (
            <SdkCard key={sdk.icon} {...sdk} />
          ))}
        </div>

        <Card>
          <CardHeader className="flex flex-row items-center gap-3 space-y-0">
            <div className="flex h-9 w-9 items-center justify-center rounded-md bg-brand/15 text-brand">
              <BookOpen className="size-4" />
            </div>
            <div className="flex-1">
              <CardTitle className="text-sm font-medium">
                {t("apiKeys.docsApiTitle")}
              </CardTitle>
              <CardDescription className="text-xs">
                {t("apiKeys.docsApiDesc")}
              </CardDescription>
            </div>
            <a href={SDK_DOCS_URL} target="_blank" rel="noopener noreferrer">
              <Button variant="outline" size="sm">
                <ExternalLink className="size-3.5" />
                {t("apiKeys.sdkDocs")}
              </Button>
            </a>
          </CardHeader>
        </Card>
      </div>
    </div>
  )
}
